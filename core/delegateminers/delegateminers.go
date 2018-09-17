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

func getLastCycleDepositAmount()(*big.Int)  {
	return new(big.Int)
}