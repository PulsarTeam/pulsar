package delegateminers

import (
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/availabledb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
)

type Depositor struct {
	Addr   common.Address
	Amount *big.Int
}

type DelegateMiner struct {
	Depositors []Depositor
	Fee        uint32
}

var delegateMinerMap = make(map[common.Address]DelegateMiner, 10)

func GetDelegateMiner(ethash *availabledb.AvailableDb, chain consensus.ChainReader, header *types.Header, address common.Address) (*state.StateDB, *DelegateMiner, error) {
	var err error = nil
	var delegateMiner = DelegateMiner{}
	var state, stateErr = ethash.GetAvailableDb(chain, header)
	cylce := header.Number.Uint64() / ethash.DsPowCycle
	cylcemod := header.Number.Uint64() % ethash.DsPowCycle
	if state == nil {
		return nil, nil, stateErr
	}
	if cylce >= 2 {
		if state.GetAccountType(address) != common.DelegateMiner {
			err = errors.New(`no miner!`)
			return state, nil, err
		}
		if cylcemod == 0 {
			delegateMinerMap = make(map[common.Address]DelegateMiner, 10)
		}
		if _, ok := delegateMinerMap[address]; ok {
			delegateMiner = delegateMinerMap[address]
			return state, &delegateMiner, err
		}
		var depositorMap = state.GetDepositUsers(address)
		delegateMiner.Fee = state.GetDelegateMiner(address).FeeRatio
		for k, v := range depositorMap {
			depositor := Depositor{Addr: k, Amount: v.Balance}
			delegateMiner.Depositors = append(delegateMiner.Depositors, depositor)
		}
		delegateMinerMap[address] = delegateMiner
	}
	return state, &delegateMiner, err
}

func GetLastCycleDepositAmount(state *state.StateDB) (*big.Int, error) {
	var err error = nil
	var miners = state.GetAllDelegateMiners()
	var amount uint64
	for _, v := range miners {
		amount += v.DepositBalance.Uint64()
	}
	return new(big.Int).SetUint64(amount), err
}

func GetLastCycleDelegateMiners(state *state.StateDB) (uint64, error) {
	var miners = state.GetAllDelegateMiners()
	var minersAmount = len(miners)
	return (uint64)(minersAmount), nil
}
