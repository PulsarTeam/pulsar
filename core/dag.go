package core

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/consensus"
	"math/big"
	"errors"
	"fmt"
)

type DAG struct{
	dm *DAGManager
}

func NewDAG(dm *DAGManager) *DAG {
	return &DAG{dm}
}

func (dag *DAG)GetSplitChain(newHeader, oldHeader *types.Header)(oldPivotChain []*types.Header, newPivotChain []*types.Header, err error) {
	// first reduce whoever is higher bound
	if oldHeader.Number.Uint64() > newHeader.Number.Uint64() {
		// reduce old chain
		for ; oldHeader != nil && oldHeader.Number.Uint64() != newHeader.Number.Uint64(); oldHeader = dag.dm.GetBlock(oldHeader.ParentHash, oldHeader.Number.Uint64()-1).Header() {
			oldPivotChain = append(oldPivotChain, oldHeader)
		}
	} else {
		// reduce new chain and append new chain blocks for inserting later on
		for ; newHeader != nil && newHeader.Number.Uint64() != oldHeader.Number.Uint64(); newHeader = dag.dm.GetBlock(newHeader.ParentHash, newHeader.Number.Uint64()-1).Header() {
			newPivotChain = append(newPivotChain, newHeader)
		}
	}

	if oldHeader == nil {
		return nil, nil, fmt.Errorf("Invalid old pivot chain")
	}
	if newHeader == nil {
		return nil, nil, fmt.Errorf("Invalid new pivot chain")
	}

	for {
		if oldHeader.Hash() == newHeader.Hash() {
			break
		}

		oldPivotChain = append(oldPivotChain, oldHeader)
		newPivotChain = append(newPivotChain, newHeader)

		oldHeader, newHeader = dag.dm.GetBlock(oldHeader.ParentHash, oldHeader.Number.Uint64()-1).Header(), dag.dm.GetBlock(newHeader.ParentHash, newHeader.Number.Uint64()-1).Header()
		if oldHeader == nil {
			return nil, nil, fmt.Errorf("Invalid old pivot chain")
		}
		if newHeader == nil {
			return nil, nil, fmt.Errorf("Invalid new pivot chain")
		}
	}

	return oldPivotChain, newPivotChain, nil
}

func (dag *DAG)IsReorg(epochHeaders []*types.Header)(isReorg bool, oldPivotChain []*types.Header, newPivotChain []*types.Header, err error){
	//pivot header is the end of the epochHeaders
	header := epochHeaders[len(epochHeaders)-1]

	// Calculate the total difficulty of the block
	ptd := dag.dm.GetTd(header.ParentHash , header.Number.Uint64()-1)
	if ptd == nil {
		return false, nil, nil, consensus.ErrUnknownAncestor
	}

	currentBlock := dag.dm.CurrentBlock()
	localTd := dag.dm.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	externTd := new(big.Int).Add(header.Difficulty, ptd)

	// Irrelevant of the canonical status, write the block itself to the database
	if err := dag.dm.hc.WriteTd(header.Hash(), header.Number.Uint64(), externTd); err != nil {
		return false, nil, nil, err
	}

	reorg := externTd.Cmp(localTd) > 0
	currentBlock = dag.dm.CurrentBlock()
	if !reorg && externTd.Cmp(localTd) == 0 {
		// Split same-difficulty blocks by number, then at random
		reorg = header.Number.Uint64() < currentBlock.NumberU64()
		reorg = reorg || ((header.Number.Uint64() == currentBlock.NumberU64() && (header.Hash().Big().Cmp(currentBlock.Header().Hash().Big()) == -1)))
	}

	if reorg && header.ParentHash != currentBlock.Hash() {
		oldPivotChain, newPivotChain, err = dag.GetSplitChain(header, currentBlock.Header())
		if err != nil{
			return false, nil, nil, errors.New("find split block error")
		}

		for j := 0; j < len(oldPivotChain)/2; j++ {
			oldPivotChain[j], oldPivotChain[len(oldPivotChain)-1-j] = oldPivotChain[len(oldPivotChain)-1-j], oldPivotChain[j]
		}

		for j := 0; j < len(newPivotChain)/2; j++ {
			newPivotChain[j], newPivotChain[len(newPivotChain)-1-j] = newPivotChain[len(newPivotChain)-1-j], newPivotChain[j]
		}

	} else {
		newPivotChain = append(newPivotChain, header)
	}

	return reorg, oldPivotChain, newPivotChain, nil
}

func (dag *DAG)InsertBlocks(epochHeaders []*types.Header){
	//block insert into dag
}

func (dag *DAG)GetFutureReferenceBlock()(blocks []*types.Block, err error){
	return nil, nil
}

func (dag *DAG)CurrentBlock()(block *types.Block, err error){
	return nil, nil
}

func (dag *DAG)GetEpochHeaders(epochNumber uint64)(epochHeaders []*types.Header, err error){
	return nil, nil
}