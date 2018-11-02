// Copyright 2014 The go-ethereum Authors
// Copyright 2014 The go-ethereum Authors
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

package state

import (
	"bytes"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/log"
)

type Code []byte

func (self Code) String() string {
	return string(self) //strings.Join(Disassemble(self), " ")
}

type Storage map[common.Hash]common.Hash

func (self Storage) String() (str string) {
	for key, value := range self {
		str += fmt.Sprintf("%X : %X\n", key, value)
	}

	return
}

func (self Storage) Copy() Storage {
	cpy := make(Storage)
	for key, value := range self {
		cpy[key] = value
	}

	return cpy
}

type StakeCache map[common.Address]common.StakeData

func (this StakeCache) Copy() StakeCache {
	cpy := make(StakeCache)
	for key, value := range this {
		cpy[key] = value
	}
	return cpy
}

func checkDepositValidity(
	miner *stateObject, user* stateObject, minerData *common.DepositData, userData *common.DepositView) {
	if !userData.Equal(minerData) {
		userDataBlkStr := "nil"
		userDataBlnStr := "nil"
		minerDataBlkStr := "nil"
		minerDataBlnStr := "nil"

		if userData.BlockNumber != nil {
			userDataBlkStr = userData.BlockNumber.String()
		}
		if userData.Balance != nil {
			userDataBlnStr = userData.Balance.String()
		}
		if minerData.BlockNumber != nil {
			minerDataBlkStr = minerData.BlockNumber.String()
		}
		if minerData.Balance != nil {
			minerDataBlnStr = minerData.Balance.String()
		}

		panic(fmt.Sprintf("Logical error!User account: %s,%s,%d\nDelegate miner: %s,%s,%d\n",
			userDataBlkStr, userDataBlnStr, userData.FeeRatio,
			minerDataBlkStr, minerDataBlnStr, miner.data.FeeRatio))
	}

	if minerData.Balance != nil && miner.data.DepositBalance.Cmp(minerData.Balance) < 0 { // whole < part
		panic(fmt.Sprintf("Logical error! miner total deposit balance: %s, deposited balance: %s\n",
			miner.data.DepositBalance.String(), minerData.Balance.String()))
	}

	if userData.Balance != nil && user.data.DepositBalance.Cmp(userData.Balance) < 0 { // whole < part
		panic(fmt.Sprintf("Logical error! user total deposit balance: %s, deposited balance: %s\n",
			user.data.DepositBalance.String(), userData.Balance.String()))
	}
}


// stateObject represents an Ethereum account which is being modified.
//
// The usage pattern is as follows:
// First you need to obtain a state object.
// Account values can be accessed and modified through the object.
// Finally, call CommitTrie to write the modified storage trie into a database.
type stateObject struct {
	address  common.Address
	addrHash common.Hash // hash of ethereum address of the account
	data     Account
	db       *StateDB

	// DB error.
	// State objects are used by the consensus core and VM which are
	// unable to deal with database-level errors. Any error that occurs
	// during a database read is memoized here and will eventually be returned
	// by StateDB.Commit.
	dbErr error

	// Write caches.
	storageTrie Trie // storage trie, which becomes non-nil on first access
	code Code // contract bytecode, which gets set when code is loaded
	cachedStorage Storage // Storage entry cache to avoid duplicate reads
	dirtyStorage  Storage // Storage entries that need to be flushed to disk

	stakeTrie Trie  // stake trie for delegate miner
	dirtyStake StakeCache // stake state need to be flushed to disk

	// Cache flags.
	// When an object is marked suicided it will be delete from the trie
	// during the "update" phase of the state transition.
	dirtyCode bool // true if the code was updated
	suicided  bool
	deleted   bool
}

// Account is the Ethereum consensus representation of accounts.
// These objects are stored in the main account trie.
type Account struct {
	Nonce    uint64
	Balance  *big.Int
	StorageRoot common.Hash // merkle root of the storage trie
	CodeHash []byte
	Type common.AccountType
	DepositBalance *big.Int
	FeeRatio uint32
	StakeRoot common.Hash
}

