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

// Package core implements the Ethereum consensus protocol.
package core

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"container/list"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/hashicorp/golang-lru"
	"gopkg.in/karalabe/cookiejar.v2/collections/prque"
	"runtime/debug"
)

var (
	blockInsertTimer = metrics.NewRegisteredTimer("chain/inserts", nil)

	ErrNoGenesis = errors.New("Genesis not found in chain")
)

const (
	bodyCacheLimit      = 256
	blockCacheLimit     = 256
	maxFutureBlocks     = 256
	maxTimeFutureBlocks = 30
	badBlockLimit       = 10
	triesInMemory       = 128
	epochCacheLimit     = 1024

	// BlockChainVersion ensures that an incompatible database forces a resync from scratch.
	BlockChainVersion = 3
)

// CacheConfig contains the configuration values for the trie caching/pruning
// that's resident in a blockchain.
type CacheConfig struct {
	Disabled      bool          // Whether to disable trie write caching (archive node)
	TrieNodeLimit int           // Memory limit (MB) at which to flush the current in-memory trie to disk
	TrieTimeLimit time.Duration // Time limit after which to flush the current in-memory trie to disk
}

// DAGManager represents the canonical chain given a database with a genesis
// block. The Blockchain manages chain imports, reverts, chain reorganisations.
//
// Importing blocks in to the block chain happens according to the set of rules
// defined by the two stage Validator. Processing of blocks is done using the
// Processor which processes the included transaction. The validation of the state
// is done in the second part of the Validator. Failing results in aborting of
// the import.
//
// The DAGManager also helps in returning blocks from **any** chain included
// in the database as well as blocks that represents the canonical chain. It's
// important to note that GetBlock can return any block and does not need to be
// included in the canonical one where as GetBlockByNumber always represents the
// canonical chain.
type DAGManager struct {
	chainConfig *params.ChainConfig // Chain & network configuration
	cacheConfig *CacheConfig        // Cache configuration for pruning

	db     ethdb.Database // Low level persistent database to store final content in
	triegc *prque.Prque   // Priority queue mapping block numbers to tries to gc
	gcproc time.Duration  // Accumulates canonical block processing for trie dumping

	hc            *HeaderChain
	rmLogsFeed    event.Feed
	chainFeed     event.Feed
	chainSideFeed event.Feed
	chainHeadFeed event.Feed
	logsFeed      event.Feed
	scope         event.SubscriptionScope
	genesisBlock  *types.Block

	mu      sync.RWMutex // global mutex for locking chain operations
	chainmu sync.RWMutex // blockchain insertion lock
	procmu  sync.RWMutex // block processor lock

	checkpoint       int            // checkpoint counts towards the new checkpoint
	currentBlock     atomic.Value   // Current head of the pivot chain
	currentFastBlock atomic.Value   // Current head of the fast-sync chain (may be above the block chain!)
	stateCache       state.Database // State database to reuse between imports (contains state cache)
	bodyCache        *lru.Cache     // Cache for the most recent block bodies
	bodyRLPCache     *lru.Cache     // Cache for the most recent block bodies in RLP encoded format
	blockCache       *lru.Cache     // Cache for the most recent entire blocks
	futureBlocks     *lru.Cache     // future blocks are blocks added for later processing
	epochCache       *lru.Cache     // Cache for the most recent epoch data

	quit    chan struct{} // blockchain quit channel
	running int32         // running must be called atomically
	// procInterrupt must be atomically called
	procInterrupt int32          // interrupt signaler for block processing
	wg            sync.WaitGroup // chain processing wait group for shutting down

	engine    consensus.Engine
	processor Processor // block processor interface
	validator Validator // block and state validator interface
	dag       DagState  //set dag
	vmConfig  vm.Config

	badBlocks *lru.Cache // Bad block cache

	pbm *pendingBlocksManager
}

type BlockAndWait struct {
	Block       common.Hash   `json:"block" gencodec:"required"`
	WaitedBlock []common.Hash `json:"waitedBlocks" gencodec:"required"`
}

type pendingData struct {
	block      *types.Block
	waitedHash map[common.Hash]struct{}
}

type pendingBlocksManager struct {
	pendingBlocks map[common.Hash]pendingData
	waitedBlocks  map[common.Hash]map[common.Hash]struct{}
}

func newPendingBlocksManager() *pendingBlocksManager {
	return &pendingBlocksManager{
		pendingBlocks: make(map[common.Hash]pendingData),
		waitedBlocks:  make(map[common.Hash]map[common.Hash]struct{}),
	}
}

func (pbm *pendingBlocksManager) addBlock(dm *DAGManager, block *types.Block) {
	if _, exist := pbm.pendingBlocks[block.Hash()]; exist {
		return
	}

	log.Info(">>>>> Add block to pending list", "block Hash", block.Hash())
	refs := block.Body().Uncles
	if len(refs) == 0 {
		panic("block has no reference block, should not be added")
	}
	data := pendingData{
		block:      block,
		waitedHash: make(map[common.Hash]struct{}, len(refs)),
	}
	for _, ref := range refs {
		if dm.HasBlock(ref.Hash(), ref.Number.Uint64()) {
			continue
		}

		data.waitedHash[ref.Hash()] = struct{}{}
		waitedBlock, exist := pbm.waitedBlocks[ref.Hash()]
		if exist {
			waitedBlock[block.Hash()] = struct{}{}
		} else {
			tmp := make(map[common.Hash]struct{})
			tmp[block.Hash()] = struct{}{}
			pbm.waitedBlocks[ref.Hash()] = tmp
		}
	}
	pbm.pendingBlocks[block.Hash()] = data
}

func (pbm *pendingBlocksManager) processBlock(block *types.Block) types.Blocks {
	var blocks types.Blocks
	waitedSet, exist := pbm.waitedBlocks[block.Hash()]
	if exist {
		log.Info("Process pending block", "block Hash", block.Hash())
		for waited := range waitedSet {
			// Waited is the hash of the block which is on the pending list and waited for us
			pending, ok := pbm.pendingBlocks[waited]
			if !ok {
				panic("logical error: no pending data")
			}
			_, ok1 := pending.waitedHash[block.Hash()]
			if !ok1 {
				// check if we are on the waited list for the pending block
				panic("logical error: not waited hash")
			}
			delete(pending.waitedHash, block.Hash())
			if len(pending.waitedHash) == 0 {
				// the pending block waited list is empty, then it can be processed.
				blocks = append(blocks, pending.block)
				delete(pbm.pendingBlocks, pending.block.Hash())
			}
		}
		delete(pbm.waitedBlocks, block.Hash())
	}
	return blocks
}

// NewDAGManager returns a fully initialised block chain using information
// available in the database. It initialises the default Ethereum Validator and
// Processor.
func NewDAGManager(db ethdb.Database, cacheConfig *CacheConfig, chainConfig *params.ChainConfig, engine consensus.Engine, vmConfig vm.Config) (*DAGManager, error) {
	if cacheConfig == nil {
		cacheConfig = &CacheConfig{
			TrieNodeLimit: 256 * 1024 * 1024,
			TrieTimeLimit: 5 * time.Minute,
		}
	}
	bodyCache, _ := lru.New(bodyCacheLimit)
	bodyRLPCache, _ := lru.New(bodyCacheLimit)
	blockCache, _ := lru.New(blockCacheLimit)
	futureBlocks, _ := lru.New(maxFutureBlocks)
	badBlocks, _ := lru.New(badBlockLimit)
	epochCache, _ := lru.New(epochCacheLimit)

	dm := &DAGManager{
		chainConfig:  chainConfig,
		cacheConfig:  cacheConfig,
		db:           db,
		triegc:       prque.New(),
		stateCache:   state.NewDatabase(db),
		quit:         make(chan struct{}),
		bodyCache:    bodyCache,
		bodyRLPCache: bodyRLPCache,
		blockCache:   blockCache,
		futureBlocks: futureBlocks,
		epochCache:   epochCache,
		engine:       engine,
		vmConfig:     vmConfig,
		badBlocks:    badBlocks,
		pbm:          newPendingBlocksManager(),
	}
	dm.SetValidator(NewBlockValidator(chainConfig, dm, engine))
	dm.SetProcessor(NewStateProcessor(chainConfig, dm, engine))
	dm.SetDag(NewDAG(dm))

	var err error
	dm.hc, err = NewHeaderChain(db, chainConfig, engine, dm.getProcInterrupt)
	if err != nil {
		return nil, err
	}
	dm.genesisBlock = dm.GetBlockByNumber(0)
	if dm.genesisBlock == nil {
		return nil, ErrNoGenesis
	}
	if err := dm.loadLastState(); err != nil {
		return nil, err
	}
	// Check the current state of the block hashes and make sure that we do not have any of the bad blocks in our chain
	for hash := range BadHashes {
		if header := dm.GetHeaderByHash(hash); header != nil {
			// get the canonical block corresponding to the offending header's number
			headerByNumber := dm.GetHeaderByNumber(header.Number.Uint64())
			// make sure the headerByNumber (if present) is in our current canonical chain
			if headerByNumber != nil && headerByNumber.Hash() == header.Hash() {
				log.Error("Found bad hash, rewinding chain", "number", header.Number, "hash", header.ParentHash)
				dm.SetHead(header.Number.Uint64() - 1)
				log.Error("Chain rewind was successful, resuming normal operation")
			}
		}
	}
	// Take ownership of this particular state
	go dm.update()
	return dm, nil
}

