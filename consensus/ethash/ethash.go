// Copyright 2017 The go-ethereum Authors
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

// Package ethash implements the ethash proof-of-work consensus engine.
package ethash

import (
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/edsrzf/mmap-go"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/hashicorp/golang-lru/simplelru"
	"github.com/ethereum/go-ethereum/core"
)

var ErrInvalidDumpMagic = errors.New("invalid dump magic")

var (
	// maxUint256 is a big integer representing 2^256-1
	maxUint256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))

	// sharedEthash is a full instance that can be shared between multiple users.
	sharedEthash = New(Config{"", 3, 0, "", 1, 0, ModeNormal})

	// algorithmRevision is the data structure version used for file naming.
	algorithmRevision = 23

	// dumpMagic is a dataset dump header to sanity check a data dump.
	dumpMagic = []uint32{0xbaddcafe, 0xfee1dead}

	initPosWeight = 5000
	posWeightPrecision int64 = 10000
	posWeightMax uint32 = 8000
	posWeightMin uint32 = 2000
)

// isLittleEndian returns whether the local system is running in little or big
// endian byte order.
func isLittleEndian() bool {
	n := uint32(0x01020304)
	return *(*byte)(unsafe.Pointer(&n)) == 0x04
}

// memoryMap tries to memory map a file of uint32s for read only access.
func memoryMap(path string) (*os.File, mmap.MMap, []uint32, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return nil, nil, nil, err
	}
	mem, buffer, err := memoryMapFile(file, false)
	if err != nil {
		file.Close()
		return nil, nil, nil, err
	}
	for i, magic := range dumpMagic {
		if buffer[i] != magic {
			mem.Unmap()
			file.Close()
			return nil, nil, nil, ErrInvalidDumpMagic
		}
	}
	return file, mem, buffer[len(dumpMagic):], err
}

// memoryMapFile tries to memory map an already opened file descriptor.
func memoryMapFile(file *os.File, write bool) (mmap.MMap, []uint32, error) {
	// Try to memory map the file
	flag := mmap.RDONLY
	if write {
		flag = mmap.RDWR
	}
	mem, err := mmap.Map(file, flag, 0)
	if err != nil {
		return nil, nil, err
	}
	// Yay, we managed to memory map the file, here be dragons
	header := *(*reflect.SliceHeader)(unsafe.Pointer(&mem))
	header.Len /= 4
	header.Cap /= 4

	return mem, *(*[]uint32)(unsafe.Pointer(&header)), nil
}

// memoryMapAndGenerate tries to memory map a temporary file of uint32s for write
// access, fill it with the data from a generator and then move it into the final
// path requested.
func memoryMapAndGenerate(path string, size uint64, generator func(buffer []uint32)) (*os.File, mmap.MMap, []uint32, error) {
	// Ensure the data folder exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, nil, err
	}
	// Create a huge temporary empty file to fill with data
	temp := path + "." + strconv.Itoa(rand.Int())

	dump, err := os.Create(temp)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = dump.Truncate(int64(len(dumpMagic))*4 + int64(size)); err != nil {
		return nil, nil, nil, err
	}
	// Memory map the file for writing and fill it with the generator
	mem, buffer, err := memoryMapFile(dump, true)
	if err != nil {
		dump.Close()
		return nil, nil, nil, err
	}
	copy(buffer, dumpMagic)

	data := buffer[len(dumpMagic):]
	generator(data)

	if err := mem.Unmap(); err != nil {
		return nil, nil, nil, err
	}
	if err := dump.Close(); err != nil {
		return nil, nil, nil, err
	}
	if err := os.Rename(temp, path); err != nil {
		return nil, nil, nil, err
	}
	return memoryMap(path)
}

// lru tracks caches or datasets by their last use time, keeping at most N of them.
type lru struct {
	what string
	new  func(epoch uint64) interface{}
	mu   sync.Mutex
	// Items are kept in a LRU cache, but there is a special case:
	// We always keep an item for (highest seen epoch) + 1 as the 'future item'.
	cache      *simplelru.LRU
	future     uint64
	futureItem interface{}
}

// newlru create a new least-recently-used cache for either the verification caches
// or the mining datasets.
func newlru(what string, maxItems int, new func(epoch uint64) interface{}) *lru {
	if maxItems <= 0 {
		maxItems = 1
	}
	cache, _ := simplelru.NewLRU(maxItems, func(key, value interface{}) {
		log.Trace("Evicted ethash "+what, "epoch", key)
	})
	return &lru{what: what, new: new, cache: cache}
}

// cache wraps an ethash cache with some metadata to allow easier concurrent use.
type cache struct {
	epoch uint64    // Epoch for which this cache is relevant
	dump  *os.File  // File descriptor of the memory mapped cache
	mmap  mmap.MMap // Memory map itself to unmap before releasing
	cache []uint32  // The actual cache data content (may be memory mapped)
	once  sync.Once // Ensures the cache is generated only once
}