func (a *Account) empty () bool {
	return a.Nonce == 0 &&
		   a.Balance.Sign() == 0 &&
		   bytes.Equal(a.CodeHash, crypto.EmptyKeccak256) &&
		   (a.Type == common.DefaultAccount || a.StakeRoot == (common.Hash{}) || a.StakeRoot == trie.EmptyRoot)
}

func createAccount(aType common.AccountType) *Account {
	if !common.AccountTypeValidity(aType) {
		panic(fmt.Sprintf("Account type is invalid: %d", aType))
	}

	pAccount := &Account{
		Nonce: 0,
		Type:  aType,
	}
	pAccount.Balance = new(big.Int)
	pAccount.CodeHash = crypto.EmptyKeccak256
	pAccount.DepositBalance = new(big.Int)

	return pAccount
}

// newObject creates a state object.
func newObject(db *StateDB, address common.Address, account *Account) *stateObject {
	return &stateObject{
		db:            db,
		address:       address,
		addrHash:      crypto.Keccak256Hash(address[:]),
		data:          *account,
		cachedStorage: make(Storage),
		dirtyStorage:  make(Storage),
		dirtyStake:    make(StakeCache),
	}
}

// empty returns whether the account is considered empty.
func (s *stateObject) empty() bool {
	return s.data.empty()
}

// EncodeRLP implements rlp.Encoder.
func (c *stateObject) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, c.data)
}

// setError remembers the first non-nil error it is called with.
func (self *stateObject) setError(err error) {
	if self.dbErr == nil {
		self.dbErr = err
	}
}

func (self *stateObject) markSuicided() {
	self.suicided = true
}

func (c *stateObject) touch() {
	c.db.journal.append(touchChange{
		account: &c.address,
	})
	if c.address == ripemd {
		// Explicitly put it in the dirty-cache, which is otherwise generated from
		// flattened journals.
		c.db.journal.dirty(c.address)
	}
}

func (c *stateObject) getStorageTrie(db Database) Trie {
	if c.storageTrie == nil {
		c.storageTrie = c.openTrie(db, c.data.StorageRoot)
	}
	return c.storageTrie
}

func (c *stateObject) getStakeTrie(db Database) Trie {
	if c.stakeTrie == nil {
		c.stakeTrie = c.openTrie(db, c.data.StakeRoot)
	}
	return c.stakeTrie
}

func (c* stateObject) openTrie(db Database, root common.Hash) (result Trie) {
	var err error
	result, err = db.OpenAccountTrie(c.addrHash, root)
	if err != nil {
		result, _ = db.OpenAccountTrie(c.addrHash, common.Hash{})
		c.setError(fmt.Errorf("can't create trie: %v", err))
	}
	return result
}

// GetState returns a value in account storage.
func (self *stateObject) GetState(db Database, key common.Hash) common.Hash {
	value, exists := self.cachedStorage[key]
	if exists {
		return value
	}
	// Load from DB in case it is missing.
	enc, err := self.getStorageTrie(db).TryGet(key[:])
	if err != nil {
		self.setError(err)
		return common.Hash{}
	}
	if len(enc) > 0 {
		_, content, _, err := rlp.Split(enc)
		if err != nil {
			self.setError(err)
		}
		value.SetBytes(content)
	}
	self.cachedStorage[key] = value
	return value
}

// SetState updates a value in account storage.
func (self *stateObject) SetState(db Database, key, value common.Hash) {
	self.db.journal.append(storageChange{
		account:  &self.address,
		key:      key,
		prevalue: self.GetState(db, key),
	})
	self.setState(key, value)
}

func (self *stateObject) setState(key, value common.Hash) {
	self.cachedStorage[key] = value
	self.dirtyStorage[key] = value
}