func (dm *DAGManager) getProcInterrupt() bool {
	return atomic.LoadInt32(&dm.procInterrupt) == 1
}

// loadLastState loads the last known chain state from the database. This method
// assumes that the chain manager mutex is held.
func (dm *DAGManager) loadLastState() error {
	// Restore the last known head block
	head := rawdb.ReadHeadBlockHash(dm.db)
	if head == (common.Hash{}) {
		// Corrupt or empty database, init from scratch
		log.Warn("Empty database, resetting chain")
		return dm.Reset()
	}

	// Make sure the entire head block is available
	currentBlock := dm.GetBlockByHash(head)
	if currentBlock == nil {
		// Corrupt or empty database, init from scratch
		log.Warn("Head block missing, resetting chain", "hash", head)
		return dm.Reset()
	}
	// Make sure the state associated with the block is available
	if _, err := state.New(currentBlock.Root(), dm.stateCache); err != nil {
		// Dangling block without a state associated, init from scratch
		log.Warn("Head state missing, repairing chain", "number", currentBlock.Number(), "hash", currentBlock.Hash())
		if err := dm.repair(&currentBlock); err != nil {
			return err
		}
	}
	// Everything seems to be fine, set as the head block
	dm.currentBlock.Store(currentBlock)

	// Restore the last known head header
	currentHeader := currentBlock.Header()
	if head := rawdb.ReadHeadHeaderHash(dm.db); head != (common.Hash{}) {
		if header := dm.GetHeaderByHash(head); header != nil {
			currentHeader = header
		}
	}
	dm.hc.SetCurrentHeader(currentHeader)

	// Restore the last known head fast block
	dm.currentFastBlock.Store(currentBlock)
	if head := rawdb.ReadHeadFastBlockHash(dm.db); head != (common.Hash{}) {
		if block := dm.GetBlockByHash(head); block != nil {
			dm.currentFastBlock.Store(block)
		}
	}

	// Issue a status log for the user
	currentFastBlock := dm.CurrentFastBlock()

	headerTd := dm.GetTd(currentHeader.Hash(), currentHeader.Number.Uint64())
	blockTd := dm.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	fastTd := dm.GetTd(currentFastBlock.Hash(), currentFastBlock.NumberU64())

	log.Info("Loaded most recent local header", "number", currentHeader.Number, "hash", currentHeader.Hash(), "td", headerTd)
	log.Info("Loaded most recent local full block", "number", currentBlock.Number(), "hash", currentBlock.Hash(), "td", blockTd)
	log.Info("Loaded most recent local fast block", "number", currentFastBlock.Number(), "hash", currentFastBlock.Hash(), "td", fastTd)

	return nil
}

// SetHead rewinds the local dm to a new epoch. In the case of headers, everything
// above the new head will be deleted and the new one set. In the case of blocks
// though, the head may be further rewound if block bodies are missing (non-archive
// nodes after a fast sync).
func (dm *DAGManager) SetHead(epoch uint64) error {
	log.Warn("Rewinding dm", "target", epoch)

	dm.mu.Lock()
	defer dm.mu.Unlock()

	// Rewind the header chain, deleting all block bodies until then
	delFn := func(db rawdb.DatabaseDeleter, hash common.Hash, num uint64) {
		rawdb.DeleteBody(db, hash, num)
	}
	dm.hc.SetHead(epoch, delFn)
	currentHeader := dm.hc.CurrentHeader()

	// Clear out any stale content from the caches
	dm.bodyCache.Purge()
	dm.bodyRLPCache.Purge()
	dm.blockCache.Purge()
	dm.futureBlocks.Purge()

	// Rewind the block chain, ensuring we don't end up with a stateless head block
	if currentBlock := dm.CurrentBlock(); currentBlock != nil && currentHeader.Number.Uint64() < currentBlock.NumberU64() {
		dm.currentBlock.Store(dm.GetBlock(currentHeader.Hash(), currentHeader.Number.Uint64()))
	}
	if currentBlock := dm.CurrentBlock(); currentBlock != nil {
		if _, err := state.New(currentBlock.Root(), dm.stateCache); err != nil {
			// Rewound state missing, rolled back to before pivot, reset to genesis
			dm.currentBlock.Store(dm.genesisBlock)
		}
	}
	// Rewind the fast block in a simpleton way to the target head
	if currentFastBlock := dm.CurrentFastBlock(); currentFastBlock != nil && currentHeader.Number.Uint64() < currentFastBlock.NumberU64() {
		dm.currentFastBlock.Store(dm.GetBlock(currentHeader.Hash(), currentHeader.Number.Uint64()))
	}
	// If either blocks reached nil, reset to the genesis state
	if currentBlock := dm.CurrentBlock(); currentBlock == nil {
		dm.currentBlock.Store(dm.genesisBlock)
	}
	if currentFastBlock := dm.CurrentFastBlock(); currentFastBlock == nil {
		dm.currentFastBlock.Store(dm.genesisBlock)
	}
	currentBlock := dm.CurrentBlock()
	currentFastBlock := dm.CurrentFastBlock()

	rawdb.WriteHeadBlockHash(dm.db, currentBlock.Hash())
	rawdb.WriteHeadFastBlockHash(dm.db, currentFastBlock.Hash())

	return dm.loadLastState()
}

// FastSyncCommitHead sets the current head block to the one defined by the hash
// irrelevant what the chain contents were prior.
func (dm *DAGManager) FastSyncCommitHead(hash common.Hash) error {
	// Make sure that both the block as well at its state trie exists
	block := dm.GetBlockByHash(hash)
	if block == nil {
		return fmt.Errorf("non existent block [%x…]", hash[:4])
	}
	if _, err := trie.NewSecure(block.Root(), dm.stateCache.TrieDB(), 0); err != nil {
		return err
	}
	// If all checks out, manually set the head block
	dm.mu.Lock()
	dm.currentBlock.Store(block)
	dm.mu.Unlock()

	log.Info("Committed new head block", "number", block.Number(), "hash", hash)
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (dm *DAGManager) GasLimit() uint64 {
	return dm.CurrentBlock().GasLimit()
}

func (dm *DAGManager) GetBlocksByEpoch(epoch uint64) types.Blocks {
	// TODO: impl
	return types.Blocks{}
}

// CurrentHeader() retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (dm *DAGManager) CurrentHeader() *types.Header {
	return dm.hc.CurrentHeader()
}

// CurrentBlock retrieves the current head block of the canonical chain. The
// block is retrieved from the blockchain's internal cache.
func (dm *DAGManager) CurrentBlock() *types.Block {
	return dm.currentBlock.Load().(*types.Block)
}

// CurrentFastBlock retrieves the current fast-sync head block of the canonical
// chain. The block is retrieved from the blockchain's internal cache.
func (dm *DAGManager) CurrentFastBlock() *types.Block {
	return dm.currentFastBlock.Load().(*types.Block)
}

// SetProcessor sets the processor required for making state modifications.
func (dm *DAGManager) SetProcessor(processor Processor) {
	dm.procmu.Lock()
	defer dm.procmu.Unlock()
	dm.processor = processor
}

// SetValidator sets the validator which is used to validate incoming blocks.
func (dm *DAGManager) SetValidator(validator Validator) {
	dm.procmu.Lock()
	defer dm.procmu.Unlock()
	dm.validator = validator
}

//SetDag
func (dm *DAGManager) SetDag(dag DagState) {
	dm.procmu.Lock()
	defer dm.procmu.Unlock()
	dm.dag = dag
}

