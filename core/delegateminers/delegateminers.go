package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
)

const DsPowCycle uint64 = 200

type Depositor struct {
	Addr   common.Address
	Amount *big.Int
}

type DelegateMiner struct {
	Depositors []Depositor
	Fee        uint32
}

var stateBuf *state.StateDB

var delegateMinerMap = make(map[common.Address]DelegateMiner, 10)

func GetDepositBalanceSum(chain consensus.ChainReader, blockNum *big.Int) *big.Int {
	GetMatureState(chain, blockNum)
	var miners = stateBuf.GetAllDelegateMiners()
	var amount uint64
	for _, v := range miners {
		amount += v.DepositBalance.Uint64()
	}
	return new(big.Int).SetUint64(amount)
}

func GetDelegateMinersCount(chain consensus.ChainReader, blockNum *big.Int) uint64 {
	GetMatureState(chain, blockNum)
	var miners = stateBuf.GetAllDelegateMiners()
	var minersAmount = len(miners)
	return (uint64)(minersAmount)
}

func GetDelegateMiner(chain consensus.ChainReader, header *types.Header, address common.Address) *DelegateMiner {
	var delegateMiner = DelegateMiner{}
	var state = GetMatureState(chain, header.Number)
	cycle := header.Number.Uint64() / DsPowCycle
	cyclemod := header.Number.Uint64() % DsPowCycle
	if state == nil {
		return nil
	}
	if cycle >= 2 {
		if state.GetAccountType(address) != common.DelegateMiner {
			return nil
		}
		if cyclemod == 0 {
			delegateMinerMap = make(map[common.Address]DelegateMiner, 10)
		}
		if _, ok := delegateMinerMap[address]; ok {
			delegateMiner = delegateMinerMap[address]
			return &delegateMiner
		}
		var depositorMap = state.GetDepositUsers(address)
		delegateMiner.Fee = state.GetDelegateMiner(address).FeeRatio
		for k, v := range depositorMap {
			depositor := Depositor{Addr: k, Amount: v.Balance}
			delegateMiner.Depositors = append(delegateMiner.Depositors, depositor)
		}
		delegateMinerMap[address] = delegateMiner
	}
	return &delegateMiner
}

func GetMatureState(chain consensus.ChainReader, blockNum *big.Int) *state.StateDB {
	cycle := blockNum.Uint64() / DsPowCycle
	cyclemod := blockNum.Uint64() % DsPowCycle
	if cycle < 2 {
		return nil
	}
	if cyclemod == 0 {
		number := (cycle - 1) * DsPowCycle
		headAvai := chain.GetHeaderByNumber(number)
		stateBuf, _ = chain.GetState(chain.GetBlock(headAvai.ParentHash, headAvai.Number.Uint64()-1).Root())
	}
	return stateBuf
}
