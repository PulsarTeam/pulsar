// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package vm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// StateDB is an EVM database for full state querying.
type StateDB interface {
	CreateAccount(common.Address)

	SubBalance(common.Address, *big.Int)
	AddBalance(common.Address, *big.Int)
	GetBalance(common.Address) *big.Int

	/**
	 * @brief Default account deposit balance to a delegate miner
	 * @param from: the deposit balance account
	 * @param to: the delegate miner account who accept the deposit
	 * @param balance: the deposit amount.
	 * @param blockNumber: the future mature block.
	 */
	Deposit(from common.Address, to common.Address, balance *big.Int, blockNumber *big.Int) error

	/**
	 * @brief Default account withdraw balance from the delegate miner
	 * @param from: the withdraw issue account (default account)
	 * @param to: the delegate miner account who store the deposit
	 */
	Withdraw(from common.Address, to common.Address) error

	/**
	 * @brief Set Account type
	 * @param account: the account to be set
	 * @param aType: state.DefaultAccount, state.DelegateMiner, defined in dspow_defs.go
	 * @param feeRatio: make sense only if the type is DelegateMiner. feeRatio/1,000,000
	 */
	SetAccountType(account common.Address, aType common.AccountType, feeRatio uint32) error

	GetAccountType(common.Address) common.AccountType

	/**
	 * @brief Get all registered delegate miners
	 * @return The map of all delegate miners address and view.
	 */
	GetAllDelegateMiners() map[common.Address]common.DMView

	GetDelegateMiner(common.Address) (common.DMView, error)

	/**
	 * @brief Get the delegate miners that the (default) account deposited shares.
	 * @param account: the account who deposit shares.
	 * @return The map of the delegate miners address and view.
	 */
	GetDepositMiners(account common.Address) (map[common.Address]common.DepositView, error)

	/**
	 * @brief Get the users of which account deposited shares to the delegate miner.
	 * @param: the delegate miner address.
	 * @return The map of the user (default account) address and its deposit data.
	 */
	GetDepositUsers(common.Address) (map[common.Address]common.DepositData, error)

	GetNonce(common.Address) uint64
	SetNonce(common.Address, uint64)

	GetCodeHash(common.Address) common.Hash
	GetCode(common.Address) []byte
	SetCode(common.Address, []byte)
	GetCodeSize(common.Address) int

	AddRefund(uint64)
	GetRefund() uint64

	GetState(common.Address, common.Hash) common.Hash
	SetState(common.Address, common.Hash, common.Hash)

	Suicide(common.Address) bool
	HasSuicided(common.Address) bool

	// Exist reports whether the given account exists in state.
	// Notably this should also return true for suicided accounts.
	Exist(common.Address) bool
	// Empty returns whether the given account is empty. Empty
	// is defined according to EIP161 (balance = nonce = code = 0).
	Empty(common.Address) bool

	RevertToSnapshot(int)
	Snapshot() int

	AddLog(*types.Log)
	AddPreimage(common.Hash, []byte)

	ForEachStorage(common.Address, func(common.Hash, common.Hash) bool)
}

// CallContext provides a basic interface for the EVM calling conventions. The EVM EVM
// depends on this context being implemented for doing subcalls and initialising new EVM contracts.
type CallContext interface {
	// Call another contract
	Call(env *EVM, me ContractRef, addr common.Address, data []byte, gas, value *big.Int) ([]byte, error)
	// Take another's contract code and execute within our own context
	CallCode(env *EVM, me ContractRef, addr common.Address, data []byte, gas, value *big.Int) ([]byte, error)
	// Same as CallCode except sender and value is propagated from parent to child scope
	DelegateCall(env *EVM, me ContractRef, addr common.Address, data []byte, gas *big.Int) ([]byte, error)
	// Create a new contract
	Create(env *EVM, me ContractRef, data []byte, gas, value *big.Int) ([]byte, common.Address, error)
}
