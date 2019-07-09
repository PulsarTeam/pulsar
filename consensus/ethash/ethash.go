// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package ethash implements the ethash proof-of-work consensus engine.
package ethash

import (
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
	"math/big"
	"math/rand"
	"sync"
	"time"
)

var ErrInvalidDumpMagic = errors.New("invalid dump magic")

var (
	// maxUint256 is a big integer representing 2^256-1
	maxUint256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))

	// sharedEthash is a full instance that can be shared between multiple users.
	sharedEthash = New(Config{ModeNormal})

	// algorithmRevision is the data structure version used for file naming.
	algorithmRevision = 23

	// dumpMagic is a dataset dump header to sanity check a data dump.
	dumpMagic = []uint32{0xbaddcafe, 0xfee1dead}

	initPosWeight             = 5000
	posWeightPrecision int64  = 10000
	posWeightMax       uint32 = 9500
	posWeightMin       uint32 = 500
)

// Mode defines the type and amount of PoW verification an ethash engine makes.
type Mode uint

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

// Config are the configuration parameters of the ethash.
type Config struct {
	PowMode Mode
}

// Ethash is a consensus engine based on proof-of-work implementing the ethash
// algorithm.
type Ethash struct {
	config Config

	// Mining related fields
	rand     *rand.Rand    // Properly seeded random source for nonces
	threads  int           // Number of threads to mine on if mining
	update   chan struct{} // Notification channel to update mining parameters
	hashrate metrics.Meter // Meter tracking the average hashrate

	// The fields below are hooks for testing
	shared    *Ethash       // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	//mining params
	difficultyAdjustCycles uint64
	minDifficulty          uint64 // The minimum of difficulty. It's also the maximum of delegate miner count, in order to avoid target overflow.
	powTargetSpacing       uint64

	lock sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
}

const (
	TesterThreads = 1
	//\\PowTargetTimespan = 14 * 24 * 60 * 60
	//\\PowTargetTimespan = 75

	PowTargetSpacing       = 15
	DifficultyAdjustCycles = 1 // how many cycles to adjust
	MinDifficulty          = 131072
)

// New creates a full sized ethash PoW scheme.
func New(config Config) *Ethash {
	return &Ethash{
		config:                 config,
		update:                 make(chan struct{}),
		hashrate:               metrics.NewMeter(),
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
	}
}

// NewTester creates a small sized ethash PoW scheme useful only for testing
// purposes.
func NewTester() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeTest,
		},
		threads:                TesterThreads,
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
	}
}

// NewFaker creates a ethash consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Ethereum
// consensus rules.
func NewFaker() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,
		},
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
	}
}

// NewFakeFailer creates a ethash consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Ethereum consensus rules.
func NewFakeFailer(fail uint64) *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,
		},
		threads:                1,
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
		fakeFail:               fail,
	}
}

// NewFakeDelayer creates a ethash consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Ethereum consensus rules.
func NewFakeDelayer(delay time.Duration) *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,
		},
		threads:                1,
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
		fakeDelay:              delay,
	}
}

// NewFullFaker creates an ethash consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFullFake,
		},
		threads:                1,
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
	}
}

// NewShared creates a full sized ethash PoW shared between all requesters running
// in the same process.
func NewShared() *Ethash {
	//	return &Ethash{shared: sharedEthash}
	return &Ethash{
		shared:                 sharedEthash,
		threads:                1,
		difficultyAdjustCycles: DifficultyAdjustCycles,
		powTargetSpacing:       PowTargetSpacing,
		minDifficulty:          MinDifficulty,
	}
}

