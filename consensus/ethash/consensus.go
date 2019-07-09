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

package ethash

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
)

// Ethash proof-of-work protocol constants.
var (
	PulsarBlockReward      *big.Int = new(big.Int).Mul(big.NewInt(128), big.NewInt(1e18)) // Block reward in wei for successfully mining a block
	FrontierBlockReward    *big.Int = PulsarBlockReward                                   // Block reward in wei for successfully mining a block
	ByzantiumBlockReward   *big.Int = PulsarBlockReward                                   // Block reward in wei for successfully mining a block upward from Byzantium
	maxUncles                       = 20                                                  // Maximum number of uncles allowed in a single block
	allowedFutureBlockTime          = 15 * time.Second                                    // Max time from current time allowed for blocks, before they're considered future blocks
	//InterestRate           *big.Int = big.NewInt(100)
	//InterestRatePrecision  *big.Int = big.NewInt(10000000000)
	FeeRatioPrecision *big.Int = big.NewInt(1000000)
	halveIntervalGoal uint64   = (60 * 60 * 24 * 365 / PowTargetSpacing) * 2 // every two years

	PosSupplyLimit          *big.Int = new(big.Int).Mul(new(big.Int).SetUint64(128*core.FixedHalveInterval(halveIntervalGoal)*2), big.NewInt(1e18)) // The PosSupplyLimit is equal to PowSupplyLimit
	PosSupplyN              *big.Int = new(big.Int).SetUint64(core.FixedHalveInterval(halveIntervalGoal) * 10)                                      // doubled after about 20 years, so 5% every year
	PowRewardRatioUncles    *big.Int = big.NewInt(3000)
	PowRewardRatioPrecision *big.Int = big.NewInt(10000)
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	errLargeBlockTime    = errors.New("timestamp too big")
	errZeroBlockTime     = errors.New("timestamp equals parent's")
	errTooManyUncles     = errors.New("too many uncles")
	errDuplicateUncle    = errors.New("duplicate uncle")
	errUncleIsAncestor   = errors.New("uncle is ancestor")
	errDanglingUncle     = errors.New("uncle's parent is not ancestor")
	errInvalidDifficulty = errors.New("non-positive difficulty")
	errInvalidMixDigest  = errors.New("invalid mix digest")
	errInvalidPoW        = errors.New("invalid proof-of-work")
	errInvalidPosWeight  = errors.New("invalid pos weight")
	errRlpEncodeErr      = errors.New("rlp encode error")
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (ethash *Ethash) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the
// stock Ethereum ethash engine.
func (ethash *Ethash) VerifyHeader(chain consensus.BlockReader, header *types.Header, seal bool, headers []*types.Header) error {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake {
		return nil
	}
	// Short circuit if the header is known, or it's parent not
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return ethash.verifyHeader(chain, header, parent, false, seal, headers)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (ethash *Ethash) VerifyHeaders(chain consensus.BlockReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake || len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// Spawn as many workers as allowed threads
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// Create a task channel and spawn the verifiers
	var (
		inputs = make(chan int)
		done   = make(chan int, workers)
		errors = make([]error, len(headers))
		abort  = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		go func() {
			for index := range inputs {
				errors[index] = ethash.verifyHeaderWorker(chain, headers, seals, index)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// Reached end of headers. Stop sending to workers.
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (ethash *Ethash) verifyHeaderWorker(chain consensus.BlockReader, headers []*types.Header, seals []bool, index int) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	if chain.GetHeader(headers[index].Hash(), headers[index].Number.Uint64()) != nil {
		return nil // known block
	}
	return ethash.verifyHeader(chain, headers[index], parent, false, seals[index], headers)
}

func (ethash *Ethash) isBackToPivot(chain consensus.BlockReader, blockHeader *types.Header, ancestors map[common.Hash]*types.Header) bool {
	header := blockHeader
	for i := 0; i < len(ancestors); i++ {
		header = chain.GetHeaderByHash(header.ParentHash)
		if ancestors[header.Hash()] != nil {
			return true
		}
	}
	return false
}

// VerifyUncles verifies that the given block's uncles conform to the consensus
// rules of the stock Ethereum ethash engine.
func (ethash *Ethash) VerifyUncles(chain consensus.BlockReader, block *types.Block) error {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake {
		return nil
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := make(map[common.Hash]*types.Header), make(map[common.Hash]*types.Header)

	ancestorslist := make([]*types.Block, 0)
	refBlocks := make([]*types.Block, 0)
	number, parent := block.NumberU64()-1, block.ParentHash()
	// the furthest acceptable number of a refer block is n-7, whose furthest refer number is (n-7)-7, i.e., n-14
	for i := 0; i < 14; i++ {
		ancestor := chain.GetBlock(parent, number)
		if ancestor == nil {
			break
		}
		ancestors[ancestor.Hash()] = ancestor.Header()
		ancestorslist = append(ancestorslist, ancestor)
		for _, uncle := range ancestor.Uncles() {
			if uncles[uncle.Hash()] == nil {
				uncles[uncle.Hash()] = uncle
			}
		}
		parent, number = ancestor.ParentHash(), number-1
	}
	ancestors[block.Hash()] = block.Header()
	if uncles[block.Hash()] == nil {
		uncles[block.Hash()] = block.Header()
	}
	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		var farthestAncestor uint64
		if block.NumberU64() > 7 {
			farthestAncestor = block.NumberU64() - 7
		} else {
			farthestAncestor = 0
		}
		if uncle.Number.Uint64() < farthestAncestor {
			return errors.New("uncle is too low")
		}

		uncleBlock := chain.GetBlock(uncle.Hash(), uncle.Number.Uint64())
		if uncleBlock == nil {
			return fmt.Errorf("cannot get uncle's block! block=[%d-%s],uncle=[%d-%s]", block.Number().Uint64(), block.Hash().String(), uncle.Number.Uint64(), uncle.Hash().String())
		}

		if !IsTopoPrepared(uncleBlock, ancestorslist, refBlocks) {
			return fmt.Errorf("uncle's topo is not prepared! block=[%d-%s],uncle=[%d-%s]", block.Number().Uint64(), block.Hash().String(), uncle.Number.Uint64(), uncle.Hash().String())
		}
		refBlocks = append(refBlocks, uncleBlock)

		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles[hash] != nil {
			return errDuplicateUncle
		}
		uncles[hash] = uncle

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return errUncleIsAncestor
		}
		var uncleParent *types.Header = ancestors[uncle.ParentHash]

		if uncleParent == nil {
			uncleParent = uncles[uncle.ParentHash]
		}
		if uncleParent == nil {
			return errors.New("uncle's parent should be available within 14 generations")
		}

		if err := ethash.verifyHeader(chain, uncle, uncleParent, true, true, nil); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules of the
// stock Ethereum ethash engine.
// See YP section 4.3.4. "Block Header Validity"
func (ethash *Ethash) verifyHeader(chain consensus.BlockReader, header, parent *types.Header, uncle bool, seal bool, headers []*types.Header) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if uncle {
		if header.Time.Cmp(math.MaxBig256) > 0 {
			return errLargeBlockTime
		}
	} else {
		if header.Time.Cmp(big.NewInt(time.Now().Add(allowedFutureBlockTime).Unix())) > 0 {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time.Cmp(parent.Time) <= 0 {
		return errZeroBlockTime
	}
	// Verify the block's difficulty based in it's timestamp and parent's difficulty
	expected := ethash.CalcDifficulty(chain, header.Time.Uint64(), parent, headers)

	if expected.Cmp(header.Difficulty) != 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v, number %v, parent num %v", header.Difficulty, expected, header.Number, parent.Number)
	}

	if !ethash.CheckSupplies(chain, header, parent, headers) {
		return fmt.Errorf("invalid supplies in blockheader %s: number %v, parent num %v", header.Hash().String(), header.Number, parent.Number)
	}

	// Verify the pos weight
	//pos := ethash.GetPosProduction(chain, header)
	//pow := ethash.GetPowProduction(chain, header)
	//y := new(big.Int).Add(pos, pow)
	//if y.Cmp(big.NewInt(0)) == 0 {
	//	y = big.NewInt(-1)
	//}
	//w := new(big.Int).Div(pos, y)
	//fmt.Printf("===header No.%d, Nonce:%x\n", header.Number, header.Nonce)
	expectedPosWeight := ethash.PosWeight(chain, header, parent, headers)

	log.Debug("verifyHeader-call-PosWeight", "no:", header.Number.String(), "hash", header.Hash().String(), "PosWeight", header.PosWeight, "expectedPosWeight", expectedPosWeight)

	if int64(header.PosWeight) > posWeightPrecision {
		return fmt.Errorf("invalid pos weight: have %v, max  %v", header.PosWeight, posWeightPrecision)
	} else if expectedPosWeight != header.PosWeight {
		return fmt.Errorf("invalid pos weight: have %v, want %v", header.PosWeight, expectedPosWeight)
	}

	// Verify that the gas limit is <= 2^63-1
	cap := uint64(0x7fffffffffffffff)
	if header.GasLimit > cap {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, cap)
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}

	// Verify that the gas limit remains within allowed bounds
	diff := int64(parent.GasLimitPivot) - int64(header.GasLimitPivot)
	if diff < 0 {
		diff *= -1
	}
	//	limit := parent.GasLimitPivot / params.GasLimitBoundDivisor

	//	if uint64(diff) >= limit || header.GasLimitPivot < params.MinGasLimit {
	//		return fmt.Errorf("invalid gas limit: have %d, want %d += %d", header.GasLimitPivot, parent.GasLimitPivot, limit)
	//	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// Verify the engine specific seal securing the block
	if seal {
		if err := ethash.verifySeal(chain, header, parent, headers); err != nil {
			return err
		}
	}
	if err := misc.VerifyForkHashes(chain.Config(), header, uncle); err != nil {
		return err
	}
	return nil
}

func (ethash *Ethash) FindInHeadersByNum(blockNum uint64, buf []*types.Header) *types.Header {
	for _, v := range buf {
		if v.Number.Uint64() == blockNum {
			return v
		}
	}
	return nil
}

// CCalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
//func (ethash *Ethash) CalcDifficulty(chain consensus.BlockReader, time uint64, parent *types.Header) *big.Int {
//	return CalcDifficulty(chain.Config(), time, parent)
//}

func (ethash *Ethash) CalcDifficulty(chain consensus.BlockReader, time uint64, parent *types.Header, headers []*types.Header) *big.Int {
	//return new(big.Int).SetInt64(10000)

	//fmt.Println(ethash.powTargetTimespan, ethash.powTargetSpacing)
	//ethash.powTargetSpacing = 15

	var (
		difficultyAdjustInterval uint64 = uint64(core.BlocksInMatureCycle()) * ethash.difficultyAdjustCycles
		h                        *types.Header
	)
	//var difficultyAdjustInterval int64 = 100

	if parent.Number.Cmp(new(big.Int).SetInt64(0)) == 0 {
		return parent.Difficulty
	}

	if ((parent.Number.Uint64()) % difficultyAdjustInterval) != 0 {
		return parent.Difficulty
	}

	hash := parent.Hash()
	for i := 0; i < (int)(difficultyAdjustInterval); i++ {
		h = chain.GetHeaderByHash(hash)
		if h == nil {
			log.Error("FATAL ERROR! CalcDifficulty can not get header", "hash", hash.String())
			panic("Logical error.\n")
		}
		hash = h.ParentHash
	}
	/*
		start := (uint64)(parent.Number.Int64() + 1 - difficultyAdjustInterval)
		h := chain.GetHeaderByNumber(start)
		if h == nil {
			h = ethash.FindInHeadersByNum(start, headers)
			if h == nil {
				log.Error("FATAL ERROR", "CalcDifficulty can not get header", start)
				panic("Logical error.\n")
			}
		}*/

	var targetTimespan uint64 = difficultyAdjustInterval * ethash.powTargetSpacing
	var actualTimespan uint64 = (uint64)(parent.Time.Int64() - (h.Time.Int64()))

	if actualTimespan < targetTimespan/4 {
		actualTimespan = targetTimespan / 4
	}
	if actualTimespan > targetTimespan*4 {
		actualTimespan = targetTimespan * 4
	}
	var powLimit uint64 = ethash.minDifficulty
	var newDifficulty *big.Int = parent.Difficulty
	newDifficulty = new(big.Int).SetUint64(newDifficulty.Uint64() * new(big.Int).SetUint64(targetTimespan).Uint64())
	newDifficulty = new(big.Int).SetUint64(newDifficulty.Uint64() / actualTimespan)
	if newDifficulty.Uint64() < powLimit {
		newDifficulty = new(big.Int).SetUint64(powLimit)
	}
	log.Info("adjust difficulty", "actualtime", actualTimespan, "number", parent.Number.Uint64()+1, "newdifficulty", newDifficulty.Uint64(), "old-difficulty", parent.Difficulty.Uint64())

	return newDifficulty
}

// Some weird constants to avoid constant memory allocs for them.
var (
	expDiffPeriod = big.NewInt(100000)
	big1          = big.NewInt(1)
	big2          = big.NewInt(2)
	big9          = big.NewInt(9)
	big10         = big.NewInt(10)
	bigMinus99    = big.NewInt(-99)
	big2999999    = big.NewInt(2999999)
)

// VerifySeal implements consensus.Engine, checking whether the given block satisfies
// the PoW difficulty requirements.

func (ethash *Ethash) VerifySeal(chain consensus.BlockReader, header *types.Header, headers []*types.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return fmt.Errorf("the parent(%s) of (%s) doesn't exist!", header.ParentHash.String(), header.Hash().String())
	}
	return ethash.verifySeal(chain, header, parent, headers)
}

func (ethash *Ethash) verifySeal(chain consensus.BlockReader, header *types.Header, parent *types.Header, headers []*types.Header) error {
	// If we're running a fake PoW, accept any seal as valid
	if ethash.config.PowMode == ModeFake || ethash.config.PowMode == ModeFullFake {
		time.Sleep(ethash.fakeDelay)
		if ethash.fakeFail == header.Number.Uint64() {
			return errInvalidPoW
		}
		return nil
	}
	// If we're running a shared PoW, delegate verification to it
	if ethash.shared != nil {
		return ethash.shared.verifySeal(chain, header, parent, headers)
	}
	// Ensure that we have a valid difficulty for the block
	if header.Difficulty.Sign() <= 0 {
		return errInvalidDifficulty
	}

	w := big.NewInt(int64(ethash.PosWeight(chain, header, parent, headers)))

	if w.Cmp(big.NewInt(int64(header.PosWeight))) != 0 {
		return errInvalidPosWeight
	}

	code, error := rlp.EncodeToBytes(header)
	if error != nil {
		return errRlpEncodeErr
	}

	result := GHash(code)
	target := ethash.CalcTarget(chain, header, headers)
	if new(big.Int).SetBytes(result).Cmp(target) > 0 {
		return errInvalidPoW
	}

	return nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the ethash protocol. The changes are done inline.
func (ethash *Ethash) Prepare(chain consensus.BlockReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty = ethash.CalcDifficulty(chain, header.Time.Uint64(), parent, nil)

	// update the supplies
	ethash.UpdateSupplies(chain, header, parent, nil)

	header.PosWeight = ethash.PosWeight(chain, header, parent, nil)
	return nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards,
// setting the final state and assembling the block.
func (ethash *Ethash) Finalize(chain consensus.BlockReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Accumulate any block and uncle rewards and commit the final state root

	var err error = nil
	powProduction := ethash.calculatePowRewards(chain.Config(), state, header, uncles)
	if header.PowProduction == nil {
		header.PowProduction = ethash.accumulatePowRewards(chain.Config(), state, header, uncles)
	} else if powProduction != nil && powProduction.Cmp(header.PowProduction) == 0 {
		ethash.accumulatePowRewards(chain.Config(), state, header, uncles)
	} else {
		err = errors.New("pow production check error")
	}

	if state.GetAccountType(header.Coinbase) == common.DelegateMiner {
		posProduction := ethash.calculatePosRewards(chain, chain.Config(), state, header, uncles)
		if header.PosProduction == nil {
			header.PosProduction = ethash.accumulatePosRewards(chain, chain.Config(), state, header, uncles)
		} else if posProduction != nil && posProduction.Cmp(header.PosProduction) == 0 {
			ethash.accumulatePosRewards(chain, chain.Config(), state, header, uncles)
		} else {
			err = errors.New("pos production check error")
		}
	}
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, txs, uncles, receipts), err
}

// Some weird constants to avoid constant memory allocs for them.
var (
	big8  = big.NewInt(8)
	big32 = big.NewInt(32)
	big0  = big.NewInt(0)
)

func CurPowReward(baseReward *big.Int, blockNumber uint64) *big.Int {
	if blockNumber == 0 {
		return big0
	}
	halveInterval := core.FixedHalveInterval(halveIntervalGoal)
	var n uint = uint((blockNumber - 1) / halveInterval)
	curBlockPowReward := new(big.Int).Rsh(baseReward, n)
	return curBlockPowReward
}

// AccumulatePowRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func (ethash *Ethash) accumulatePowRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) *big.Int {
	// Select the correct block reward based on chain progression
	blockReward := FrontierBlockReward
	if config.IsByzantium(header.Number) {
		blockReward = ByzantiumBlockReward
	}
	curPowReward := CurPowReward(blockReward, header.Number.Uint64())
	log.Info("accumulatePowRewards", "no:", header.Number.String(), "reward", curPowReward.String(), "Coinbase", header.Coinbase.String())
	uncleCnt := new(big.Int).SetUint64(uint64(len(uncles)))
	total := new(big.Int)
	if uncleCnt.Sign() > 0 {
		powRewardUncles := new(big.Int).Mul(curPowReward, PowRewardRatioUncles)
		powRewardUncles.Div(powRewardUncles, PowRewardRatioPrecision)
		powRewardSelf := new(big.Int).Sub(curPowReward, powRewardUncles)
		powRewardPerUncle := powRewardUncles.Div(powRewardUncles, uncleCnt)
		if powRewardPerUncle.Sign() > 0 {
			for _, uncle := range uncles {
				state.AddBalance(uncle.Coinbase, powRewardPerUncle)
				total.Add(total, powRewardPerUncle)
			}
		}
		if powRewardSelf.Sign() > 0 {
			state.AddBalance(header.Coinbase, powRewardSelf)
			total.Add(total, powRewardSelf)
		}
	} else {
		state.AddBalance(header.Coinbase, curPowReward)
		total.Add(total, curPowReward)
	}
	return total
}

// CalculateRewards calculate all the POW mining reward of the block(include uncles' rewards).
// The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func (ethash *Ethash) calculatePowRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) *big.Int {
	// Select the correct block reward based on chain progression
	blockReward := FrontierBlockReward
	if config.IsByzantium(header.Number) {
		blockReward = ByzantiumBlockReward
	}

	curPowReward := CurPowReward(blockReward, header.Number.Uint64())
	uncleCnt := new(big.Int).SetUint64(uint64(len(uncles)))
	total := new(big.Int)
	if uncleCnt.Sign() > 0 {
		powRewardUncles := new(big.Int).Mul(curPowReward, PowRewardRatioUncles)
		powRewardUncles.Div(powRewardUncles, PowRewardRatioPrecision)
		powRewardSelf := new(big.Int).Sub(curPowReward, powRewardUncles)
		powRewardPerUncle := powRewardUncles.Div(powRewardUncles, uncleCnt)
		if powRewardPerUncle.Sign() > 0 {
			for _, uncle := range uncles {
				uncle.Coinbase.String()
				total.Add(total, powRewardPerUncle)
			}
		}
		if powRewardSelf.Sign() > 0 {
			total.Add(total, powRewardSelf)
		}
	} else {
		total.Add(total, curPowReward)
	}
	return total
}

// AccumulatePosRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func (ethash *Ethash) accumulatePosRewards(chain consensus.BlockReader, config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) *big.Int {
	matureState := core.GetMatureState(chain, header.Number.Uint64(), nil) //\\
	if matureState == nil || matureState.DelegateMinersCount() == 0 {
		return new(big.Int)
	}
	feeRatio, balanceSum, users := matureState.GetDelegateMiner(header.Coinbase)
	if balanceSum == nil && users == nil {
		return new(big.Int)
	}

	posSupply := ethash.GetPosMatureTotalSupply(chain, header, nil)
	remainingPosSupply := new(big.Int).Sub(PosSupplyLimit, posSupply)
	if remainingPosSupply.Sign() <= 0 {
		return new(big.Int)
	}

	total := new(big.Int)
	feeTotal := new(big.Int)
	for userAddr, depositData := range users {
		rewardBase := depositData.Balance
		if rewardBase.Cmp(remainingPosSupply) > 0 {
			rewardBase = remainingPosSupply
		}
		rewardStakeRaw := new(big.Int).Div(rewardBase, PosSupplyN)

		//delegateFee := rewardStakeRaw * (FeeRatio/FeeRatioPrecision)
		delegateFee := new(big.Int).Mul(rewardStakeRaw, new(big.Int).SetUint64(uint64(feeRatio)))
		delegateFee.Div(delegateFee, FeeRatioPrecision)

		//rewardStake = rewardStakeRaw - delegateFee
		rewardStake := new(big.Int).Sub(rewardStakeRaw, delegateFee)
		log.Info("accumulatePosRewards", "no", header.Number.String(), "rewardRaw", rewardStakeRaw.String(), "rewardStake", rewardStake.String(), "userAddr", userAddr.String(), "delegateFee", delegateFee.String(), "delegateAddr", header.Coinbase.String())
		feeTotal.Add(feeTotal, delegateFee)
		total.Add(total, rewardStakeRaw)
		state.AddBalance(userAddr, rewardStake)
	}

	state.AddBalance(header.Coinbase, feeTotal)
	return total
}

// CalculatePosRewards calculate all the POS reward of the block(the stake reward).
// The total reward consists of the stake rewards paid to the stake holders and
// the delegate fee paid to delegate miners.
func (ethash *Ethash) calculatePosRewards(chain consensus.BlockReader, config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) *big.Int {
	matureState := core.GetMatureState(chain, header.Number.Uint64(), nil)
	if matureState == nil || matureState.DelegateMinersCount() == 0 {
		return new(big.Int)
	}
	feeRatio, balanceSum, users := matureState.GetDelegateMiner(header.Coinbase)
	if balanceSum == nil && users == nil {
		return new(big.Int)
	}

	posSupply := ethash.GetPosMatureTotalSupply(chain, header, nil)
	remainingPosSupply := new(big.Int).Sub(PosSupplyLimit, posSupply)
	if remainingPosSupply.Sign() <= 0 {
		return new(big.Int)
	}
	total := new(big.Int)
	feeTotal := new(big.Int)
	for _, depositData := range users {
		rewardBase := depositData.Balance
		if rewardBase.Cmp(remainingPosSupply) > 0 {
			rewardBase = remainingPosSupply
		}
		rewardStakeRaw := new(big.Int).Div(rewardBase, PosSupplyN)

		//delegateFee := rewardStakeRaw * (FeeRatio/FeeRatioPrecision)
		delegateFee := new(big.Int).Mul(rewardStakeRaw, new(big.Int).SetUint64(uint64(feeRatio)))
		delegateFee.Div(delegateFee, FeeRatioPrecision)

		feeTotal.Add(feeTotal, delegateFee)
		total.Add(total, rewardStakeRaw)
	}
	return total
}

func IsInPreEpoch(hash common.Hash, ancestors []*types.Block) bool {
	for _, act := range ancestors {
		if hash == act.Hash() {
			return true
		}
		uncles := act.Uncles()
		for _, uncle := range uncles {
			if uncle.Hash() == hash {
				return true
			}
		}
	}
	return false
}

func IsTopoPrepared(block *types.Block, ancestors []*types.Block, refBlocks []*types.Block) bool {

	// parent
	if !IsInPreEpoch(block.ParentHash(), ancestors) &&
		!IsInBlocks(block.ParentHash(), refBlocks) {
		return false
	}

	// references
	uncles := block.Uncles()
	for _, uncle := range uncles {
		if !IsInPreEpoch(uncle.Hash(), ancestors) &&
			!IsInBlocks(uncle.Hash(), refBlocks) {
			return false
		}
	}
	return true
}

func IsInBlocks(hash common.Hash, blocks []*types.Block) bool {
	for i := 0; i < len(blocks); i++ {
		if hash == blocks[i].Hash() {
			return true
		}
	}
	return false
}