func (dm *DAGManager) Dag() DagState {
	dm.procmu.RLock()
	defer dm.procmu.RUnlock()
	return dm.dag
}

// Validator returns the current validator.
func (dm *DAGManager) Validator() Validator {
	dm.procmu.RLock()
	defer dm.procmu.RUnlock()
	return dm.validator
}

// Processor returns the current processor.
func (dm *DAGManager) Processor() Processor {
	dm.procmu.RLock()
	defer dm.procmu.RUnlock()
	return dm.processor
}

// State returns a new mutable state based on the current HEAD block.
func (dm *DAGManager) State() (*state.StateDB, error) {
	return dm.StateAt(dm.CurrentBlock().Root())
}

// StateAt returns a new mutable state based on a particular point in time.
func (dm *DAGManager) StateAt(root common.Hash) (*state.StateDB, error) {
	return state.New(root, dm.stateCache)
}

//
func (dm *DAGManager) GetState(root common.Hash) (*state.StateDB, error) {
	return state.New(root, dm.stateCache)
}

// Reset purges the entire blockchain, restoring it to its genesis state.
func (dm *DAGManager) Reset() error {
	return dm.ResetWithGenesisBlock(dm.genesisBlock)
}

// ResetWithGenesisBlock purges the entire blockchain, restoring it to the
// specified genesis state.
func (dm *DAGManager) ResetWithGenesisBlock(genesis *types.Block) error {
	// Dump the entire block chain and purge the caches
	if err := dm.SetHead(0); err != nil {
		return err
	}
	dm.mu.Lock()
	defer dm.mu.Unlock()

	// Prepare the genesis block and reinitialise the chain
	if err := dm.hc.WriteTd(genesis.Hash(), genesis.NumberU64(), genesis.Difficulty()); err != nil {
		log.Crit("Failed to write genesis block TD", "err", err)
	}
	rawdb.WriteBlock(dm.db, genesis)

	dm.genesisBlock = genesis
	dm.insert(dm.genesisBlock)
	dm.currentBlock.Store(dm.genesisBlock)
	dm.hc.SetGenesis(dm.genesisBlock.Header())
	dm.hc.SetCurrentHeader(dm.genesisBlock.Header())
	dm.currentFastBlock.Store(dm.genesisBlock)

	return nil
}

// repair tries to repair the current blockchain by rolling back the current block
// until one with associated state is found. This is needed to fix incomplete db
// writes caused either by crashes/power outages, or simply non-committed tries.
//
// This method only rolls back the current block. The current header and current
// fast block are left intact.
func (dm *DAGManager) repair(head **types.Block) error {
	for {
		// Abort if we've rewound to a head block that does have associated state
		if _, err := state.New((*head).Root(), dm.stateCache); err == nil {
			log.Info("Rewound blockchain to past state", "number", (*head).Number(), "hash", (*head).Hash())
			return nil
		}
		// Otherwise rewind one block and recheck state availability there
		(*head) = dm.GetBlock((*head).ParentHash(), (*head).NumberU64()-1)
	}
}

// Export writes the active chain to the given writer.
func (dm *DAGManager) Export(w io.Writer) error {
	return dm.ExportN(w, uint64(0), dm.CurrentBlock().NumberU64())
}

// ExportN writes a subset of the active chain to the given writer.
func (dm *DAGManager) ExportN(w io.Writer, first uint64, last uint64) error {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	if first > last {
		return fmt.Errorf("export failed: first (%d) is greater than last (%d)", first, last)
	}
	log.Info("Exporting batch of blocks", "count", last-first+1)

	for nr := first; nr <= last; nr++ {
		block := dm.GetBlockByNumber(nr)
		if block == nil {
			return fmt.Errorf("export failed on #%d: not found", nr)
		}

		if err := block.EncodeRLP(w); err != nil {
			return err
		}
	}

	return nil
}