// updateStorageTrie writes cached storage modifications into the object's storage trie.
func (self *stateObject) updateStorageTrie(db Database) Trie {
	tr := self.getStorageTrie(db)
	for key, value := range self.dirtyStorage {
		delete(self.dirtyStorage, key)
		if (value == common.Hash{}) {
			self.setError(tr.TryDelete(key[:]))
			continue
		}
		// Encoding []byte cannot fail, ok to ignore the error.
		v, _ := rlp.EncodeToBytes(bytes.TrimLeft(value[:], "\x00"))
		self.setError(tr.TryUpdate(key[:], v))
	}
	return tr
}

// updateStakeTrie writes cached storage modifications into the object's storage trie.
func (self *stateObject) updateStakeTrie(db Database) Trie {
	tr := self.getStakeTrie(db)
	if len(self.dirtyStake) > 0 {
		for key, value := range self.dirtyStake {
			if value.Empty() {
				self.setError(tr.TryDelete(key[:]))
				continue
			}
			// Encoding []byte cannot fail, ok to ignore the error.
			v, _ := rlp.EncodeToBytes(&value)
			self.setError(tr.TryUpdate(key[:], v))
		}
		self.dirtyStake = make(StakeCache) // clear map
	}
	return tr
}

// updateStorageRoot sets the trie root to the current root hash of
func (self *stateObject) updateStorageRoot(db Database) {
	self.updateStorageTrie(db)
	self.data.StorageRoot = self.storageTrie.Hash()
}

// UpdateStakeRoot sets the stake trie root to the current root hash of
func (self *stateObject) updateStakeRoot(db Database) {
	self.updateStakeTrie(db)
	self.data.StakeRoot = self.stakeTrie.Hash()
}

// commitStorageTrie commit the storage trie of the object to db.
// This updates the trie root.
func (self *stateObject) commitStorageTrie(db Database) error {
	self.updateStorageTrie(db)
	if self.dbErr != nil {
		return self.dbErr
	}
	root, err := self.storageTrie.Commit(nil)
	if err == nil {
		self.data.StorageRoot = root
	}
	return err
}

// commitStakeTrie commit the stake trie of the object to db.
// This updates the trie root.
func (self *stateObject) commitStakeTrie(db Database) error {
	self.updateStakeTrie(db)
	if self.dbErr != nil {
		return self.dbErr
	}
	root, err := self.stakeTrie.Commit(nil)
	if err == nil {
		self.data.StakeRoot = root
	}
	return err
}

// CommitTrie the storage trie of the object to db.
// This updates the trie root.
func (self *stateObject) CommitTrie(db Database) error {
	err := self.commitStorageTrie(db)
	err1 := self.commitStakeTrie(db)
	if err == nil {
		err = err1
	}
	return err
}

// AddBalance removes amount from c's balance.
// It is used to add funds to the destination account of a transfer.
func (c *stateObject) AddBalance(amount *big.Int) {
	// EIP158: We must check emptiness for the objects such that the account
	// clearing (0,0,0 objects) can take effect.
	if amount.Sign() == 0 {
		if c.empty() {
			c.touch()
		}

		return
	}
	c.SetBalance(new(big.Int).Add(c.Balance(), amount))
}

// SubBalance removes amount from c's balance.
// It is used to remove funds from the origin account of a transfer.
func (c *stateObject) SubBalance(amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}
	c.SetBalance(new(big.Int).Sub(c.Balance(), amount))
}

func (self *stateObject) SetBalance(amount *big.Int) {
	self.db.journal.append(balanceChange{
		account: &self.address,
		prev:    new(big.Int).Set(self.data.Balance),
	})
	self.setBalance(amount)
}

func (self *stateObject) setBalance(amount *big.Int) {
	self.data.Balance = amount
}

// Return the gas back to the origin. Used by the Virtual machine or Closures
func (c *stateObject) ReturnGas(gas *big.Int) {}