// calculate the pos target.
func (ethash *Ethash) CalcTarget(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {

	if header.Difficulty.Uint64() < ethash.minDifficulty {
		panic(fmt.Sprintf("The header difficulty(%d) is less than minDifficulty(%d), header number=%d", header.Difficulty.Int64(), ethash.minDifficulty, header.Number.Int64()))
	}

	// calc the target
	target := new(big.Int).Div(maxUint256, header.Difficulty)
	//powWeight := posWeightPrecision - int64(header.PosWeight)
	posTargetAvg := new(big.Int).Div(target, big.NewInt(posWeightPrecision))
	posTargetAvg.Mul(posTargetAvg, big.NewInt(int64(header.PosWeight)))
	powTarget := new(big.Int).Sub(target, posTargetAvg)

	//powTarget := new(big.Int).Mul(target, big.NewInt(powWeight))
	matureState := core.GetMatureState(chain, header.Number.Uint64(), headers)
	if matureState == nil || matureState.DelegateMinersCount() == 0 || matureState.DepositBalanceSum().Sign() == 0 {
		return powTarget
	}

	// POS
	_, localSum, _ := matureState.GetDelegateMiner(header.Coinbase)
	if localSum == nil || localSum.Sign() == 0 {
		return powTarget
	}

	// notice that the posTargetLocal = posTargetAvg*dmCounts * (posLocalSum/posNetworkSum)

	// for a valid difficulty(>=ethash.minDifficulty), the target*difficulty< MaxBigInt,
	// so for a delegate miner count (dmCounts)<minDifficulty, the posTargetAvg*dmCounts<MaxBigInt.
	// i.e. the max delegate miner count(dmCountMax)=minDifficulty
	tmp := new(big.Int).Mul(posTargetAvg, big.NewInt(int64(matureState.DelegateMinersCount())))
	tmp.Div(tmp, matureState.DepositBalanceSum())
	posTarget := new(big.Int).Mul(tmp, localSum)
	return new(big.Int).Add(powTarget, posTarget)
}

// sanity check the supplies in this header
func (ethash *Ethash) CheckSupplies(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) bool {

	// if not the start of a cycle, just use the parent values
	if header.Number.Uint64() <= core.BlocksInMatureCycle() || ((header.Number.Uint64()-1)%core.BlocksInMatureCycle()) != 0 {
		if header.PowOldMatureSupply.Cmp(parent.PowOldMatureSupply) != 0 {
			log.Error("the PowOldMatureSupply not valid!", "hash", header.Hash(), "have", header.PowOldMatureSupply, "want", parent.PowOldMatureSupply)
			return false
		}
		if header.PowLastMatureCycleSupply.Cmp(parent.PowLastMatureCycleSupply) != 0 {
			log.Error("the PowLastMatureCycleSupply not valid!", "hash", header.Hash(), "have", header.PowLastMatureCycleSupply, "want", parent.PowLastMatureCycleSupply)
			return false
		}
		if header.PowLastCycleSupply.Cmp(parent.PowLastCycleSupply) != 0 {
			log.Error("the PowLastCycleSupply not valid!", "hash", header.Hash(), "have", header.PowLastCycleSupply, "want", parent.PowLastCycleSupply)
			return false
		}
		if header.PosOldMatureSupply.Cmp(parent.PosOldMatureSupply) != 0 {
			log.Error("the PosOldMatureSupply not valid!", "hash", header.Hash(), "have", header.PosOldMatureSupply, "want", parent.PosOldMatureSupply)
			return false
		}
		if header.PosLastMatureCycleSupply.Cmp(parent.PosLastMatureCycleSupply) != 0 {
			log.Error("the PosLastMatureCycleSupply not valid!", "hash", header.Hash(), "have", header.PosLastMatureCycleSupply, "want", parent.PosLastMatureCycleSupply)
			return false
		}
		if header.PosLastCycleSupply.Cmp(parent.PosLastCycleSupply) != 0 {
			log.Error("the PosLastCycleSupply not valid!", "hash", header.Hash(), "have", header.PosLastCycleSupply, "want", parent.PosLastCycleSupply)
			return false
		}
		return true
	}

	// otherwise, calculate the new values
	PowOldMatureSupply := new(big.Int).Add(parent.PowOldMatureSupply, parent.PowLastMatureCycleSupply)
	if header.PowOldMatureSupply.Cmp(PowOldMatureSupply) != 0 {
		log.Error("the PowOldMatureSupply not valid!", "hash", header.Hash(), "have", header.PowOldMatureSupply, "want", PowOldMatureSupply)
		return false
	}
	PowLastMatureCycleSupply := parent.PowLastCycleSupply
	if header.PowLastMatureCycleSupply.Cmp(PowLastMatureCycleSupply) != 0 {
		log.Error("the PowLastMatureCycleSupply not valid!", "hash", header.Hash(), "have", header.PowLastMatureCycleSupply, "want", PowLastMatureCycleSupply)
		return false
	}
	PowLastCycleSupply := ethash.CalcLastCyclePowSupply(chain, header, parent, headers)
	if header.PowLastCycleSupply.Cmp(PowLastCycleSupply) != 0 {
		log.Error("the PowLastCycleSupply not valid!", "hash", header.Hash(), "have", header.PowLastCycleSupply, "want", PowLastCycleSupply)
		return false
	}
	PosOldMatureSupply := new(big.Int).Add(parent.PosOldMatureSupply, parent.PosLastMatureCycleSupply)
	if header.PosOldMatureSupply.Cmp(PosOldMatureSupply) != 0 {
		log.Error("the PosOldMatureSupply not valid!", "hash", header.Hash(), "have", header.PosOldMatureSupply, "want", PosOldMatureSupply)
		return false
	}
	PosLastMatureCycleSupply := parent.PosLastCycleSupply
	if header.PosLastMatureCycleSupply.Cmp(PosLastMatureCycleSupply) != 0 {
		log.Error("the PosLastMatureCycleSupply not valid!", "hash", header.Hash(), "have", header.PosLastMatureCycleSupply, "want", PosLastMatureCycleSupply)
		return false
	}
	PosLastCycleSupply := ethash.CalcLastCyclePosSupply(chain, header, parent, headers)
	if header.PosLastCycleSupply.Cmp(PosLastCycleSupply) != 0 {
		log.Error("the PosLastCycleSupply not valid!", "hash", header.Hash(), "have", header.PosLastCycleSupply, "want", parent.PosLastCycleSupply)
		return false
	}
	return true
}

// update the supplies
func (ethash *Ethash) UpdateSupplies(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) {

	if header.Number.Uint64() < core.BlocksInMatureCycle() || ((header.Number.Uint64()-1)%core.BlocksInMatureCycle()) != 0 {
		header.PowOldMatureSupply = parent.PowOldMatureSupply
		header.PowLastMatureCycleSupply = parent.PowLastMatureCycleSupply
		header.PowLastCycleSupply = parent.PowLastCycleSupply

		header.PosOldMatureSupply = parent.PosOldMatureSupply
		header.PosLastMatureCycleSupply = parent.PosLastMatureCycleSupply
		header.PosLastCycleSupply = parent.PosLastCycleSupply
		return
	}

	header.PowOldMatureSupply = new(big.Int).Add(parent.PowOldMatureSupply, parent.PowLastMatureCycleSupply)
	header.PowLastMatureCycleSupply = parent.PowLastCycleSupply
	header.PowLastCycleSupply = ethash.CalcLastCyclePowSupply(chain, header, parent, nil)

	header.PosOldMatureSupply = new(big.Int).Add(parent.PosOldMatureSupply, parent.PosLastMatureCycleSupply)
	header.PosLastMatureCycleSupply = parent.PosLastCycleSupply
	header.PosLastCycleSupply = ethash.CalcLastCyclePosSupply(chain, header, parent, nil)

}

func (ethash *Ethash) CalcLastCyclePosSupply(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) *big.Int {
	start, end := core.LastCycleRange(header.Number.Uint64())
	sumPos := big.NewInt(0)

	log.Debug("#DEBUG#  CalcLastCyclePosSupply ", "header.Number", header.Number.String(), "header.hash", header.Hash().String(), "start", start, "end", end)

	hash := header.ParentHash
	var h *types.Header = nil
	for {
		h = chain.GetHeaderByHash(hash)
		if h == nil {
			log.Error("FATAL ERROR! CalcLastCyclePosSupply can not get header", "hash", hash)
			panic("Logical error.\n")
		}
		//log.Debug("#DEBUG# CalcLastCyclePosSupply get header", "h.Number", h.Number.String(), "h.Hash", h.Hash().String(), "h.ParentHash", h.ParentHash.String())
		hash = h.ParentHash
		if h.Number.Uint64() >= end {
			continue
		} else if h.Number.Uint64() <= start {
			if start == h.Number.Uint64() {
				sumPos.Add(sumPos, h.PosProduction)
			}
			break
		} else {
			sumPos.Add(sumPos, h.PosProduction)
		}
	}
	return sumPos
}

func (ethash *Ethash) CalcLastCyclePowSupply(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) *big.Int {
	sumPow := big.NewInt(0)
	start, end := core.LastCycleRange(header.Number.Uint64())

	log.Debug("#DEBUG#  CalcLastCyclePowSupply ", "header.Number", header.Number.String(), "header.hash", header.Hash().String(), "start", start, "end", end)

	hash := header.ParentHash
	var h *types.Header = nil
	for {
		h = chain.GetHeaderByHash(hash)
		if h == nil {
			log.Error("FATAL ERROR! CalcLastCyclePowSupply can not get header", "hash", hash)
			panic("Logical error.\n")
		}
		//log.Debug("#DEBUG# CalcLastCyclePowSupply get header", "h.Number", h.Number.String(), "h.Hash", h.Hash().String(), "h.ParentHash", h.ParentHash.String())
		hash = h.ParentHash
		if h.Number.Uint64() >= end {
			continue
		} else if h.Number.Uint64() <= start {
			if start == h.Number.Uint64() {
				sumPow.Add(sumPow, h.PowProduction)
			}
			break
		} else {
			sumPow.Add(sumPow, h.PowProduction)
		}
	}
	return sumPow
}

// returns the pos weight in a certain cycle.
func (ethash *Ethash) PosWeight(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) uint32 {
	if header.Number.Uint64() < core.MinMatureBlockNumber() {
		log.Debug("#1 PosWeight", "no:", header.Number.String(), "hash", header.Hash().String(), "initPosWeight", initPosWeight)
		return uint32(initPosWeight)
	}

	weightAdjustInterval := uint64(core.BlocksInMatureCycle()) * ethash.difficultyAdjustCycles
	if ((header.Number.Uint64() - 1) % weightAdjustInterval) != 0 {
		return parent.PosWeight
	}

	powLastMatureCycleSupply := header.PowLastMatureCycleSupply // ethash.GetPowProduction(chain, header, headers)
	posLastMatureCycleSupply := header.PosLastMatureCycleSupply // ethash.GetPosProduction(chain, header, headers)
	log.Debug("#2 PosWeight", "no:", header.Number.String(), "hash", header.Hash().String(), "powLastMatureCycleSupply", powLastMatureCycleSupply.String(), "posProduction", posLastMatureCycleSupply.String())
	t := big.NewInt(0)
	if powLastMatureCycleSupply.Cmp(t) == 0 && posLastMatureCycleSupply.Cmp(t) == 0 {
		log.Debug("#3 PosWeight", "no:", header.Number.String(), "hash", header.Hash().String(), "initPosWeight", initPosWeight)
		return uint32(initPosWeight)
	}

	x := new(big.Int).Mul(powLastMatureCycleSupply, big.NewInt(int64(posWeightPrecision)))
	weight := new(big.Int).Div(x, new(big.Int).Add(powLastMatureCycleSupply, posLastMatureCycleSupply))
	weight32u := uint32(weight.Uint64())
	if weight32u > posWeightMax {
		weight32u = posWeightMax
	} else if weight32u < posWeightMin {
		weight32u = posWeightMin
	}
	return weight32u
}

func (ethash *Ethash) FindInHeaders(header *types.Header, headers []*types.Header) bool {
	for _, v := range headers {
		if header.Hash().String() == v.Hash().String() {
			return true
		}
	}
	return false
}

// returns the total supply of pos in all previous mature cycles.
func (ethash *Ethash) GetPosMatureTotalSupply(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {

	return big.NewInt(0).Add(header.PosOldMatureSupply, header.PosLastMatureCycleSupply)
}

// returns the total supply of pow in all previous mature cycles.
func (ethash *Ethash) GetPowMatureTotalSupply(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {

	return big.NewInt(0).Add(header.PowOldMatureSupply, header.PowLastMatureCycleSupply)
}

// Threads returns the number of mining threads currently enabled. This doesn't
// necessarily mean that mining is running!
func (ethash *Ethash) Threads() int {
	ethash.lock.Lock()
	defer ethash.lock.Unlock()

	return ethash.threads
}

// SetThreads updates the number of mining threads currently enabled. Calling
// this method does not start mining, only sets the thread count. If zero is
// specified, the miner will use all cores of the machine. Setting a thread
// count below zero is allowed and will cause the miner to idle, without any
// work being done.
func (ethash *Ethash) SetThreads(threads int) {
	ethash.lock.Lock()
	defer ethash.lock.Unlock()

	// If we're running a shared PoW, set the thread count on that instead
	if ethash.shared != nil {
		ethash.shared.SetThreads(threads)
		return
	}
	// Update the threads and ping any running seal to pull in any changes
	ethash.threads = threads
	select {
	case ethash.update <- struct{}{}:
	default:
	}
}

// Hashrate implements PoW, returning the measured rate of the search invocations
// per second over the last minute.
func (ethash *Ethash) Hashrate() float64 {
	return ethash.hashrate.Rate1()
}

// APIs implements consensus.Engine, returning the user facing RPC APIs. Currently
// that is empty.
func (ethash *Ethash) APIs(chain consensus.BlockReader) []rpc.API {
	return nil
}

func (ethash *Ethash) HashimotoforHeader(hash []byte, nonce uint64) []byte {
	return hashimoto(hash, nonce)
}