// insert injects a new head block into the current block chain. This method
// assumes that the block is indeed a true head. It will also reset the head
// header and the head fast sync block to this very same block if they are older
// or if they are on a different side chain.
//
// Note, this function assumes that the `mu` mutex is held!
func (dm *DAGManager) insert(block *types.Block) {
	// If the block is on a side chain or an unknown one, force other heads onto it too
	updateHeads := rawdb.ReadCanonicalHash(dm.db, block.NumberU64()) != block.Hash()

	// Add the block to the canonical chain number scheme and mark as the head
	rawdb.WriteCanonicalHash(dm.db, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(dm.db, block.Hash())

	dm.currentBlock.Store(block)

	// If the block is better than our head or is on a different chain, force update heads
	if updateHeads {
		dm.hc.SetCurrentHeader(block.Header())
		rawdb.WriteHeadFastBlockHash(dm.db, block.Hash())

		dm.currentFastBlock.Store(block)
	}
}

// Genesis retrieves the chain's genesis block.
func (dm *DAGManager) Genesis() *types.Block {
	return dm.genesisBlock
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (dm *DAGManager) GetBody(hash common.Hash) *types.Body {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := dm.bodyCache.Get(hash); ok {
		body := cached.(*types.Body)
		return body
	}
	number := dm.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(dm.db, hash, *number)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	dm.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (dm *DAGManager) GetBodyRLP(hash common.Hash) rlp.RawValue {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := dm.bodyRLPCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := dm.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyRLP(dm.db, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// Cache the found body for next time and return
	dm.bodyRLPCache.Add(hash, body)
	return body
}

// HasBlock checks if a block is fully present in the database or not.
func (dm *DAGManager) HasBlock(hash common.Hash, number uint64) bool {
	if dm.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(dm.db, hash, number)
}

// HasState checks if state trie is fully present in the database or not.
func (dm *DAGManager) HasState(hash common.Hash) bool {
	_, err := dm.stateCache.OpenTrie(hash)
	return err == nil
}

// HasBlockAndState checks if a block and associated state trie is fully present
// in the database or not, caching it if present.
func (dm *DAGManager) HasBlockAndState(hash common.Hash, number uint64) bool {
	// Check first that the block itself is known
	block := dm.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return dm.HasState(block.Root())
}

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (dm *DAGManager) GetBlock(hash common.Hash, number uint64) *types.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if block, ok := dm.blockCache.Get(hash); ok {
		return block.(*types.Block)
	}
	block := rawdb.ReadBlock(dm.db, hash, number)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	dm.blockCache.Add(block.Hash(), block)
	return block
}

// GetEpoch retrieves a epoch data from the database by hash and number,
// caching it if found.
func (dm *DAGManager) GetEpochData(hash common.Hash, number uint64) *types.EpochData {
	// Short circuit if the epoch data is already in the cache, retrieve otherwise
	if epochData, ok := dm.epochCache.Get(hash); ok {
		return epochData.(*types.EpochData)
	}
	epochData := rawdb.ReadEpochData(dm.db, hash, number)
	if epochData == nil {
		return nil
	}
	// Cache the found epoch data for next time and return
	dm.epochCache.Add(epochData.PivotBlockHeader.Hash(), epochData)
	return epochData
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (dm *DAGManager) GetBlockByHash(hash common.Hash) *types.Block {
	number := dm.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return dm.GetBlock(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (dm *DAGManager) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(dm.db, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return dm.GetBlock(hash, number)
}

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (dm *DAGManager) GetReceiptsByHash(hash common.Hash) types.Receipts {
	number := rawdb.ReadHeaderNumber(dm.db, hash)
	if number == nil {
		return nil
	}
	return rawdb.ReadReceipts(dm.db, hash, *number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (dm *DAGManager) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
	number := dm.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := dm.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		*number--
	}
	return
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (dm *DAGManager) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	uncles := []*types.Header{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = dm.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return uncles
}

// TrieNode retrieves a blob of data associated with a trie node (or code hash)
// either from ephemeral in-memory cache, or from persistent storage.
func (dm *DAGManager) TrieNode(hash common.Hash) ([]byte, error) {
	return dm.stateCache.TrieDB().Node(hash)
}

// Stop stops the blockchain service. If any imports are currently in progress
// it will abort them using the procInterrupt.
func (dm *DAGManager) Stop() {
	if !atomic.CompareAndSwapInt32(&dm.running, 0, 1) {
		return
	}
	// Unsubscribe all subscriptions registered from blockchain
	dm.scope.Close()
	close(dm.quit)
	atomic.StoreInt32(&dm.procInterrupt, 1)

	dm.wg.Wait()

	// Ensure the state of a recent block is also stored to disk before exiting.
	// We're writing three different states to catch different restart scenarios:
	//  - HEAD:     So we don't need to reprocess any blocks in the general case
	//  - HEAD-1:   So we don't do large reorgs if our HEAD becomes an uncle
	//  - HEAD-127: So we have a hard limit on the number of blocks reexecuted
	if !dm.cacheConfig.Disabled {
		triedb := dm.stateCache.TrieDB()

		for _, offset := range []uint64{0, 1, triesInMemory - 1} {
			if number := dm.CurrentBlock().NumberU64(); number > offset {
				recent := dm.GetBlockByNumber(number - offset)

				log.Info("Writing cached state to disk", "block", recent.Number(), "hash", recent.Hash(), "root", recent.Root())
				if err := triedb.Commit(recent.Root(), true); err != nil {
					log.Error("Failed to commit recent state trie", "err", err)
				}
			}
		}
		for !dm.triegc.Empty() {
			triedb.Dereference(dm.triegc.PopItem().(common.Hash))
		}
		if size, _ := triedb.Size(); size != 0 {
			log.Error("Dangling trie nodes after full cleanup")
		}
	}
	log.Info("Blockchain manager stopped")
}

func (dm *DAGManager) procFutureBlocks() {
	blocks := make([]*types.Block, 0, dm.futureBlocks.Len())
	for _, hash := range dm.futureBlocks.Keys() {
		if block, exist := dm.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, block.(*types.Block))
		}
	}
	if len(blocks) > 0 {
		types.BlockBy(types.Number).Sort(blocks)

		// Insert one by one as chain insertion needs contiguous ancestry between blocks
		for i := range blocks {
			dm.InsertBlocks(blocks[i:i+1], nil)
		}
	}
}

// WriteStatus status of write
type WriteStatus byte

const (
	NonStatTy WriteStatus = iota
	CanonStatTy
	SideStatTy
)

// Rollback is designed to remove a chain of links from the database that aren't
// certain enough to be valid.
func (dm *DAGManager) Rollback(chain []common.Hash) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	for i := len(chain) - 1; i >= 0; i-- {
		hash := chain[i]

		currentHeader := dm.hc.CurrentHeader()
		if currentHeader.Hash() == hash {
			dm.hc.SetCurrentHeader(dm.GetHeader(currentHeader.ParentHash, currentHeader.Number.Uint64()-1))
		}
		if currentFastBlock := dm.CurrentFastBlock(); currentFastBlock.Hash() == hash {
			newFastBlock := dm.GetBlock(currentFastBlock.ParentHash(), currentFastBlock.NumberU64()-1)
			dm.currentFastBlock.Store(newFastBlock)
			rawdb.WriteHeadFastBlockHash(dm.db, newFastBlock.Hash())
		}
		if currentBlock := dm.CurrentBlock(); currentBlock.Hash() == hash {
			newBlock := dm.GetBlock(currentBlock.ParentHash(), currentBlock.NumberU64()-1)
			dm.currentBlock.Store(newBlock)
			rawdb.WriteHeadBlockHash(dm.db, newBlock.Hash())
		}
	}
}

// SetReceiptsData computes all the non-consensus fields of the receipts
func SetReceiptsData(config *params.ChainConfig, block *types.Block, receipts types.Receipts) error {
	signer := types.MakeSigner(config, block.Number())

	transactions, logIndex := block.Transactions(), uint(0)
	if len(transactions) != len(receipts) {
		return errors.New("transaction and receipt count mismatch")
	}

	for j := 0; j < len(receipts); j++ {
		// The transaction hash can be retrieved from the transaction itself
		receipts[j].TxHash = transactions[j].Hash()

		// The contract address can be derived from the transaction itself
		if transactions[j].To() == nil {
			// Deriving the signer is expensive, only do if it's actually needed
			from, _ := types.Sender(signer, transactions[j])
			receipts[j].ContractAddress = crypto.CreateAddress(from, transactions[j].Nonce())
		}
		// The used gas can be calculated based on previous receipts
		if j == 0 {
			receipts[j].GasUsed = receipts[j].CumulativeGasUsed
		} else {
			receipts[j].GasUsed = receipts[j].CumulativeGasUsed - receipts[j-1].CumulativeGasUsed
		}
		// The derived log fields can simply be set from the block and transaction
		for k := 0; k < len(receipts[j].Logs); k++ {
			receipts[j].Logs[k].BlockNumber = block.NumberU64()
			receipts[j].Logs[k].BlockHash = block.Hash()
			receipts[j].Logs[k].TxHash = receipts[j].TxHash
			receipts[j].Logs[k].TxIndex = uint(j)
			receipts[j].Logs[k].Index = logIndex
			logIndex++
		}
	}
	return nil
}

// InsertReceiptChain attempts to complete an already existing header chain with
// transaction and receipt data.
func (dm *DAGManager) InsertReceiptChain(blockChain types.Blocks, receiptChain []types.Receipts) (int, error) {
	dm.wg.Add(1)
	defer dm.wg.Done()

	// Do a sanity check that the provided chain is actually ordered and linked
	for i := 1; i < len(blockChain); i++ {
		if blockChain[i].NumberU64() != blockChain[i-1].NumberU64()+1 || blockChain[i].ParentHash() != blockChain[i-1].Hash() {
			log.Error("Non contiguous receipt insert", "number", blockChain[i].Number(), "hash", blockChain[i].Hash(), "parent", blockChain[i].ParentHash(),
				"prevnumber", blockChain[i-1].Number(), "prevhash", blockChain[i-1].Hash())
			return 0, fmt.Errorf("non contiguous insert: item %d is #%d [%x…], item %d is #%d [%x…] (parent [%x…])", i-1, blockChain[i-1].NumberU64(),
				blockChain[i-1].Hash().Bytes()[:4], i, blockChain[i].NumberU64(), blockChain[i].Hash().Bytes()[:4], blockChain[i].ParentHash().Bytes()[:4])
		}
	}

	var (
		stats = struct{ processed, ignored int32 }{}
		start = time.Now()
		bytes = 0
		batch = dm.db.NewBatch()
	)
	for i, block := range blockChain {
		receipts := receiptChain[i]
		// Short circuit insertion if shutting down or processing failed
		if atomic.LoadInt32(&dm.procInterrupt) == 1 {
			return 0, nil
		}
		// Short circuit if the owner header is unknown
		if !dm.HasHeader(block.Hash(), block.NumberU64()) {
			return i, fmt.Errorf("containing header #%d [%x…] unknown", block.Number(), block.Hash().Bytes()[:4])
		}
		// Skip if the entire data is already known
		if dm.HasBlock(block.Hash(), block.NumberU64()) {
			stats.ignored++
			continue
		}
		// Compute all the non-consensus fields of the receipts
		if err := SetReceiptsData(dm.chainConfig, block, receipts); err != nil {
			return i, fmt.Errorf("failed to set receipts data: %v", err)
		}
		// Write all the data out into the database
		rawdb.WriteBody(batch, block.Hash(), block.NumberU64(), block.Body())
		rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), receipts)
		rawdb.WriteTxLookupEntries(batch, block.Transactions(), block.Header()) //\\fast mode need modify

		stats.processed++

		if batch.ValueSize() >= ethdb.IdealBatchSize {
			if err := batch.Write(); err != nil {
				return 0, err
			}
			bytes += batch.ValueSize()
			batch.Reset()
		}
	}
	if batch.ValueSize() > 0 {
		bytes += batch.ValueSize()
		if err := batch.Write(); err != nil {
			return 0, err
		}
	}

	// Update the head fast sync block if better
	dm.mu.Lock()
	head := blockChain[len(blockChain)-1]
	if td := dm.GetTd(head.Hash(), head.NumberU64()); td != nil { // Rewind may have occurred, skip in that case
		currentFastBlock := dm.CurrentFastBlock()
		if dm.GetTd(currentFastBlock.Hash(), currentFastBlock.NumberU64()).Cmp(td) < 0 {
			rawdb.WriteHeadFastBlockHash(dm.db, head.Hash())
			dm.currentFastBlock.Store(head)
		}
	}
	dm.mu.Unlock()

	log.Info("Imported new block receipts",
		"count", stats.processed,
		"elapsed", common.PrettyDuration(time.Since(start)),
		"number", head.Number(),
		"hash", head.Hash(),
		"size", common.StorageSize(bytes),
		"ignored", stats.ignored)
	return 0, nil
}

var lastWrite uint64

// WriteBlockWithoutState writes only the block and its metadata to the database,
// but does not write any state. This is used to construct competing side forks
// up to the point where they exceed the canonical total difficulty.
func (dm *DAGManager) WriteBlockWithoutState(block *types.Block, td *big.Int) (err error) {
	dm.wg.Add(1)
	defer dm.wg.Done()

	if err := dm.hc.WriteTd(block.Hash(), block.NumberU64(), td); err != nil {
		return err
	}
	rawdb.WriteBlock(dm.db, block)

	return nil
}

func (dm *DAGManager) deleteOldTransaction(oldPivotChain []*types.Header) (err error) {
	var (
		deletedLogs []*types.Log

		collectLogs = func(hash common.Hash) {
			// Coalesce logs and set 'Removed'.
			number := dm.hc.GetBlockNumber(hash)
			if number == nil {
				return
			}
			receipts := rawdb.ReadReceipts(dm.db, hash, *number)
			for _, receipt := range receipts {
				for _, log := range receipt.Logs {
					del := *log
					del.Removed = true
					deletedLogs = append(deletedLogs, &del)
				}
			}
		}
	)

	batch := dm.db.NewBatch()
	// delete transactions on the old pivot chain
	if len(oldPivotChain) != 0 {
		for _, h := range oldPivotChain {
			epoc := dm.GetEpochData(h.Hash(), h.Number.Uint64())
			for _, tx := range epoc.Transactions {
				rawdb.DeleteTxLookupEntry(batch, tx.Hash())
			}

			collectLogs(h.Hash())
		}
	}
	batch.Write()
	if err := batch.Write(); err != nil {
		return errors.New("Delete old transactions from db error!")
	}

	if len(deletedLogs) > 0 {
		go dm.rmLogsFeed.Send(RemovedLogsEvent{deletedLogs})
	}

	//1.0 should send side blocks to prossible uncles
	//2.0 should get these blocks from dag
	if len(oldPivotChain) > 0 {
		go func() {
			for _, h := range oldPivotChain {
				b := dm.GetBlock(h.Hash(), h.Number.Uint64())
				dm.chainSideFeed.Send(ChainSideEvent{Block: b})
			}
		}()
	}

	return nil
}

// WriteBlockWithState write epoch data and all associated state to the database.
func (dm *DAGManager) WriteBlockWithState(pivotBlock *types.Block,
	referenceBlocks []*types.Block,
	transactions types.Transactions,
	receipts types.Receipts,
	state *state.StateDB) (status WriteStatus, err error) {
	dm.wg.Add(1)
	defer dm.wg.Done()

	// Make sure no inconsistent state is leaked during insertion
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var referenceHeaders []*types.Header
	for _, b := range referenceBlocks {
		referenceHeaders = append(referenceHeaders, b.Header())
	}

	epochData := &types.EpochData{
		PivotBlockHeader:     pivotBlock.Header(),
		ReferenceBlockHeader: referenceHeaders,
		Transactions:         transactions,
		Receipts:             receipts,
	}

	//dag
	referenceHeaders = append(referenceHeaders, pivotBlock.Header())
	reorg, oldPivotChain, newPivotChain, err := dm.Dag().IsReorg(referenceHeaders)

	if err != nil {
		log.Error("WriteBlockWithState error", err)
		return NonStatTy, err
	}

	// Write other block data using a batch
	batch := dm.db.NewBatch()
	for _, b := range referenceBlocks {
		if dm.GetBlock(b.Hash(), b.NumberU64()) != nil {
			continue
		}
		rawdb.WriteBlock(batch, b)
	}
	rawdb.WriteBlock(batch, pivotBlock)

	//write epoch data
	rawdb.WriteEpochData(batch, epochData.PivotBlockHeader.Hash(), epochData.PivotBlockHeader.Number.Uint64(), epochData)
	if err := batch.Write(); err != nil {
		log.Error("batch WriteBlockWithState error", err)
		return NonStatTy, err
	}
	//get pivotBlock and commit db
	block := pivotBlock
	root, err := state.Commit(dm.chainConfig.IsEIP158(block.Number()))
	if err != nil {
		log.Error("state commit error", err)
		return NonStatTy, err
	}
	triedb := dm.stateCache.TrieDB()

	// If we're running an archive node, always flush
	if dm.cacheConfig.Disabled {
		if err := triedb.Commit(root, false); err != nil {
			return NonStatTy, err
		}
	} else {
		// Full but not archive node, do proper garbage collection
		triedb.Reference(root, common.Hash{}) // metadata reference to keep trie alive
		dm.triegc.Push(root, -float32(block.NumberU64()))

		if current := block.NumberU64(); current > triesInMemory {
			// If we exceeded our memory allowance, flush matured singleton nodes to disk
			var (
				nodes, imgs = triedb.Size()
				limit       = common.StorageSize(dm.cacheConfig.TrieNodeLimit) * 1024 * 1024
			)
			if nodes > limit || imgs > 4*1024*1024 {
				triedb.Cap(limit - ethdb.IdealBatchSize)
			}
			// Find the next state trie we need to commit
			header := dm.GetHeaderByNumber(current - triesInMemory)
			chosen := header.Number.Uint64()

			// If we exceeded out time allowance, flush an entire trie to disk
			if dm.gcproc > dm.cacheConfig.TrieTimeLimit {
				// If we're exceeding limits but haven't reached a large enough memory gap,
				// warn the user that the system is becoming unstable.
				if chosen < lastWrite+triesInMemory && dm.gcproc >= 2*dm.cacheConfig.TrieTimeLimit {
					log.Info("State in memory for too long, committing", "time", dm.gcproc, "allowance", dm.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/triesInMemory)
				}
				// Flush an entire trie and restart the counters
				triedb.Commit(header.Root, true)
				lastWrite = chosen
				dm.gcproc = 0
			}
			// Garbage collect anything below our required write retention
			for !dm.triegc.Empty() {
				root, number := dm.triegc.Pop()
				if uint64(-number) > chosen {
					dm.triegc.Push(root, number)
					break
				}
				triedb.Dereference(root.(common.Hash))
			}
		}
	}

	rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), receipts)

	//reorg
	if reorg {
		if err := dm.deleteOldTransaction(oldPivotChain); err != nil {
			log.Error("reorg WriteBlockWithState error", err)
			return NonStatTy, err
		}

		//write transactions on the new pivot chain
		if len(newPivotChain) > 1 {
			tempNewPivotChain := newPivotChain[:len(newPivotChain)-2]
			for _, h := range tempNewPivotChain {
				epoc := dm.GetEpochData(h.Hash(), h.Number.Uint64())
				if epoc == nil {
					log.Error("FATAL ERROR", "can not get epoc : ", h.Number.Uint64())
					panic("can not get epoc error.\n")
				}
				rawdb.WriteTxLookupEntries(batch, epoc.Transactions, h)
			}

		}
		rawdb.WriteTxLookupEntries(batch, epochData.Transactions, pivotBlock.Header())

		// Write the positional metadata for preimages
		rawdb.WritePreimages(batch, block.NumberU64(), state.Preimages())

		status = CanonStatTy
	} else {
		status = SideStatTy
	}

	if err := batch.Write(); err != nil {
		log.Error("batch write error", err)
		return NonStatTy, err
	}
	// Set new head.
	if status == CanonStatTy {
		for _, h := range newPivotChain {
			b := dm.GetBlock(h.Hash(), h.Number.Uint64())
			dm.insert(b)
			log.Info("CanonStatTy   WriteBlockWithState", "num:", h.Number.Uint64(), "hash:", h.Hash().String())
		}
		dm.Dag().InsertBlocks(referenceHeaders)
	}

	for _, h := range referenceHeaders {
		dm.futureBlocks.Remove(h.Hash()) //\\  waitUncleBlocks.Remove
	}

	return status, nil
}

