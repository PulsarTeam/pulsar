package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"github.com/ethereum/go-ethereum/core/state"
	"errors"
)

const  CYCLE  = 2 * 24 * 60 * 60 / 15

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



	return new(big.Int),nil
}

func GetLastCycleDelegateMiners(state *state.StateDB)(uint64,error)  {
	return 0,nil
}
