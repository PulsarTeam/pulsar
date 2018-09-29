package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/availabledb"
	"errors"
)

type Depositor struct {
	Addr common.Address
	Amount    *big.Int
}

type DelegateMiner struct {
	Depositors []Depositor
	Fee uint32
}

func GetDelegateMiner(ethash *availabledb.AvailableDb, chain consensus.ChainReader, header *types.Header, address common.Address)(*state.StateDB,*DelegateMiner, error)  {
	var err error = nil

	var state, stateErr = ethash.GetAvailableDb(chain,header)
    if state == nil {
        return nil, nil, stateErr
	}

	var depositorMap = state.GetDepositMap(address)
	var miners = state.GetAllDelegateMiners()
	miner ,ok := miners[address]
	if (!ok){
		err = errors.New(`no miner!`)
	}
	delegateMiner := &DelegateMiner{Fee: miner.FeeRatio}
	for k, v := range depositorMap{
		depositor := Depositor{Addr:k, Amount:v.Balance}
		delegateMiner.Depositors = append(delegateMiner.Depositors, depositor)
	}
	return state, delegateMiner,err
}

func GetLastCycleDepositAmount(state *state.StateDB)(*big.Int,error)  {
	var err error = nil
	var miners = state.GetAllDelegateMiners()
	var amount uint64
	for _, v := range miners{
		amount += v.DepositBalance.Uint64()
	}
	return new(big.Int).SetUint64(amount),err
}

func GetLastCycleDelegateMiners(state *state.StateDB)(uint64,error)  {
	var miners = state.GetAllDelegateMiners()
	var minersAmount = len(miners)
	return (uint64)(minersAmount),nil
}