func (self *stateObject) deepCopy(db *StateDB) *stateObject {
	stateObject := newObject(db, self.address, &self.data)
	if self.storageTrie != nil {
		stateObject.storageTrie = db.db.CopyTrie(self.storageTrie)
	}
	if self.stakeTrie != nil {
		stateObject.stakeTrie = db.db.CopyTrie(self.stakeTrie)
	}
	stateObject.code = self.code
	stateObject.dirtyStorage = self.dirtyStorage.Copy()
	stateObject.cachedStorage = self.dirtyStorage.Copy()
	stateObject.dirtyStake = self.dirtyStake.Copy()
	stateObject.suicided = self.suicided
	stateObject.dirtyCode = self.dirtyCode
	stateObject.deleted = self.deleted
	return stateObject
}

func (self *stateObject) setType(aType common.AccountType, feeRatio uint32) {
	if self.data.Type != aType {
		if aType == common.DelegateMiner {
			if !common.FeeRatioValidity(feeRatio) {
				panic(fmt.Sprintf("Wrong fee ratio for delegate miner: %d\n", feeRatio))
			}
			self.data.FeeRatio = feeRatio
		} else {
			self.data.FeeRatio = 0
		}
		self.data.Type = aType

		self.db.journal.append(typeChange{
			account: &self.address,
			prevType: self.data.Type,
		})
	}
}

func (self *stateObject) getType() common.AccountType {
	return self.data.Type
}

func (self *stateObject) setDeposit(db Database, from *stateObject, balance *big.Int, blockNumber *big.Int) error {
	var err error
	var dv common.DepositView
	var dd common.DepositData

	if dv, err = from.getDepositView(db, &self.address); err != nil {
		return err
	}
	if dd, err = self.getDepositData(db, &from.address); err != nil {
		return err
	}

	checkDepositValidity(self, from, &dd, &dv)
	if !dv.Empty() {
		return common.ErrRedeposit
	}

	// Delegate miner record the default account deposit information.
	self.dirtyStake[from.address] = common.DepositData{
		Balance: new (big.Int).Set(balance),
		BlockNumber: new (big.Int).Set(blockNumber),
	}

	// Default account record to which delegate miner it deposited.
	from.dirtyStake[self.address] = common.DepositView{
		DepositData:common.DepositData{
			Balance: new (big.Int).Set(balance),
			BlockNumber: new (big.Int).Set(blockNumber),
		},
		FeeRatio: self.data.FeeRatio,
	}

	// Delegate miner update deposit balance.
	prevMinerDeposit := new (big.Int).Set(self.data.DepositBalance)
	self.data.DepositBalance.Add(self.data.DepositBalance, balance)
	self.db.journal.append(depositSinkChange{
		account: &self.address,
		from: &from.address,
		prev: prevMinerDeposit,
	})

	// Customer account update deposit balance
	from.data.Balance.Sub(from.data.Balance, balance)
	from.data.DepositBalance.Add(from.data.DepositBalance, balance)
	self.db.journal.append(depositUp{
		account: &from.address,
		deltaBalance: new (big.Int).Set(balance),
	})

	return nil
}

func (self *stateObject) rmDeposit(db Database, from *stateObject) error {
	var err error
	var dv common.DepositView
	var dd common.DepositData

	if dv, err = from.getDepositView(db, &self.address); err != nil {
		return err
	}
	if dd, err = self.getDepositData(db, &from.address); err != nil {
		return err
	}

	checkDepositValidity(self, from, &dd, &dv)
	if dv.Empty() {
		return common.ErrNoDepositBalance
	}

	// Delegate miner remove the record.
	self.dirtyStake[from.address] = common.DepositData{
		Balance: nil,
		BlockNumber: nil,
	}

	// Default account remove the record
	from.dirtyStake[self.address] = common.DepositView{
		DepositData:common.DepositData{
			Balance: nil,
			BlockNumber: nil,
		},
		FeeRatio: 0,
	}

	// Delegate miner update deposit balance.
	prevDeposit := new (big.Int).Set(self.data.DepositBalance)
	self.data.DepositBalance.Sub(self.data.DepositBalance, dv.Balance)
	self.db.journal.append(depositSinkChange{
		account: &self.address,
		from: &from.address,
		prev: prevDeposit,
	})

	// Default account update deposit balance
	from.data.Balance.Add(from.data.Balance, dv.Balance)
	from.data.DepositBalance.Sub(from.data.DepositBalance, dv.Balance)
	self.db.journal.append(depositDown{
		account: &from.address,
		deltaBalance: new (big.Int).Set(dv.Balance),
	})

	return nil
}

