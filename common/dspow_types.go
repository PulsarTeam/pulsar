package common

import (
	"math/big"
	"errors"
)

const (
	DefaultAccount = iota
	DelegateMiner
)

var (
	ErrAccountTypeNotAllowed = errors.New("account type not allowed")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrRedeposit = errors.New("redeposit to same miner")
	ErrNoDepositBalance = errors.New("no deposit balance")
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

func MaxFeeRatio() uint32 {
	return 1000000 - 1
}

type StakeData interface {
	Empty() bool
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

// Deposit View from default account
type DepositView struct {
	DepositData
	FeeRatio uint32 `json:"delegateMinerFeeRatio" gencodec:"required"`
}

func (self *DepositView) Equal(dd *DepositData) bool {
	return (self.Balance == dd.Balance && self.BlockNumber == dd.BlockNumber) ||
		(self.Balance != nil && dd.Balance != nil && self.Balance.Cmp(dd.Balance) == 0 &&
			self.BlockNumber != nil && dd.BlockNumber != nil && self.BlockNumber.Cmp(dd.BlockNumber) == 0)
}