// InsertBlocks attempts to insert the given batch of blocks in to the canonical
// chain or, otherwise, create a fork. If an error is returned it will return
// the index number of the failing block as well an error describing what went
// wrong.
//
// After insertion is done, all accumulated events will be fired.
func (dm *DAGManager) InsertBlocks(blocks types.Blocks, refBlocks *list.List) (int, error) {
	n, events, logs, err := dm.insertBlocks(blocks, refBlocks)
	dm.PostChainEvents(events, logs)
	return n, err
}

//Based pivot block fetch uncles transactions
func (dm *DAGManager) getPivotBlockReferencesTxs(block *types.Block) types.TransactionRefs {
	var (
		txRefs      types.TransactionRefs = make([]*types.TransactionRef, 0)
		refTxsTmp   types.Transactions    = make([]*types.Transaction, 0)
		refTxs      types.Transactions    = make([]*types.Transaction, 0)
		ancestorTxs types.Transactions    = make([]*types.Transaction, 0)
		index       uint64
	)
	for i := 0; i < len(block.Uncles()); i++ {
		if dm.HasBlock(block.Uncles()[i].Hash(), block.Uncles()[i].Number.Uint64()) {
			if len(refTxsTmp) > 0 {
				for _, txin := range dm.GetBlockByHash(block.Uncles()[i].Hash()).Transactions() {
					for k := len(refTxsTmp) - 1; k >= 0; k-- {
						if txin.Hash() == refTxsTmp[k].Hash() {
							break
						}
						if k == 0 && txin.Hash() != refTxsTmp[k].Hash() {
							refTxsTmp = append(refTxsTmp, txin)
						}
					}

				}
			} else {
				refTxsTmp = append(refTxsTmp, dm.GetBlockByHash(block.Uncles()[i].Hash()).Transactions()...)
			}

		} else {
			log.Warn("the block uncles is not complete, num:", block.Uncles()[i].Number.Uint64())
		}
	}
	if len(block.Uncles()) > 0 {
		tmpParent := dm.GetBlockByHash(block.ParentHash())
		for index = 1; index <= 7; index++ {
			if block.Number().Uint64()-index >= 0 {
				ancestorTxs = append(ancestorTxs, dm.GetBlockByHash(tmpParent.Hash()).Transactions()...)
				tmpParent = dm.GetBlockByHash(tmpParent.Hash())
			}
		}
	}

	//parentTxs := dm.GetBlockByHash(block.ParentHash()).Transactions()
	if len(ancestorTxs) > 0 {
		for _, rtx := range refTxsTmp {
			for i := len(ancestorTxs) - 1; i >= 0; i-- {
				if rtx.Hash() == ancestorTxs[i].Hash() {
					break
				}
				if i == 0 && rtx.Hash() != ancestorTxs[i].Hash() {
					refTxs = append(refTxs, rtx)
				}
			}
		}
	} else {
		refTxs = append(refTxs, refTxsTmp...)
	}

	for _, tx := range refTxs {
		txRefs = append(txRefs, &types.TransactionRef{Tx: tx, IsRef: true})
	}

	return txRefs
}

