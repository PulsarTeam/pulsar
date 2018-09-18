package delegateminers

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
)

type Depositor struct {
	Addr common.Address
	Amount    big.Int
}

type DelegateMiner struct {
	Depositors []Depositor
	Fee uint64
}

func GetDepositors(address common.Address)(DelegateMiner, error)  {
	delegateMiner := DelegateMiner{}
	return delegateMiner,nil
}

func GetLastCycleDepositAmount()(*big.Int,error)  {
	return new(big.Int),nil
}

func GetLastCycleDelegateMiners()(uint64,error)  {
	return 0,nil
}
