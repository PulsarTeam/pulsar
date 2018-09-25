package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"github.com/ethereum/go-ethereum/core/state"
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

func GetDepositors(state *state.StateDB, address common.Address)(DelegateMiner, error)  {
	var err error = nil
	var depositorMap = state.GetDepositMap(address)
	var miners = state.GetAllDelegateMiners()
	miner ,ok := miners[address]
	if (!ok){
		err = errors.New(`no miner!`)
	}
	delegateMiner := DelegateMiner{Fee: miner.FeeRatio}
	for k, v := range depositorMap{
		depositor := Depositor{Addr:k, Amount:v.Balance}
		delegateMiner.Depositors = append(delegateMiner.Depositors, depositor)
	}
	return delegateMiner,err
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
