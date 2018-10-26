package core

import (
	"math/big"
	"sync"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/log"
)

type dmAttr struct {
	feeRatio uint32
	depositBalanceSum *big.Int
	users map[common.Address]common.DepositData
}

type MatureState struct {
	state *state.StateDB

	// caches
	miners map[common.Address]*dmAttr
	depositBalanceSum *big.Int	// Sum of all miner deposit
	dmCount uint32
}

type matureStateSet struct {
	current *MatureState
	prev *MatureState
	minBlock uint64
	cs sync.RWMutex
}

const (
	blocksInMatureCycle uint64 = 256
	blocksInMatureCycleMask = blocksInMatureCycle - 1
	minMatureBlockNumber = blocksInMatureCycle * 2
)


var cachedStates = matureStateSet{
	current: nil,
	prev: nil,
	minBlock: minMatureBlockNumber,
}

func MinMatureBlockNumber() uint64 { return minMatureBlockNumber }

func LastMatureCycleRange(cur uint64) (uint64, uint64) {
	if cur >= minMatureBlockNumber {
		end := (cur & ^blocksInMatureCycleMask) - blocksInMatureCycle
		return end - blocksInMatureCycle, end
	}
	return 0, 0
}

func GetMatureState(chain consensus.ChainReader,  blockNum uint64) *MatureState {
	if (blockNum & blocksInMatureCycleMask) != 0 {
		cachedStates.cs.RLock()
		defer cachedStates.cs.RUnlock()
		if blockNum >= cachedStates.minBlock {
			return cachedStates.current
		}
		return cachedStates.prev
	}

	if blockNum >= minMatureBlockNumber {
		// get the state
		header := chain.GetHeaderByNumber(blockNum - blocksInMatureCycle - 1)
		stateDB, err := chain.GetState(header.Root)
		if err != nil {
			log.Error(fmt.Sprintf(
				"FATAL ERROR: can not get state DB from matured block: %d. reason: %v\n",
				blockNum - blocksInMatureCycle - 1, err))
			panic("Logical error.\n")
		}
		dmViews := stateDB.GetAllDelegateMiners()
		count := len(dmViews)
		sum := new(big.Int)
		for _, view := range dmViews {
			sum.Add(sum, view.DepositBalance)
		}

		// update the cache
		cachedStates.cs.Lock()
		defer cachedStates.cs.Unlock()
		cachedStates.prev = cachedStates.current
		cachedStates.minBlock += blocksInMatureCycle
		cachedStates.current = &MatureState{
			state:             stateDB,
			miners:            make(map[common.Address]*dmAttr),
			depositBalanceSum: sum,
			dmCount:           uint32(count),
		}
	}

	return cachedStates.current
}

func (self *MatureState) GetDelegateMiner(addr common.Address) (uint32, *big.Int, map[common.Address]common.DepositData) {
	if miner, ok := self.miners[addr]; ok {
		return miner.feeRatio, miner.depositBalanceSum, miner.users
	}

	if uint32(len(self.miners)) < self.dmCount && self.state.GetAccountType(addr) == common.DelegateMiner {
		dv, dvErr := self.state.GetDelegateMiner(addr)
		dd, ddErr := self.state.GetDepositUsers(addr)
		if dvErr != nil && ddErr != nil {
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