//Based pivot block fetch uncles
func (dm *DAGManager) getPivotBlockReferences(block *types.Block) types.Blocks {
	var refers types.Blocks
	for i := 0; i < len(block.Uncles()); i++ {
		if dm.HasBlock(block.Uncles()[i].Hash(), block.Uncles()[i].Number.Uint64()) {
			refers = append(refers, dm.GetBlockByHash(block.Uncles()[i].Hash()))
		}
	}
	return refers
}

// insertBlocks will execute the actual chain insertion and event aggregation. The
// only reason this method exists as a separate one is to make locking cleaner
// with deferred statements.
func (dm *DAGManager) insertBlocks(blocks types.Blocks, refBlocks *list.List) (int, []interface{}, []*types.Log, error) {
	if len(blocks) == 0 {
		return 0, nil, nil, nil
	}

	fmt.Printf("insertBlocks block number : %v , block hash: %v\n", blocks[0].Number().String(), blocks[0].Hash().String())
	for i := 1; i < len(blocks); i++ {
		// Do a sanity check that the provided chain is actually ordered and linked
		if blocks[i].NumberU64() != blocks[i-1].NumberU64()+1 || blocks[i].ParentHash() != blocks[i-1].Hash() {
			// Chain broke ancestry, log a messge (programming error) and skip insertion
			log.Error("Non contiguous block insert", "number", blocks[i].Number(), "hash", blocks[i].Hash(),
				"parent", blocks[i].ParentHash(), "prevnumber", blocks[i-1].Number(), "prevhash", blocks[i-1].Hash())

			return 0, nil, nil, fmt.Errorf("non contiguous insert: item %d is #%d [%x…], item %d is #%d [%x…] (parent [%x…])", i-1, blocks[i-1].NumberU64(),
				blocks[i-1].Hash().Bytes()[:4], i, blocks[i].NumberU64(), blocks[i].Hash().Bytes()[:4], blocks[i].ParentHash().Bytes()[:4])
		}
		fmt.Printf("insertBlocks block number : %v , block hash: %v\n", blocks[i].Number().String(), blocks[i].Hash().String())
	}

	dm.wg.Add(1)
	defer dm.wg.Done()

	dm.chainmu.Lock()
	defer dm.chainmu.Unlock()
	defer func() {
		log.Info("leave insertBlocks")
	}()

	// A queued approach to delivering events. This is generally
	// faster than direct delivery and requires much less mutex
	// acquiring.
	var (
		stats         = insertStats{startTime: mclock.Now()}
		events        = make([]interface{}, 0, len(blocks))
		lastCanon     *types.Block
		coalescedLogs []*types.Log
	)

	// Start the parallel header verifier
	//\\headers := make([]*types.Header, len(chain))
	//\\seals := make([]bool, len(chain))

	headers := make([]*types.Header, 1)
	seals := make([]bool, 1)

	// Start a parallel signature recovery (signer will fluke on fork transition, minimal perf loss)
	senderCacher.recoverFromBlocks(types.MakeSigner(dm.chainConfig, blocks[0].Number()), blocks)

	// Iterate over the blocks and insert when the verifier permits
	for i, block := range blocks {
		headers[0] = block.Header()
		seals[0] = true
		abort, results := dm.engine.VerifyHeaders(dm, headers, seals)
		defer close(abort)

		// If the chain is terminating, stop processing blocks
		if atomic.LoadInt32(&dm.procInterrupt) == 1 {
			log.Debug("Premature abort during blocks processing")
			break
		}
		// If the header is a banned one, straight out abort
		if BadHashes[block.Hash()] {
			dm.reportBlock(block, nil, ErrBlacklistedHash)
			return i, events, coalescedLogs, ErrBlacklistedHash
		}
		// Wait for the block's verification to complete
		bstart := time.Now()

		err := <-results
		if err == nil {
			err = dm.Validator().ValidateBody(block)
		}
		switch {
		case err == ErrKnownBlock:
			// Block and state both already known. However if the current block is below
			// this number we did a rollback and we should reimport it nonetheless.
			if dm.CurrentBlock().NumberU64() >= block.NumberU64() {
				stats.ignored++
				continue
			}

		case err == consensus.ErrFutureBlock:
			// Allow up to MaxFuture second in the future blocks. If this limit is exceeded
			// the chain is discarded and processed at a later time if given.
			max := big.NewInt(time.Now().Unix() + maxTimeFutureBlocks)
			if block.Time().Cmp(max) > 0 {
				return i, events, coalescedLogs, fmt.Errorf("future block: %v > %v", block.Time(), max)
			}
			dm.futureBlocks.Add(block.Hash(), block)
			stats.queued++
			continue

		case err == consensus.ErrUnknownAncestor && dm.futureBlocks.Contains(block.ParentHash()):
			dm.futureBlocks.Add(block.Hash(), block)
			stats.queued++
			continue

		case err == consensus.ErrPrunedAncestor:
			// Block competing with the canonical chain, store in the db, but don't process
			// until the competitor TD goes above the canonical TD
			currentBlock := dm.CurrentBlock()
			localTd := dm.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
			externTd := new(big.Int).Add(dm.GetTd(block.ParentHash(), block.NumberU64()-1), block.Difficulty())
			if localTd.Cmp(externTd) > 0 {
				if err = dm.WriteBlockWithoutState(block, externTd); err != nil {
					return i, events, coalescedLogs, err
				}
				continue
			}
			// Competitor chain beat canonical, gather all blocks from the common ancestor
			var winner []*types.Block

			parent := dm.GetBlock(block.ParentHash(), block.NumberU64()-1)
			for !dm.HasState(parent.Root()) {
				winner = append(winner, parent)
				parent = dm.GetBlock(parent.ParentHash(), parent.NumberU64()-1)
			}
			for j := 0; j < len(winner)/2; j++ {
				winner[j], winner[len(winner)-1-j] = winner[len(winner)-1-j], winner[j]
			}
			// Import all the pruned blocks to make the state available
			dm.chainmu.Unlock()
			_, evs, logs, err := dm.insertBlocks(winner, nil)
			dm.chainmu.Lock()
			events, coalescedLogs = evs, logs

			if err != nil {
				return i, events, coalescedLogs, err
			}

		case err == ErrUnclesNotCompletely:
			if refBlocks == nil {
				if len(blocks) != 1 {
					panic("logic error, can not handle list of block without reference downloaded")
				}
				dm.pbm.addBlock(dm, block)
				continue
			}
			tmp := make(types.Blocks, 1)
			for _, refHdr := range block.Uncles() {
				processed := false
				for elem := refBlocks.Front(); elem != nil; elem = elem.Next() {
					refBlk := elem.Value.(*types.Block)
					if refHdr.Hash() == refBlk.Hash() {
						tmp[0] = refBlk
						refBlocks.Remove(elem)
						dm.chainmu.Unlock()
						_, pendingEvs, pendingLogs, pendingErr := dm.insertBlocks(tmp, refBlocks)
						dm.chainmu.Lock()
						events = append(events, pendingEvs)
						coalescedLogs = append(coalescedLogs, pendingLogs...)
						if pendingErr != nil {
							return 0, events, coalescedLogs, pendingErr
						}
						processed = true
						break
					}
				}

				if !processed && !dm.HasBlock(refHdr.Hash(), refHdr.Number.Uint64()) {
					panic(fmt.Sprintf("logic error: unknown reference block: %s, %d\n",
						refHdr.Hash().String(), refHdr.Number.Uint64()))
				}
			}

		case err != nil:
			dm.reportBlock(block, nil, err)
			return i, events, coalescedLogs, err
		}

		// Create a new statedb using the parent block and report an
		// error if it fails.
		var parent *types.Block
		if i == 0 {
			parent = dm.GetBlock(block.ParentHash(), block.NumberU64()-1)
		} else {
			parent = blocks[i-1]
		}
		state, err := state.New(parent.Root(), dm.stateCache)
		if err != nil {
			return i, events, coalescedLogs, err
		}
		// Process block using the parent state as reference point.
		execTxs := dm.getPivotBlockReferencesTxs(block)
		referenceBlocks := dm.getPivotBlockReferences(block)
		receipts, logs, usedGas, txs, err := dm.processor.Process(block, block.Transactions(), execTxs, state, dm.vmConfig)
		if err != nil {
			dm.reportBlock(block, receipts, err)
			return i, events, coalescedLogs, err
		}
		// Validate the state using the default validator
		err = dm.Validator().ValidateState(block, parent, state, receipts, usedGas)
		//\err2 := dm.Validator().ValidateHeader(block, state)
		//\\if err != nil || err2 != nil{
		if err != nil {
			dm.reportBlock(block, receipts, err)
			return i, events, coalescedLogs, err
		}
		proctime := time.Since(bstart)
		// Write the block to the chain and get the status.
		status, err := dm.WriteBlockWithState(block, referenceBlocks, txs, receipts, state)
		blockList := dm.pbm.processBlock(block)
		if err != nil {
			return i, events, coalescedLogs, err
		}
		switch status {
		case CanonStatTy:
			log.Debug("Inserted new block", "number", block.Number(), "hash", block.Hash(), "uncles", len(block.Uncles()),
				"txs", len(block.Transactions()), "gas", block.GasUsed(), "elapsed", common.PrettyDuration(time.Since(bstart)))

			coalescedLogs = append(coalescedLogs, logs...)
			blockInsertTimer.UpdateSince(bstart)
			events = append(events, ChainEvent{block, block.Hash(), logs})
			lastCanon = block

			// Only count canonical blocks for GC processing time
			dm.gcproc += proctime

		case SideStatTy:
			log.Debug("Inserted forked block", "number", block.Number(), "hash", block.Hash(), "diff", block.Difficulty(), "elapsed",
				common.PrettyDuration(time.Since(bstart)), "txs", len(block.Transactions()), "gas", block.GasUsed(), "uncles", len(block.Uncles()))

			blockInsertTimer.UpdateSince(bstart)
			events = append(events, ChainSideEvent{block})
		}
		stats.processed++
		stats.usedGas += usedGas

		cache, _ := dm.stateCache.TrieDB().Size()
		stats.report(blocks, i, cache)

		for len(blockList) > 0 {
			dm.chainmu.Unlock()
			_, pendingEvs, pendingLogs, pendingErr := dm.insertBlocks(blockList[0:1], nil)
			dm.chainmu.Lock()
			events = append(events, pendingEvs)
			coalescedLogs = append(coalescedLogs, pendingLogs...)
			if pendingErr != nil {
				return 0, events, coalescedLogs, pendingErr
			}
			blockList = blockList[1:]
		}
	}
	// Append a single chain head event if we've progressed the chain
	if lastCanon != nil && dm.CurrentBlock().Hash() == lastCanon.Hash() {
		events = append(events, ChainHeadEvent{lastCanon})
	}
	return 0, events, coalescedLogs, nil
}