// newCache creates a new ethash verification cache and returns it as a plain Go
// interface to be usable in an LRU cache.
func newCache(epoch uint64) interface{} {
	return &cache{epoch: epoch}
}

// finalizer unmaps the memory and closes the file.
func (c *cache) finalizer() {
	if c.mmap != nil {
		c.mmap.Unmap()
		c.dump.Close()
		c.mmap, c.dump = nil, nil
	}
}

// Mode defines the type and amount of PoW verification an ethash engine makes.
type Mode uint

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

// Config are the configuration parameters of the ethash.
type Config struct {
	CacheDir       string
	CachesInMem    int
	CachesOnDisk   int
	DatasetDir     string
	DatasetsInMem  int
	DatasetsOnDisk int
	PowMode        Mode
}

// Ethash is a consensus engine based on proof-of-work implementing the ethash
// algorithm.
type Ethash struct {
	config Config

	// Mining related fields
	rand     *rand.Rand    // Properly seeded random source for nonces
	threads  int           // Number of threads to mine on if mining
	update   chan struct{} // Notification channel to update mining parameters
	hashrate metrics.Meter // Meter tracking the average hashrate

	// The fields below are hooks for testing
	shared    *Ethash       // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	//mining params
	powTargetTimespan int64
	minDifficulty int64 // The minimum of difficulty. It's also the maximum of delegate miner count, in order to avoid target overflow.
	powTargetSpacing int64

	lock sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
}

const(
	TesterThreads = 1
	//\\PowTargetTimespan = 14 * 24 * 60 * 60
	//\\PowTargetTimespan = 75
	PowTargetSpacing = 5
	MinDifficulty = 131072
)

var PowTargetTimespan int64 = core.BlocksInMatureCycle() * PowTargetSpacing

// New creates a full sized ethash PoW scheme.
func New(config Config) *Ethash {
	if config.CachesInMem <= 0 {
		log.Warn("One ethash cache must always be in memory", "requested", config.CachesInMem)
		config.CachesInMem = 1
	}
	if config.CacheDir != "" && config.CachesOnDisk > 0 {
		log.Info("Disk storage enabled for ethash caches", "dir", config.CacheDir, "count", config.CachesOnDisk)
	}
	if config.DatasetDir != "" && config.DatasetsOnDisk > 0 {
		log.Info("Disk storage enabled for ethash DAGs", "dir", config.DatasetDir, "count", config.DatasetsOnDisk)
	}
	return &Ethash{
		config:   config,
		update:   make(chan struct{}),
		hashrate: metrics.NewMeter(),
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewTester creates a small sized ethash PoW scheme useful only for testing
// purposes.
func NewTester() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeTest,
		},
		threads: TesterThreads,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewFaker creates a ethash consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Ethereum
// consensus rules.
func NewFaker() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,

		},
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewFakeFailer creates a ethash consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Ethereum consensus rules.
func NewFakeFailer(fail uint64) *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,
		},
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
		fakeFail: fail,
	}
}

// NewFakeDelayer creates a ethash consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Ethereum consensus rules.
func NewFakeDelayer(delay time.Duration) *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFake,
		},
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
		fakeDelay: delay,
	}
}