func (self *stateObject) getDepositData(db Database, pAddr *common.Address) (common.DepositData, error) {
	if data, exist := self.dirtyStake[*pAddr]; exist {
		log.Warn(fmt.Sprintf("Get deposit data from dirty for miner: %s\n", pAddr.String()))
		return data.(common.DepositData), nil
	}

	var dataBytes []byte
	var err error
	var data common.DepositData
	if dataBytes, err = self.getStakeTrie(db).TryGet(pAddr[:]); err == nil {
		if len(dataBytes) > 0 {
			// Found
			err = rlp.DecodeBytes(dataBytes, &data)
		}
	}
	return data, err
}

func (self *stateObject) getDepositView(db Database, pAddr *common.Address) (common.DepositView, error) {
	if data, exist := self.dirtyStake[*pAddr]; exist {
		log.Warn(fmt.Sprintf("Get deposit view from dirty for user: %s\n", pAddr.String()))
		return data.(common.DepositView), nil
	}

	var dataBytes []byte
	var err error
	var data common.DepositView
	if dataBytes, err = self.getStakeTrie(db).TryGet(pAddr[:]); err == nil {
		if len(dataBytes) > 0 {
			// Found
			err = rlp.DecodeBytes(dataBytes, &data)
		}
	}
	return data, err
}

//
// Attribute accessors
//

// Returns the address of the contract/account
func (c *stateObject) Address() common.Address {
	return c.address
}

// Code returns the contract code associated with this object, if any.
func (self *stateObject) Code(db Database) []byte {
	if self.code != nil {
		return self.code
	}
	if bytes.Equal(self.CodeHash(), crypto.EmptyKeccak256) {
		return nil
	}
	code, err := db.ContractCode(self.addrHash, common.BytesToHash(self.CodeHash()))
	if err != nil {
		self.setError(fmt.Errorf("can't load code hash %x: %v", self.CodeHash(), err))
	}
	self.code = code
	return code
}

func (self *stateObject) SetCode(codeHash common.Hash, code []byte) {
	prevcode := self.Code(self.db.db)
	self.db.journal.append(codeChange{
		account:  &self.address,
		prevhash: self.CodeHash(),
		prevcode: prevcode,
	})
	self.setCode(codeHash, code)
}

func (self *stateObject) setCode(codeHash common.Hash, code []byte) {
	self.code = code
	self.data.CodeHash = codeHash[:]
	self.dirtyCode = true
}

func (self *stateObject) SetNonce(nonce uint64) {
	self.db.journal.append(nonceChange{
		account: &self.address,
		prev:    self.data.Nonce,
	})
	self.setNonce(nonce)
}

func (self *stateObject) setNonce(nonce uint64) {
	self.data.Nonce = nonce
}

func (self *stateObject) CodeHash() []byte {
	return self.data.CodeHash
}

func (self *stateObject) Balance() *big.Int {
	return self.data.Balance
}

func (self *stateObject) DepositBalance() *big.Int {
	return self.data.DepositBalance
}

func (self *stateObject) Nonce() uint64 {
	return self.data.Nonce
}

// Never called, but must be present to allow stateObject to be used
// as a vm.Account interface that also satisfies the vm.ContractRef
// interface. Interfaces are awesome.
func (self *stateObject) Value() *big.Int {
	panic("Value on stateObject should never be called")
}