// insertStats tracks and reports on block insertion.
type insertStats struct {
	queued, processed, ignored int
	usedGas                    uint64
	lastIndex                  int
	startTime                  mclock.AbsTime
}

// statsReportLimit is the time limit during import after which we always print
// out progress. This avoids the user wondering what's going on.
const statsReportLimit = 8 * time.Second

// report prints statistics if some number of blocks have been processed
// or more than a few seconds have passed since the last message.
func (st *insertStats) report(chain []*types.Block, index int, cache common.StorageSize) {
	// Fetch the timings for the batch
	var (
		now     = mclock.Now()
		elapsed = time.Duration(now) - time.Duration(st.startTime)
	)
	// If we're at the last block of the batch or report period reached, log
	if index == len(chain)-1 || elapsed >= statsReportLimit {
		var (
			end = chain[index]
			txs = countTransactions(chain[st.lastIndex : index+1])
		)
		context := []interface{}{
			"blocks", st.processed, "txs", txs, "mgas", float64(st.usedGas) / 1000000,
			"elapsed", common.PrettyDuration(elapsed), "mgasps", float64(st.usedGas) * 1000 / float64(elapsed),
			"number", end.Number(), "hash", end.Hash(), "cache", cache,
		}
		if st.queued > 0 {
			context = append(context, []interface{}{"queued", st.queued}...)
		}
		if st.ignored > 0 {
			context = append(context, []interface{}{"ignored", st.ignored}...)
		}
		log.Info("Imported new chain segment", context...)

		*st = insertStats{startTime: now, lastIndex: index + 1}
	}
}

func countTransactions(chain []*types.Block) (c int) {
	for _, b := range chain {
		c += len(b.Transactions())
	}
	return c
}