// NewFullFaker creates an ethash consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Ethash {
	return &Ethash{
		config: Config{
			PowMode: ModeFullFake,
		},
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// NewShared creates a full sized ethash PoW shared between all requesters running
// in the same process.
func NewShared() *Ethash {
//	return &Ethash{shared: sharedEthash}
	return &Ethash{
		shared: sharedEthash,
		threads: 1,
		powTargetTimespan: PowTargetTimespan,
		powTargetSpacing: PowTargetSpacing,
		minDifficulty: MinDifficulty,
	}
}

// calculate the pos target.
func (ethash *Ethash) CalcTarget(chain consensus.ChainReader, header *types.Header, headers []*types.Header) *big.Int {

	if header.Difficulty.Int64() < ethash.minDifficulty {
		panic( fmt.Sprintf("The header difficulty(%d) is less than minDifficulty(%d), header number=%d", header.Difficulty.Int64() , ethash.minDifficulty, header.Number.Int64() ))
	}

	// calc the target
	target := new(big.Int).Div(maxUint256, header.Difficulty)
	//powWeight := posWeightPrecision - int64(header.PosWeight)
	posTargetAvg := new(big.Int).Div(target, big.NewInt(posWeightPrecision))
	posTargetAvg.Mul(posTargetAvg, big.NewInt(int64(header.PosWeight)))
	powTarget := new(big.Int).Sub( target, posTargetAvg)

	//powTarget := new(big.Int).Mul(target, big.NewInt(powWeight))
	matureState := core.GetMatureState(chain, header.Number.Uint64(), headers)
	if matureState == nil || matureState.DelegateMinersCount() == 0 || matureState.DepositBalanceSum().Sign() == 0 {
		return powTarget
	}

	// POS
	_, localSum, _:= matureState.GetDelegateMiner(header.Coinbase)
	if localSum == nil || localSum.Sign() == 0 {
		return powTarget
	}

	// notice that the posTargetLocal = posTargetAvg*dmCounts * (posLocalSum/posNetworkSum)

	// for a valid difficulty(>=ethash.minDifficulty), the target*difficulty< MaxBigInt,
	// so for a delegate miner count (dmCounts)<minDifficulty, the posTargetAvg*dmCounts<MaxBigInt.
	// i.e. the max delegate miner count(dmCountMax)=minDifficulty
	tmp := new(big.Int).Mul(posTargetAvg, big.NewInt(int64(matureState.DelegateMinersCount())))
	tmp.Div(tmp, matureState.DepositBalanceSum())
	posTarget := new(big.Int).Mul(tmp, localSum)
	return new(big.Int).Add(powTarget, posTarget)
}

// returns the pos weight in a certain cycle.
func (ethash *Ethash) PosWeight(chain consensus.ChainReader, header *types.Header, headers []*types.Header) uint32 {
	if header.Number.Uint64() < core.MinMatureBlockNumber() {
		return uint32(initPosWeight)
	}

	powProduction := ethash.GetPowProduction(chain, header, headers)
	posProduction := ethash.GetPosProduction(chain, header, headers)
	t := big.NewInt(0)
	if powProduction.Cmp(t) == 0 && posProduction.Cmp(t) == 0 {
		return uint32(initPosWeight)
	}

	x := new(big.Int).Mul(powProduction, big.NewInt(int64(posWeightPrecision)))
	weight := new(big.Int).Div(x, new(big.Int).Add(powProduction, posProduction))
	weight32u := uint32(weight.Uint64())
	if weight32u > posWeightMax {
		weight32u = posWeightMax
	} else if weight32u < posWeightMin {
		weight32u = posWeightMin
	}
	return weight32u
}

func (ethash *Ethash)FindInHeaders(header *types.Header, headers []*types.Header) bool {
	for _, v := range headers {
		if header.Hash().String() == v.Hash().String() {
			return true
		}
	}
	return false
}

// returns the total pow production in the previous mature cycle.
func (ethash *Ethash) GetPowProduction(chain consensus.ChainReader, header *types.Header, headers []*types.Header) *big.Int {
	sumPow := big.NewInt(0)
	start, end := core.LastMatureCycleRange(header.Number.Uint64())
	for i := start; i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPow.Add(sumPow, h.PowProduction)
		} else if found := ethash.FindInHeaders(header, headers); found {
			sumPow.Add(sumPow, header.PowProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPow
}

// returns the total pos production in the previous mature cycle.
func (ethash *Ethash) GetPosProduction(chain consensus.ChainReader, header *types.Header, headers []*types.Header) *big.Int {
	start, end := core.LastMatureCycleRange(header.Number.Uint64())
	sumPos := big.NewInt(0)
	for i := start; i < end; i++ {
		h:=chain.GetHeaderByNumber(i)
		if h != nil {
			sumPos.Add(sumPos, h.PosProduction)
		} else if found := ethash.FindInHeaders(header, headers); found {
			sumPos.Add(sumPos, header.PosProduction)
		} else {
			log.Warn("cannot find header.", " header number:", i)
		}
	}
	return sumPos
}

// Threads returns the number of mining threads currently enabled. This doesn't
// necessarily mean that mining is running!
func (ethash *Ethash) Threads() int {
	ethash.lock.Lock()
	defer ethash.lock.Unlock()

	return ethash.threads
}

// SetThreads updates the number of mining threads currently enabled. Calling
// this method does not start mining, only sets the thread count. If zero is
// specified, the miner will use all cores of the machine. Setting a thread
// count below zero is allowed and will cause the miner to idle, without any
// work being done.
func (ethash *Ethash) SetThreads(threads int) {
	ethash.lock.Lock()
	defer ethash.lock.Unlock()

	// If we're running a shared PoW, set the thread count on that instead
	if ethash.shared != nil {
		ethash.shared.SetThreads(threads)
		return
	}
	// Update the threads and ping any running seal to pull in any changes
	ethash.threads = threads
	select {
	case ethash.update <- struct{}{}:
	default:
	}
}

// Hashrate implements PoW, returning the measured rate of the search invocations
// per second over the last minute.
func (ethash *Ethash) Hashrate() float64 {
	return ethash.hashrate.Rate1()
}

// APIs implements consensus.Engine, returning the user facing RPC APIs. Currently
// that is empty.
func (ethash *Ethash) APIs(chain consensus.ChainReader) []rpc.API {
	return nil
}

func (ethash *Ethash)HashimotoforHeader(hash []byte, nonce uint64) ([]byte) {
	return hashimoto(hash, nonce)
}
