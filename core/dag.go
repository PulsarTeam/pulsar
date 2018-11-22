package core

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/consensus"
	"math/big"
	mrand1 "math/rand"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

type DAG struct{
	bc *DAGManager
}

func NewDAG(dm *DAGManager) *DAG {
	return &DAG{dm}
}

func (dag *DAG)IsReorg(blocks []*types.Block)(isReorg bool, oldblocks []*types.Block, err error){
	block := blocks[len(blocks)-1]

	// Calculate the total difficulty of the block
	ptd := dag.bc.GetTd(block.ParentHash(), block.NumberU64()-1)
	if ptd == nil {
		return false, nil, consensus.ErrUnknownAncestor
	}

	currentBlock := dag.bc.CurrentBlock()
	localTd := dag.bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	externTd := new(big.Int).Add(block.Difficulty(), ptd)

	// Irrelevant of the canonical status, write the block itself to the database
	if err := dag.bc.hc.WriteTd(block.Hash(), block.NumberU64(), externTd); err != nil {
		return false, nil, err
	}

	reorg := externTd.Cmp(localTd) > 0
	currentBlock = dag.bc.CurrentBlock()
	if !reorg && externTd.Cmp(localTd) == 0 {
		// Split same-difficulty blocks by number, then at random
		reorg = block.NumberU64() < currentBlock.NumberU64() || (block.NumberU64() == currentBlock.NumberU64() && mrand1.Float64() < 0.5)
	}

	if reorg {
		if block.ParentHash() != currentBlock.Hash() {
			oldblocks = append(oldblocks, currentBlock)
		}else{
			oldblocks = nil
		}
	}

	return reorg, oldblocks, nil
}

func (dag *DAG)InsertBlocks(epocblocks []*types.Block){
	//\\//\\
	block := epocblocks[len(epocblocks)-1]

	// If the block is on a side chain or an unknown one, force other heads onto it too
	updateHeads := rawdb.ReadCanonicalHash(dag.bc.db, block.NumberU64()) != block.Hash()

	// Add the block to the canonical chain number scheme and mark as the head
	rawdb.WriteCanonicalHash(dag.bc.db, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(dag.bc.db, block.Hash())

	dag.bc.currentBlock.Store(block)

	// If the block is better than our head or is on a different chain, force update heads
	if updateHeads {
		dag.bc.hc.SetCurrentHeader(block.Header())
		rawdb.WriteHeadFastBlockHash(dag.bc.db, block.Hash())

		dag.bc.currentFastBlock.Store(block)
	}

}

func (dag *DAG)GetFutureReferenceBlock()(blocks []*types.Block, err error){
	return nil, nil
}

func (dag *DAG)CurrentBlock()(block *types.Block, err error){
	return nil, nil
}