// reorgs takes two blocks, an old chain and a new chain and will reconstruct the blocks and inserts them
// to be part of the new canonical chain and accumulates potential missing transactions and post an
// event about them
func (dm *DAGManager) reorg(oldBlock, newBlock *types.Block) error {
	var (
		newChain    types.Blocks
		oldChain    types.Blocks
		commonBlock *types.Block
		deletedTxs  types.Transactions
		deletedLogs []*types.Log
		// collectLogs collects the logs that were generated during the
		// processing of the block that corresponds with the given hash.
		// These logs are later announced as deleted.
		collectLogs = func(hash common.Hash) {
			// Coalesce logs and set 'Removed'.
			number := dm.hc.GetBlockNumber(hash)
			if number == nil {
				return
			}
			receipts := rawdb.ReadReceipts(dm.db, hash, *number)
			for _, receipt := range receipts {
				for _, log := range receipt.Logs {
					del := *log
					del.Removed = true
					deletedLogs = append(deletedLogs, &del)
				}
			}
		}
	)

	// first reduce whoever is higher bound
	if oldBlock.NumberU64() > newBlock.NumberU64() {
		// reduce old chain
		for ; oldBlock != nil && oldBlock.NumberU64() != newBlock.NumberU64(); oldBlock = dm.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1) {
			oldChain = append(oldChain, oldBlock)
			deletedTxs = append(deletedTxs, oldBlock.Transactions()...)

			collectLogs(oldBlock.Hash())
		}
	} else {
		// reduce new chain and append new chain blocks for inserting later on
		for ; newBlock != nil && newBlock.NumberU64() != oldBlock.NumberU64(); newBlock = dm.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1) {
			newChain = append(newChain, newBlock)
		}
	}
	if oldBlock == nil {
		return fmt.Errorf("Invalid old chain")
	}
	if newBlock == nil {
		return fmt.Errorf("Invalid new chain")
	}

	for {
		if oldBlock.Hash() == newBlock.Hash() {
			commonBlock = oldBlock
			break
		}

		oldChain = append(oldChain, oldBlock)
		newChain = append(newChain, newBlock)
		deletedTxs = append(deletedTxs, oldBlock.Transactions()...)
		collectLogs(oldBlock.Hash())

		oldBlock, newBlock = dm.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1), dm.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1)
		if oldBlock == nil {
			return fmt.Errorf("Invalid old chain")
		}
		if newBlock == nil {
			return fmt.Errorf("Invalid new chain")
		}
	}
	// Ensure the user sees large reorgs
	if len(oldChain) > 0 && len(newChain) > 0 {
		logFn := log.Debug
		if len(oldChain) > 63 {
			logFn = log.Warn
		}
		logFn("Chain split detected", "number", commonBlock.Number(), "hash", commonBlock.Hash(),
			"drop", len(oldChain), "dropfrom", oldChain[0].Hash(), "add", len(newChain), "addfrom", newChain[0].Hash())
	} else {
		log.Error("Impossible reorg, please file an issue", "oldnum", oldBlock.Number(), "oldhash", oldBlock.Hash(), "newnum", newBlock.Number(), "newhash", newBlock.Hash())
	}
	// Insert the new chain, taking care of the proper incremental order
	var addedTxs types.Transactions
	for i := len(newChain) - 1; i >= 0; i-- {
		// insert the block in the canonical way, re-writing history
		dm.insert(newChain[i])
		// write lookup entries for hash based transaction/receipt searches
		//rawdb.WriteTxLookupEntries(dm.db, newChain[i])
		rawdb.WriteTxLookupEntries(dm.db, newChain[i].Transactions(), newChain[i].Header())
		addedTxs = append(addedTxs, newChain[i].Transactions()...)
	}
	// calculate the difference between deleted and added transactions
	diff := types.TxDifference(deletedTxs, addedTxs)
	// When transactions get deleted from the database that means the
	// receipts that were created in the fork must also be deleted
	batch := dm.db.NewBatch()
	for _, tx := range diff {
		rawdb.DeleteTxLookupEntry(batch, tx.Hash())
	}
	batch.Write()

	if len(deletedLogs) > 0 {
		go dm.rmLogsFeed.Send(RemovedLogsEvent{deletedLogs})
	}
	if len(oldChain) > 0 {
		go func() {
			for _, block := range oldChain {
				dm.chainSideFeed.Send(ChainSideEvent{Block: block})
			}
		}()
	}

	return nil
}

// PostChainEvents iterates over the events generated by a chain insertion and
// posts them into the event feed.
// TODO: Should not expose PostChainEvents. The chain events should be posted in WriteBlock.
func (dm *DAGManager) PostChainEvents(events []interface{}, logs []*types.Log) {
	// post event logs for further processing
	if logs != nil {
		dm.logsFeed.Send(logs)
	}
	for _, event := range events {
		switch ev := event.(type) {
		case ChainEvent:
			dm.chainFeed.Send(ev)

		case ChainHeadEvent:
			dm.chainHeadFeed.Send(ev)

		case ChainSideEvent:
			dm.chainSideFeed.Send(ev)
		}
	}
}

func (dm *DAGManager) update() {
	futureTimer := time.NewTicker(5 * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			dm.procFutureBlocks()
		case <-dm.quit:
			return
		}
	}
}

// BadBlocks returns a list of the last 'bad blocks' that the client has seen on the network
func (dm *DAGManager) BadBlocks() []*types.Block {
	blocks := make([]*types.Block, 0, dm.badBlocks.Len())
	for _, hash := range dm.badBlocks.Keys() {
		if blk, exist := dm.badBlocks.Peek(hash); exist {
			block := blk.(*types.Block)
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// addBadBlock adds a bad block to the bad-block LRU cache
func (dm *DAGManager) addBadBlock(block *types.Block) {
	dm.badBlocks.Add(block.Hash(), block)
}

// reportBlock logs a bad block error.
func (dm *DAGManager) reportBlock(block *types.Block, receipts types.Receipts, err error) {
	debug.PrintStack()

	dm.addBadBlock(block)

	var receiptString string
	for _, receipt := range receipts {
		receiptString += fmt.Sprintf("\t%v\n", receipt)
	}
	log.Error(fmt.Sprintf(`
########## BAD BLOCK #########
Chain config: %v

Number: %v
Hash: 0x%x
%v

Error: %v
##############################
`, dm.chainConfig, block.Number(), block.Hash(), receiptString, err))
}

// InsertHeaderChain attempts to insert the given header chain in to the local
// chain, possibly creating a reorg. If an error is returned, it will return the
// index number of the failing header as well an error describing what went wrong.
//
// The verify parameter can be used to fine tune whether nonce verification
// should be done or not. The reason behind the optional check is because some
// of the header retrieval mechanisms already need to verify nonces, as well as
// because nonces can be verified sparsely, not needing to check each.
func (dm *DAGManager) InsertHeaderChain(chain []*types.Header, checkFreq int) (int, error) {
	start := time.Now()
	if i, err := dm.hc.ValidateHeaderChain(chain, checkFreq); err != nil {
		return i, err
	}

	// Make sure only one thread manipulates the chain at once
	dm.chainmu.Lock()
	defer dm.chainmu.Unlock()

	dm.wg.Add(1)
	defer dm.wg.Done()

	whFunc := func(header *types.Header) error {
		dm.mu.Lock()
		defer dm.mu.Unlock()

		_, err := dm.hc.WriteHeader(header)
		return err
	}

	return dm.hc.InsertHeaderChain(chain, whFunc, start)
}

// writeHeader writes a header into the local chain, given that its parent is
// already known. If the total difficulty of the newly inserted header becomes
// greater than the current known TD, the canonical chain is re-routed.
//
// Note: This method is not concurrent-safe with inserting blocks simultaneously
// into the chain, as side effects caused by reorganisations cannot be emulated
// without the real blocks. Hence, writing headers directly should only be done
// in two scenarios: pure-header mode of operation (light clients), or properly
// separated header/block phases (non-archive clients).
func (dm *DAGManager) writeHeader(header *types.Header) error {
	dm.wg.Add(1)
	defer dm.wg.Done()

	dm.mu.Lock()
	defer dm.mu.Unlock()

	_, err := dm.hc.WriteHeader(header)
	return err
}

// GetTd retrieves a block's total difficulty in the canonical chain from the
// database by hash and number, caching it if found.
func (dm *DAGManager) GetTd(hash common.Hash, number uint64) *big.Int {
	return dm.hc.GetTd(hash, number)
}

// GetTdByHash retrieves a block's total difficulty in the canonical chain from the
// database by hash, caching it if found.
func (dm *DAGManager) GetTdByHash(hash common.Hash) *big.Int {
	return dm.hc.GetTdByHash(hash)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (dm *DAGManager) GetHeader(hash common.Hash, number uint64) *types.Header {
	return dm.hc.GetHeader(hash, number)
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (dm *DAGManager) GetHeaderByHash(hash common.Hash) *types.Header {
	return dm.hc.GetHeaderByHash(hash)
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (dm *DAGManager) HasHeader(hash common.Hash, number uint64) bool {
	return dm.hc.HasHeader(hash, number)
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (dm *DAGManager) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	return dm.hc.GetBlockHashesFromHash(hash, max)
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (dm *DAGManager) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	dm.chainmu.Lock()
	defer dm.chainmu.Unlock()

	return dm.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (dm *DAGManager) GetHeaderByNumber(number uint64) *types.Header {
	return dm.hc.GetHeaderByNumber(number)
}

// Config retrieves the blockchain's chain configuration.
func (dm *DAGManager) Config() *params.ChainConfig { return dm.chainConfig }

// Engine retrieves the blockchain's consensus engine.
func (dm *DAGManager) Engine() consensus.Engine { return dm.engine }

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (dm *DAGManager) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return dm.scope.Track(dm.rmLogsFeed.Subscribe(ch))
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (dm *DAGManager) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return dm.scope.Track(dm.chainFeed.Subscribe(ch))
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (dm *DAGManager) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return dm.scope.Track(dm.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (dm *DAGManager) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return dm.scope.Track(dm.chainSideFeed.Subscribe(ch))
}

// SubscribeLogsEvent registers a subscription of []*types.Log.
func (dm *DAGManager) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return dm.scope.Track(dm.logsFeed.Subscribe(ch))
}

func (dm *DAGManager) GetPendingBlocks() []interface{} {
	var result []interface{}
	for k1, v1 := range dm.pbm.pendingBlocks {
		var waited []common.Hash
		for k2, _ := range v1.waitedHash {
			waited = append(waited, k2)
		}
		data := &BlockAndWait{k1, waited}
		result = append(result, data)
	}
	return result
}
