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
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"math/rand"
	"sync"
	"time"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/core"
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

	initPosWeight = 5000
	posWeightPrecision int64 = 10000
	posWeightMax uint32 = 9500
	posWeightMin uint32 = 500
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
	PowMode        Mode
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
	powTargetTimespan int64
	minDifficulty int64 // The minimum of difficulty. It's also the maximum of delegate miner count, in order to avoid target overflow.
	powTargetSpacing int64

	lock sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
}

const(
	TesterThreads = 1
	//\\PowTargetTimespan = 14 * 24 * 60 * 60
	//\\PowTargetTimespan = 75
	PowTargetSpacing = 10
	MinDifficulty = 131072
)

var PowTargetTimespan = core.BlocksInMatureCycle() * PowTargetSpacing

// New creates a full sized ethash PoW scheme.
func New(config Config) *Ethash {
	return &Ethash{
		config:   config,
		update:   make(chan struct{}),
		hashrate: metrics.NewMeter(),
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewTester creates a small sized ethash PoW scheme useful only for testing
// purposes.
func NewTester() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeTest,
		},
		threads: TesterThreads,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
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
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
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
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
		fakeFail: fail,
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
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
		fakeDelay: delay,
	}
}

// NewFullFaker creates an ethash consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFullFake,
		},
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewShared creates a full sized ethash PoW shared between all requesters running
// in the same process.
func NewShared() *Ethash {
//	return &Ethash{shared: sharedEthash}
	return &Ethash{
		shared: sharedEthash,
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// calculate the pos target.
func (ethash *Ethash) CalcTarget(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {

	if header.Difficulty.Int64() < ethash.minDifficulty {
		panic( fmt.Sprintf("The header difficulty(%d) is less than minDifficulty(%d), header number=%d", header.Difficulty.Int64() , ethash.minDifficulty, header.Number.Int64() ))
	}

	// calc the target
	target := new(big.Int).Div(maxUint256, header.Difficulty)
	//powWeight := posWeightPrecision - int64(header.PosWeight)
	posTargetAvg := new(big.Int).Div(target, big.NewInt(posWeightPrecision))
	posTargetAvg.Mul(posTargetAvg, big.NewInt(int64(header.PosWeight)))
	powTarget := new(big.Int).Sub( target, posTargetAvg)

	//powTarget := new(big.Int).Mul(target, big.NewInt(powWeight))
	matureState := core.GetMatureState(chain, header.Number.Uint64(), headers)
	if matureState == nil || matureState.DelegateMinersCount() == 0 || matureState.DepositBalanceSum().Sign() == 0 {
		return powTarget
	}

	// POS
	_, localSum, _:= matureState.GetDelegateMiner(header.Coinbase)
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

// returns the pos weight in a certain cycle.
func (ethash *Ethash) PosWeight(chain consensus.BlockReader, header *types.Header, headers []*types.Header) uint32 {
	if header.Number.Uint64() < core.MinMatureBlockNumber() {
		return uint32(initPosWeight)
	}

	powProduction := ethash.GetPowProduction(chain, header, headers)
	posProduction := ethash.GetPosProduction(chain, header, headers)
	t := big.NewInt(0)
	if powProduction.Cmp(t) == 0 && posProduction.Cmp(t) == 0 {
		return uint32(initPosWeight)
	}

	x := new(big.Int).Mul(powProduction, big.NewInt(int64(posWeightPrecision)))
	weight := new(big.Int).Div(x, new(big.Int).Add(powProduction, posProduction))
	weight32u := uint32(weight.Uint64())
	if weight32u > posWeightMax {
		weight32u = posWeightMax
	} else if weight32u < posWeightMin {
		weight32u = posWeightMin
	}
	return weight32u
}

func (ethash *Ethash)FindInHeaders(header *types.Header, headers []*types.Header) bool {
	for _, v := range headers {
		if header.Hash().String() == v.Hash().String() {
			return true
		}
	}
	return false
}


// returns the total pow production in the previous mature cycle.
func (ethash *Ethash) GetPowProduction(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {
	sumPow := big.NewInt(0)
	start, end := core.LastMatureCycleRange(header.Number.Uint64())
	for i := start; i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPow.Add(sumPow, h.PowProduction)
		} else if foundHeader := ethash.FindInHeadersByNum(i, headers); foundHeader!=nil {
			sumPow.Add(sumPow, foundHeader.PowProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPow
}

// returns the total pos production in the previous mature cycle.
func (ethash *Ethash) GetPosProduction(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {
	start, end := core.LastMatureCycleRange(header.Number.Uint64())
	sumPos := big.NewInt(0)
	for i := start; i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPos.Add(sumPos, h.PosProduction)
		} else if foundHeader := ethash.FindInHeadersByNum(i, headers); foundHeader!=nil {
			sumPos.Add(sumPos, foundHeader.PosProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPos
}

// returns the total supply of pos in all previous mature cycles.
func (ethash *Ethash) GetPosMatureTotalSupply(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {
	_, end := core.LastMatureCycleRange(header.Number.Uint64())
	sumPos := big.NewInt(0)
	for i := uint64(0); i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPos.Add(sumPos, h.PosProduction)
		} else if foundHeader := ethash.FindInHeadersByNum(i, headers); foundHeader!=nil {
			sumPos.Add(sumPos, foundHeader.PosProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPos
}

// returns the total supply of pow in all previous mature cycles.
func (ethash *Ethash) GetPowMatureTotalSupply(chain consensus.BlockReader, header *types.Header, headers []*types.Header) *big.Int {
	_, end := core.LastMatureCycleRange(header.Number.Uint64())
	sumPow := big.NewInt(0)
	for i := uint64(0); i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPow.Add(sumPow, h.PowProduction)
		} else if foundHeader := ethash.FindInHeadersByNum(i, headers); foundHeader!=nil {
			sumPow.Add(sumPow, foundHeader.PowProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPow
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

func (ethash *Ethash)HashimotoforHeader(hash []byte, nonce uint64) ([]byte) {
	return hashimoto(hash, nonce)
}
