package common

import "math/big"

const (
	DefaultAccount = iota
	DelegateMiner
)

type AccountType uint8

func (aType AccountType) String() string {
	switch (aType) {
	case DefaultAccount: return "DefaultAccount"
	case DelegateMiner: return "DelegateMiner"
	default: panic("wrong type")
	}
}

func AccountTypeValidity(aType AccountType) bool {
	return aType == DefaultAccount || aType == DelegateMiner
}

func FeeRatioValidity(feeRatio uint32) bool {
	return feeRatio < 1000000
}

type DepositData struct {
	Balance *big.Int `json:"depositBalance" gencodec:"required"`
	BlockNumber *big.Int `json:"depositBlockNumber" gencodec:"required"`
}

func (this DepositData) Empty() bool {
	return this.Balance == nil || this.Balance.Sign() == 0
}

// View of Delegate Miner
type DMView struct {
	FeeRatio uint32
	DepositBalance *big.Int
}