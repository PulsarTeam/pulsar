package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
)

type Depositor struct {
	addr common.Address
	amount    big.Int
}


func getDepositors(address common.Address)([]Depositor, error)  {
	depositors := []Depositor{}
	return depositors,nil
}

func getLastCycleDepositAmount()(*big.Int,error)  {
	return new(big.Int),nil
}

func getLastCycleDelegateMiners()(uint64,error)  {
	return 0,nil
}
