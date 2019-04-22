package core

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"math/big"
	"sync"
)

type dmAttr struct {
	feeRatio          uint32
	depositBalanceSum *big.Int
	users             map[common.Address]common.DepositData
}

type MatureState struct {
	state *state.StateDB

	// caches
	miners            map[common.Address]*dmAttr
	depositBalanceSum *big.Int // Sum of all miner deposit
	dmCount           uint32
}

type matureStateSet struct {
	current  *MatureState
	prev     *MatureState
	minBlock uint64
	cs       sync.Mutex
}

const (
	blocksInMatureCycle     uint64 = 8192 // the mature cycle, for 15 seconds per block it's 1280 minutes, i.e., about 21.333 hours
	blocksInMatureCycleMask        = ^(blocksInMatureCycle - 1)
	minMatureBlockNumber           = blocksInMatureCycle * 2
)

func MinMatureBlockNumber() uint64 { return minMatureBlockNumber }

func BlocksInMatureCycle() uint64 { return uint64(blocksInMatureCycle) }

// return the last cycle range [start, end)
func LastCycleRange(cur uint64) (start uint64, end uint64) {
	if cur > blocksInMatureCycle {
		end = (cur-1)&blocksInMatureCycleMask + 1
		return end - blocksInMatureCycle, end
	}
	return 0, 1
}

func LastMatureCycleRange(cur uint64) (uint64, uint64) {
	if cur > minMatureBlockNumber {
		return LastCycleRange(cur - blocksInMatureCycle)
	}
	return 0, 1
}

func FixedHalveInterval(rawHalveInterval uint64) uint64 {
	return rawHalveInterval & blocksInMatureCycleMask
}

var cachedStates = matureStateSet{
	current:  nil,
	prev:     nil,
	minBlock: 0,
}

func GetMatureState(chain consensus.BlockReader, blockNum uint64, backup []*types.Header) *MatureState {
	cachedStates.cs.Lock()
	defer cachedStates.cs.Unlock()
	if cachedStates.current != nil {
		if blockNum >= cachedStates.minBlock && blockNum < cachedStates.minBlock+blocksInMatureCycle {
			// Current cycle
			return cachedStates.current
		}
		if blockNum >= cachedStates.minBlock-blocksInMatureCycle && blockNum < cachedStates.minBlock {
			// Previous cycle
			if cachedStates.prev != nil || blockNum < minMatureBlockNumber {
				return cachedStates.prev
			}
			// update previous cycle.
			cachedStates.prev = newMatureState(chain, backup, cachedStates.minBlock-minMatureBlockNumber-1)
			return cachedStates.prev
		}

		nextCycleBlock := cachedStates.minBlock + blocksInMatureCycle
		if blockNum >= nextCycleBlock && blockNum < nextCycleBlock+blocksInMatureCycle {
			// Next cycle
			cachedStates.prev = cachedStates.current
			cachedStates.current = newMatureState(chain, backup, cachedStates.minBlock-1)
			cachedStates.minBlock = nextCycleBlock
			return cachedStates.current
		}
	}

	var mState *MatureState
	if blockNum >= minMatureBlockNumber {
		startBlock := blockNum & blocksInMatureCycleMask
		mState = newMatureState(chain, backup, startBlock-blocksInMatureCycle-1)
		if cachedStates.current == nil {
			// First calling
			cachedStates.current = mState
			cachedStates.minBlock = startBlock
		} else {
			// Ad-hoc calling doesn't affect cache
			log.Warn("Ad-hoc calling mature state", "block: ", blockNum, "current: ", cachedStates.minBlock)
		}
	}
	return mState
}

func (self *MatureState) GetDelegateMiner(addr common.Address) (uint32, *big.Int, map[common.Address]common.DepositData) {
	if miner, ok := self.miners[addr]; ok {
		return miner.feeRatio, miner.depositBalanceSum, miner.users
	}

	if uint32(len(self.miners)) < self.dmCount && self.state.GetAccountType(addr) == common.DelegateMiner {
		dv, dvErr := self.state.GetDelegateMiner(addr)
		dd, ddErr := self.state.GetDepositUsers(addr)
		if dvErr == nil && ddErr == nil {
			dmObj := &dmAttr{
				feeRatio:          dv.FeeRatio,
				depositBalanceSum: new(big.Int).Set(dv.DepositBalance),
				users:             dd,
			}
			self.miners[addr] = dmObj
			return dv.FeeRatio, dmObj.depositBalanceSum, dd
		}
	}

	return 0, nil, nil // not found
}

func (self *MatureState) DelegateMinersCount() uint32 {
	return self.dmCount
}

func (self *MatureState) DepositBalanceSum() *big.Int {
	return new(big.Int).Set(self.depositBalanceSum)
}

func getHeaderFromBuffer(buf []*types.Header, blockNum uint64) *types.Header {
	for _, v := range buf {
		if v.Number.Uint64() == blockNum {
			return v
		}
	}
	return nil
}

func newMatureState(chain consensus.BlockReader, backup []*types.Header, blockNum uint64) *MatureState {
	// get the state
	header := chain.GetHeaderByNumber(blockNum)
	if header == nil {
		if header = getHeaderFromBuffer(backup, blockNum); header == nil {
			//\\fmt.Printf("get header From buffer ====== blockNum: %v \n", blockNum)
			log.Error("FATAL ERROR", "can not get header", blockNum)
			panic("Logical error.\n")
		}
	}
	stateDB, err := chain.GetState(header.Root)
	if err != nil {
		log.Error("FATAL ERROR", "can not get state DB from matured block", blockNum, "error", err)
		panic("Logical error.\n")
	}
	dmViews := stateDB.GetAllDelegateMiners()
	count := len(dmViews)
	sum := new(big.Int)
	for _, view := range dmViews {
		sum.Add(sum, view.DepositBalance)
	}

	return &MatureState{
		state:             stateDB,
		miners:            make(map[common.Address]*dmAttr),
		depositBalanceSum: sum,
		dmCount:           uint32(count),
	}
}
