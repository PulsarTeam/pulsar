package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
)

type Depositor struct {
	Addr common.Address
	Amount    *big.Int
}


func GetDepositors(address common.Address)([]Depositor, error)  {
	depositors := []Depositor{}
	return depositors,nil
}

func GetLastCycleDepositAmount()(*big.Int,error)  {
	return new(big.Int),nil
}

func GetLastCycleDelegateMiners()(uint64,error)  {
	return 0,nil
}
