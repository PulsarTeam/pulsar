package dag


import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"github.com/ethereum/go-ethereum/swarm/log"
	"sort"
	"fmt"
	"github.com/pkg/errors"
)

type DAGBlock struct{
	BlockHash		common.Hash
	Number			uint64
	ParentHash		common.Hash
	ReferHashes		[]common.Hash
	Difficulty		*big.Int
	//-----------------------------
	SubtreeDifficulty	*big.Int
	ChildHashes		[]common.Hash
	pivotChild		int
}

type EpochMeta struct {
	EpochNumber		uint64
	BlockHash		common.Hash
	MemberBlockHashes	[]common.Hash
}

type DAGCore struct{
	dagBlocks		map[common.Hash]*DAGBlock
	epochList		[]*EpochMeta
	tipBlocks		[]common.Hash
	tipEndBlocks	[]common.Hash
	root			common.Hash
	ID 				int
}

func NewDAGCore(id int) *DAGCore {
	return &DAGCore{
		make(map[common.Hash]*DAGBlock),
		make([]*EpochMeta,0, 16),
		make([]common.Hash,0, 16),
		make([]common.Hash,0, 8),
		common.BytesToHash([]byte{0x0}),
		id,
	}
}

// if the element exists in the slice, return its index
// if not, return -1
func findIn(elem common.Hash, slice *[]common.Hash) int {
	idx:=-1
	for i, v := range *slice {
		if v==elem {
			idx = i
			break
		}
	}
	return idx
}

// remove element from the slice
// if the element exists, remove it and return true
// if not, return false
func RemoveIfExist(elem common.Hash, slice *[]common.Hash) bool {
	idx:= findIn(elem, slice)
	if idx>=0 {
		*slice = append((*slice)[:idx], (*slice)[idx+1:]...)
		return true
	}
	return false
}

// add element into the slice
// if the element doesn't exist, add it and return true
// if it exists, return false
func addIfNotExist(elem common.Hash, slice *[]common.Hash) bool {
	idx:= findIn(elem, slice)
	if idx==-1 {
		*slice = append(*slice, elem)
		return true
	}
	return false
}

// check the consensus between two dags
func (dag *DAGCore)CheckConsensus(otherDag *DAGCore) error {

	//fmt.Printf("Checking [%d] and [%d] ...\n", dag.ID, otherDag.ID )

	// check roots
	if dag.root!=otherDag.root {
		msg := fmt.Sprintf("The roots are not equal! [%d]root[%s], [%d]root[%s]", dag.ID, dag.root.String(), otherDag.ID, otherDag.root.String())
		fmt.Println(msg)
		return errors.New(msg)
	}

	// check hashes
	for k, _ := range dag.dagBlocks {
		if _, ok := otherDag.dagBlocks[k]; !ok {
			msg := fmt.Sprintf(" The block sets are not equal! block[%s] exists in [%d] but doesn't exist in [%d]!", k.String(), dag.ID, otherDag.ID)
			fmt.Println(msg)
			return errors.New(msg)
		}
	}
	for k, _ := range otherDag.dagBlocks {
		if _, ok := dag.dagBlocks[k]; !ok {
			msg := fmt.Sprintf(" The block sets are not equal! block[%s] doesn't exist in [%d] but exists in [%d]!", k.String(), dag.ID, otherDag.ID)
			fmt.Println(msg)
			return errors.New(msg)
		}
	}

	// check epoch list
	len1 := len(dag.epochList)
	len2 := len(otherDag.epochList)
	if len1!=len2 {
		msg := fmt.Sprintf("The epoch list length are not equal! %d, %d", len1, len2)
		fmt.Println(msg)
		return errors.New(msg)
	}

	for i:=0; i<len1; i++ {
		epoch := dag.epochList[i]
		otherEpoch := otherDag.epochList[i]
		if epoch.BlockHash != otherEpoch.BlockHash || epoch.EpochNumber!=otherEpoch.EpochNumber {
			msg := fmt.Sprintf("EpochList[%d] not equal! [%d]=%d:%s, [%d]=%d:%s", i,
				dag.ID, epoch.EpochNumber, epoch.BlockHash.String(),
				otherDag.ID, otherEpoch.EpochNumber, otherEpoch.BlockHash.String())
			fmt.Println(msg)
			return errors.New(msg)
		}

		mlen1 := len(epoch.MemberBlockHashes)
		mlen2 := len(otherEpoch.MemberBlockHashes)
		if mlen1!=mlen2 {
			msg := fmt.Sprintf("The epochList[%d](%d:%s)'s member length are not equal! %d-%d, %d-%d", i,
				epoch.EpochNumber, epoch.BlockHash.String(), dag.ID, mlen1, otherDag.ID, mlen2)
			fmt.Println(msg)
			return errors.New(msg)
		}
		for j:=0; j<mlen1; j++ {
			if epoch.MemberBlockHashes[j]!=otherEpoch.MemberBlockHashes[j] {
				msg := fmt.Sprintf("The epochList[%d](%d:%s)'s MemberBlockHashes[%d] are not equal! %d-%s, %d-%s", i,
					epoch.EpochNumber, epoch.BlockHash.String(), j, dag.ID, epoch.MemberBlockHashes[j].String(), otherDag.ID, otherEpoch.MemberBlockHashes[j].String())
				fmt.Println(msg)
				return errors.New(msg)
			}
		}
	}

	// check tip list
	tlen1 := len(dag.tipBlocks)
	tlen2 := len(otherDag.tipBlocks)
	if tlen1!=tlen2 {
		msg := fmt.Sprintf("The tip list length are not equal! %d, %d", tlen1, tlen2)
		fmt.Println(msg)
		return errors.New(msg)
	}

	for i:=0; i<tlen1; i++ {
		if dag.tipBlocks[i] != otherDag.tipBlocks[i] {
			msg := fmt.Sprintf("tipBlocks[%d] not equal! %d-%s, %d-%s", i,
				dag.ID, dag.tipBlocks[i].String(), otherDag.ID, otherDag.tipBlocks[i].String() )
			fmt.Println(msg)
			return errors.New(msg)
		}
	}

	// check tip end list
	telen1 := len(dag.tipEndBlocks)
	telen2 := len(otherDag.tipEndBlocks)
	if telen1!=telen2 {
		msg := fmt.Sprintf("The tip end list length are not equal! %d, %d", telen1, telen2)
		fmt.Println(msg)
		return errors.New(msg)
	}

	for i:=0; i<telen1; i++ {
		if dag.tipEndBlocks[i] != otherDag.tipEndBlocks[i] {
			msg := fmt.Sprintf("tipEndBlocks[%d] not equal! %d-%s, %d-%s", i,
				dag.ID, dag.tipEndBlocks[i].String(), otherDag.ID, otherDag.tipEndBlocks[i].String() )
			fmt.Println(msg)
			return errors.New(msg)
		}
	}

	return nil

}

func (dag *DAGCore)InitDAG(blocks []*types.Block) error {

	// add all blocks into the dag block total list and tip list
	for i, block := range blocks {

		if !dag.IsBlockInDAG(block.Hash()) {
		// SimpleHashTest
		//if !dag.IsBlockInDAG(common.BigToHash( big.NewInt( int64(block.Header().GasLimit)))) {
			dag.insertBlockBaseData(block)
		} else {
			return errors.Errorf("InitDAG, when inserting block, but the block already in the DAG! hash:%s", block.Hash().String())
		}

		if i==0 {
			dag.root = block.Hash()
			// SimpleHashTest
			//dag.root = common.BigToHash( big.NewInt( int64(block.Header().GasLimit)))
		}
	}

	// update the child hash list
	for hash, dagBlock := range dag.dagBlocks {
		if parent, ok:= dag.dagBlocks[dagBlock.ParentHash]; ok {
			parent.ChildHashes = append(dag.dagBlocks[dagBlock.ParentHash].ChildHashes, hash)
			//fmt.Printf("InitDAG [%s] add child:[%s]\n", dagBlock.ParentHash.String(), hash.String() )
			//fmt.Printf(" ChildHashes.len=%d\n", len(parent.ChildHashes))
		} else if hash!=dag.root {
			log.Error("InitDAG, parent not found!", "hash", dagBlock.BlockHash, "parentHash", dagBlock.ParentHash)
			fmt.Printf("InitDAG [%s]'s parent [%s] not found!\n", hash.String(), dagBlock.ParentHash.String() )
		}
	}

	// update the subtree difficulty of all dagblocks
	for _, dagBlock := range dag.dagBlocks {
		dagBlock.SubtreeDifficulty = dag.calculateSubtreeDifficulty(dagBlock, true )

	}

	// calculate the pivot chain
	for _, dagBlock := range dag.dagBlocks {
		dag.updatePivotChild(dagBlock)

	}

	// calculate epoches
	if block, ok:= dag.dagBlocks[dag.root]; ok {
		dag.updateDescendantEpoches(block)
	} else {
		log.Error("InitDAG, root not found!", "hash", dag.root)
	}

	// sort the tip list
	dag.topoSortInEpoch(&dag.tipBlocks)

	// gather the tip-end list
	dag.gatherTipEndList()
	return nil
}

func (dag *DAGCore)updateDescendantEpoches(dagBlock *DAGBlock, ){
	block := dagBlock
	ok := false
	for {
		dag.updateEpoch(block)
		if block.pivotChild < 0 {
			// normal end
			break
		}
		length := len(block.ChildHashes)
		if block.pivotChild >= length {
			// error end
			log.Error("updateDescendantEpoches, calculate epoches, pivotChild is out of range !", "hash", block.BlockHash, "number", block.Number, "pivotChild", block.pivotChild, "childHashes length", length)
			break
		}
		hash := block.ChildHashes[block.pivotChild]

		if block, ok = dag.dagBlocks[hash]; !ok {
			log.Error("updateDescendantEpoches, block not found!", "hash", hash)
			break
		}
	}
}

func (dag *DAGCore)clearDescendantEpoches(epochHash common.Hash){

	epoch, _ :=dag.GetEpochByHash(epochHash)
	if epoch==nil {
		log.Error("clearDescendantEpoches, epoch not found!", "hash", epochHash)
		return
	}

	length := len(dag.epochList)
	for i:=length-1; i>=0 && dag.epochList[i].BlockHash!=epochHash; i--{
		dag.removeEpoch(dag.epochList[i].BlockHash)
	}
}

// before calling, make sure the given dagBlock is in the pivot chain and all its ancestor epoches are available already.
func (dag *DAGCore)updateEpoch(dagBlock *DAGBlock) {

	epoch := EpochMeta{
		dagBlock.Number,
		dagBlock.BlockHash,
		make([]common.Hash, 0, 16),
	}

	if !RemoveIfExist(dagBlock.BlockHash, &dag.tipBlocks) {
		log.Error("updateEpoch, remove from tipBlocks but not found!", "hash", dagBlock.BlockHash)
	}

	// add references
	for i, hash := range dagBlock.ReferHashes {
		if referBlock, ok:= dag.dagBlocks[hash]; ok {
			dag.addIntoEpoch(referBlock, &epoch.MemberBlockHashes)
		} else {
			log.Error("updateEpoch, reference not found!", "hash", dagBlock.BlockHash, "referHash", hash, "i",i)
		}
	}

	dag.topoSortInEpoch(&epoch.MemberBlockHashes)
	dag.epochList = append(	dag.epochList, &epoch)
}

// remove the epoch and all its descendants defined by the given pivot block
func (dag *DAGCore)removeEpoch(pivotBlockHash common.Hash) bool {
	idx := -1
	for i, v := range dag.epochList {
		if v.BlockHash==pivotBlockHash {
			idx = i
		}
	}
	if idx==-1 {
		log.Error("removeEpoch, pivotBlockHash not found!", "hash", pivotBlockHash)
		return false
	}

	for i:=len(dag.epochList)-1; i>=idx; i-- {
		addIfNotExist(dag.epochList[i].BlockHash, &dag.tipBlocks)
		for _, v := range dag.epochList[i].MemberBlockHashes {
			addIfNotExist(v, &dag.tipBlocks)
		}
		dag.epochList = dag.epochList[:i]
	}
	return true
}


// add the given dagBlock and all its ancestors and refer-ancestors into the lastest epoch
func (dag *DAGCore)addIntoEpoch(dagBlock *DAGBlock, members *[]common.Hash)  {

	if dag.isInPreEpoch(dagBlock.BlockHash) {
		return
	}
	for _, memberHash := range *members {
		if memberHash==dagBlock.BlockHash{
			return
		}
	}

	(*members) = append(*members, dagBlock.BlockHash)
	if !RemoveIfExist(dagBlock.BlockHash, &dag.tipBlocks) {
		log.Error("addIntoEpoch, remove from tipBlocks but not found!", "hash", dagBlock.BlockHash)
	}

	// add parent
	if parent, ok:= dag.dagBlocks[dagBlock.ParentHash]; ok {
		dag.addIntoEpoch(parent, members)
	} else {
		log.Error("addIntoEpoch, parent not found!", "hash", dagBlock.BlockHash, "parentHash", parent)
	}

	// add references (if need)
	for i, hash := range dagBlock.ReferHashes {
		if referBlock, ok:= dag.dagBlocks[hash]; ok {
			dag.addIntoEpoch(referBlock, members)
		} else {
			log.Error("addIntoEpoch, reference not found!", "hash", dagBlock.BlockHash, "referHash", hash, "i",i)
		}
	}
}

func (dag *DAGCore) isInPreEpoch( hash common.Hash) bool {
	for _,epoch := range dag.epochList {
		if hash==epoch.BlockHash {
			return true
		}
		for _,memberHash := range epoch.MemberBlockHashes {
			if memberHash==hash{
				return true
			}
		}
	}
	return false
}

func IsBeforeFence( hash common.Hash, members *[]common.Hash, fence int) bool {
	for i:=0; i<fence; i++ {
		if (*members)[i]==hash{
			return true
		}
	}
	return false
}

func (dag *DAGCore) isInTipBlocks( hash common.Hash) bool {
	return findIn(hash, &dag.tipBlocks)>=0
}

// judge whether the block's parent and all its references are in the previous epoches.
func (dag *DAGCore) isInsertPrepared(dagBlock *DAGBlock) bool {

		// parent
		if !dag.IsBlockInDAG(dagBlock.ParentHash) {

			return false
		}

		// references
		for _, uncle := range dagBlock.ReferHashes {

			if !dag.IsBlockInDAG(uncle) {

				return false
			}
		}

	return true
}

func (dag *DAGCore) isTopoPrepared( hash common.Hash, members *[]common.Hash, fence int ) bool {

	if dagBlock, ok:= dag.dagBlocks[hash]; ok {

		// parent
		if !dag.isInPreEpoch(dagBlock.ParentHash) &&
			!IsBeforeFence(dagBlock.ParentHash, members, fence) {
			return false
		}

		// references
		for _, memberHash := range dagBlock.ReferHashes {
			if !dag.isInPreEpoch(memberHash) &&
				!IsBeforeFence(memberHash, members, fence) {
				return false
			}
		}

	} else {
		log.Error("isTopoPrepared, block not found!", "hash", hash)
		return false
	}
	return true
}



func (dag *DAGCore) isInPivot( hash common.Hash) bool {
	for _, v := range dag.epochList {
		if v.BlockHash==hash {
			return true
		}
	}
	return false
}

type HashSlice []common.Hash

func(s HashSlice) Len() int {
	return len(s)
}

func(s HashSlice) Less(i, j int) bool {
	return s[i].Big().Cmp( s[j].Big())<0
}

func(s HashSlice) Swap(i, j int){
	s[i], s[j] = s[j], s[i]
}

func (dag *DAGCore)topoSortInEpoch( members *[]common.Hash)  {

	// firstly sort by hash
	sort.Sort(HashSlice(*members))

	// topology sort
	fence := 0
	for i:=fence; i<len(*members); i++ {
		if dag.isTopoPrepared( (*members)[i], members, fence) {
			for j:=i; j>fence; j-- {
				(*members)[j-1],(*members)[j] = (*members)[j],(*members)[j-1]
			}
			//fmt.Printf("topoSortInEpoch, (%s) :%d ---> %d\n", (*members)[fence].String(), fence, fence+1)
			i=fence
			fence++
			continue
		}
	}

	if fence!=len(*members) {
		log.Error("topoSortInEpoch, not all elememts sorted!", "length", len(*members), "sorted", fence)
		fmt.Printf("topoSortInEpoch, not all elememts sorted! length:%d, sorted:%d\n", len(*members), fence)
	}
	//fmt.Printf("topoSortInEpoch, sorted! length:%d, sorted:%d\n", len(*members), fence)
}

func (dag *DAGCore)gatherTipEndList(){
	for _, hash := range dag.tipBlocks {
		if dagBlock, ok:= dag.dagBlocks[hash]; ok {
			if dagBlock.pivotChild<0 {
				dag.tipEndBlocks = append(dag.tipEndBlocks, hash )
			}
		} else {
			log.Error("gatherTipEndList, dagBlock not found!", "hash", hash)
			fmt.Printf("gatherTipEndList, [%s] not found!\n", hash.String() )
		}
	}
}


func (dag *DAGCore)updatePivotChild(dagBlock *DAGBlock){
	max := big.NewInt(0)
	for i, childHash := range dagBlock.ChildHashes {
		if child, ok:= dag.dagBlocks[childHash]; ok {
			//fmt.Printf("%d||updatePivotChild,[%s]-(%d)[%s], diff(%s/%s)  max:%s\n", dag.ID, dagBlock.BlockHash.String(), i, childHash.String(), child.Difficulty.String(), child.SubtreeDifficulty.String(), max.String() )
			if dagBlock.pivotChild ==-1 || child.SubtreeDifficulty.Cmp(max)>0 ||
			(child.SubtreeDifficulty.Cmp(max)==0 && childHash.Big().Cmp(dagBlock.ChildHashes[dagBlock.pivotChild].Big())<0 )  {
				max = child.SubtreeDifficulty
				dagBlock.pivotChild = i
			}
		} else {
			log.Error("updatePivotChild, child not found!", "hash", dagBlock.BlockHash, "childHash", childHash, "i",i)
		}
	}
}


// recursively calculate the subtree difficulty of the given dagblock
func (dag *DAGCore)calculateSubtreeDifficulty(dagBlock *DAGBlock, recalculateChildren bool)(*big.Int){
	if len(dagBlock.ChildHashes) == 0 {
		return dagBlock.Difficulty
	} else {
		subtreeDifficulty :=  new(big.Int).Set(dagBlock.Difficulty)
		for i, childHash := range dagBlock.ChildHashes {
			if child, ok:= dag.dagBlocks[childHash]; ok {
				if recalculateChildren {
					subtreeDifficulty.Add(subtreeDifficulty, dag.calculateSubtreeDifficulty(child, true))
				} else {
					subtreeDifficulty.Add(subtreeDifficulty, child.SubtreeDifficulty)
				}
			} else {
				log.Error("CalculateSubtreeDifficulty, child not found!", "hash", dagBlock.BlockHash, "childHash", childHash, "index",i)
			}
		}
		return subtreeDifficulty
	}
}

//block insert into dag
func (dag *DAGCore)generateDAGBlock(block *types.Block) ( *DAGBlock) {

	dagBlock := DAGBlock {
		block.Header().Hash(),
		//SimpleHashTest
		//common.BigToHash( big.NewInt( int64(block.Header().GasLimit))),

		block.NumberU64(),
		block.ParentHash(),
		make([]common.Hash, 0, len(block.Uncles())),
		block.Difficulty(),
		block.Difficulty(),
		make([]common.Hash, 0),
		-1,
	}
	// update the reference hashes
	for _, refer := range block.Uncles() {

		dagBlock.ReferHashes = append(dagBlock.ReferHashes, refer.Hash())
		//SimpleHashTest
		//dagBlock.ReferHashes = append(dagBlock.ReferHashes, common.BigToHash( big.NewInt( int64(refer.GasLimit))))
	}

	return &dagBlock
}

// clone a DAGBlock object
func (dagBlock *DAGBlock)Clone() ( *DAGBlock) {

	clonedBlock := DAGBlock {
		dagBlock.BlockHash,
		dagBlock.Number,
		dagBlock.ParentHash,
		make([]common.Hash, len(dagBlock.ReferHashes)),
		new(big.Int).Set(dagBlock.Difficulty),
		new(big.Int).Set(dagBlock.SubtreeDifficulty),
		make([]common.Hash, len(dagBlock.ChildHashes)),
		dagBlock.pivotChild,
	}
	// update the reference hashes
	for i, _ := range dagBlock.ReferHashes {
		clonedBlock.ReferHashes[i] = dagBlock.ReferHashes[i]
	}

	// update the child hashes
	for i, _ := range dagBlock.ChildHashes {
		clonedBlock.ChildHashes[i] = dagBlock.ChildHashes[i]
	}

	return &clonedBlock
}

func (dag *DAGCore)generateDAGBlockByParams(hash common.Hash, parentHash common.Hash, number uint64, uncles []common.Hash, difficulty int64) ( *DAGBlock) {

	dagBlock := DAGBlock {
		hash,
		number,
		parentHash,
		make([]common.Hash, 0, len(uncles)),
		big.NewInt((difficulty)),
		big.NewInt((difficulty)),
		make([]common.Hash, 0),
		-1,
	}
	// update the reference hashes
	for _, refer := range uncles {
		dagBlock.ReferHashes = append(dagBlock.ReferHashes, refer)
	}

	return &dagBlock
}

//block insert into dag
func (dag *DAGCore)insertBlockBaseData(block *types.Block) ( *DAGBlock) {

	dagBlock := dag.generateDAGBlock(block)

	if dag.insertDAGBlock(dagBlock) {
		return dagBlock
	}

	return nil
}

//dagBlock insert into dag
// return true if success, false if not
func (dag *DAGCore)insertDAGBlock(dagBlock *DAGBlock) (bool) {

	//fmt.Printf("%d||insert: %s\n" , dag.ID, dagBlock.BlockHash.String())
	if _, ok := dag.dagBlocks[dagBlock.BlockHash]; !ok {
		dag.dagBlocks[dagBlock.BlockHash] = dagBlock
		dag.tipBlocks = append(dag.tipBlocks, dagBlock.BlockHash)
		return true
	}
	return false
}

//block insert into dag
func (dag *DAGCore)InsertBlock(block *types.Block) bool {

	refers := make([]common.Hash, 0, 0)
	// update the reference hashes
	for _, refer := range block.Uncles() {
		refers = append(refers, refer.Hash())
	}

	dagBlock := dag.generateDAGBlockByParams(block.Hash(), block.ParentHash(), block.NumberU64(), refers, 10000 )
	return dag.InsertDAGBlock(dagBlock)

}

//block insert into dag
func (dag *DAGCore)InsertDAGBlock(dagBlock *DAGBlock) bool {
	if !dag.CanBlockInsert(dagBlock) {
		return false
	}
	clonedBlock := dagBlock.Clone()
	success := dag.insertDAGBlock(clonedBlock);
	if  !success {
		return false
	}
	if parent, ok:= dag.dagBlocks[clonedBlock.ParentHash]; ok {

		//fmt.Printf("InsertBlock, hash:%s, parentHash:%s, childs=%d {", clonedBlock.BlockHash.String(), clonedBlock.ParentHash.String(), len(parent.ChildHashes), )
		//for _, v := range parent.ChildHashes {
		//	fmt.Println(v.String())
		//}
		//fmt.Println("}")
		parent.ChildHashes = append(parent.ChildHashes, clonedBlock.BlockHash)
		var pivotUpdateBlock *DAGBlock = nil

		blockToProcess := parent

		//TODO root need to be changed to isolation block
		for blockToProcess.BlockHash!=dag.root {
			blockToProcess.SubtreeDifficulty = dag.calculateSubtreeDifficulty(blockToProcess, false)
			if !dag.isInPivot(blockToProcess.BlockHash) {
				dag.updatePivotChild(blockToProcess)
			} else if pivotUpdateBlock==nil {
				dag.updatePivotChild(blockToProcess)
				pivotUpdateBlock = blockToProcess
			}

			if blockToProcess, ok = dag.dagBlocks[blockToProcess.ParentHash]; ok {


			} else {
				log.Error("InsertBlock, parent not found!", "hash", clonedBlock.BlockHash, "parentHash", clonedBlock.ParentHash)
				fmt.Printf("InsertBlock, parent not found! hash:%s, parentHash:%s", clonedBlock.BlockHash.String(), clonedBlock.ParentHash.String() )
			}

		}

		if blockToProcess.BlockHash==dag.root && pivotUpdateBlock==nil {
			dag.updatePivotChild(blockToProcess)
			pivotUpdateBlock = blockToProcess
		}

		dag.clearDescendantEpoches(pivotUpdateBlock.BlockHash)
		//fmt.Printf("pivotUpdateBlock:%s\n", pivotUpdateBlock.BlockHash.String())
		newEpochHash := pivotUpdateBlock.ChildHashes[pivotUpdateBlock.pivotChild]

		// calculate epoches
		if block, ok:= dag.dagBlocks[newEpochHash]; ok {
			//fmt.Printf("newEpochHash==%s\n" , newEpochHash.String())
			dag.updateDescendantEpoches(block)
		} else {
			log.Error("InsertBlock, pivotUpdateBlock not found!", "hash", pivotUpdateBlock)
		}

	} else {
		log.Error("InsertBlock, parent not found!", "hash", clonedBlock.BlockHash, "parentHash", clonedBlock.ParentHash)
		return false
	}

	// sort the tip list
	dag.topoSortInEpoch(&dag.tipBlocks)

	//clear the tip-end list
	dag.tipEndBlocks = dag.tipEndBlocks[0:0]

	// gather the tip-end list
	dag.gatherTipEndList()

	return true
}

func (dag *DAGCore)InsertBlocks(epochBlocks []*types.Block) error{
	//blocks insert into dag
	for _, v := range epochBlocks {
		if !dag.InsertBlock(v) {
			return errors.Errorf("InsertBlocks, block:%s failed!", v.Hash().String())
		}
	}
	return nil
}

func (dag *DAGCore)InsertDagBlocks(epochDagBlocks []*DAGBlock) error{
	//blocks insert into dag
	for _, v := range epochDagBlocks {
		if !dag.InsertDAGBlock(v) {
			return errors.Errorf("InsertDagBlocks:%s, block:failed!", v.BlockHash.String())
		}
	}
	return nil
}

func (dag *DAGCore)GetTipEnds()(tipEnds []*DAGBlock, err error){

	for _, hash := range dag.tipEndBlocks {
		if dagBlock, ok:= dag.dagBlocks[hash]; ok {
			tipEnds = append(tipEnds, dagBlock)
		} else {
			msg := fmt.Sprintf("tipEnd:%s not found\n", hash.String())
			return tipEnds, errors.New(msg)
		}
	}

	return tipEnds, nil
}

func (dag *DAGCore)GetFutureReferenceBlock()(hashes []common.Hash, err error){
	return dag.tipEndBlocks, nil
}

func (dag *DAGCore)CurrentBlock()(hash common.Hash, err error){
	length := len(dag.epochList)
	return dag.epochList[length-1].BlockHash, nil
}

func (dag *DAGCore)GetBlockByHash(hash common.Hash) *DAGBlock{

	if dagBlock, ok:= dag.dagBlocks[hash]; ok {
		return dagBlock
	} else {
		return nil
	}
}

func (dag *DAGCore)GetEpochByNumber(epochNumber uint64)(epoch *EpochMeta, err error){
	for _, v := range dag.epochList {
		if epochNumber==v.EpochNumber {
			return v, nil
		}
	}
	return nil, nil
}

func (dag *DAGCore)GetEpochByHash(epochHash common.Hash)(epoch *EpochMeta, err error){
	for _, v := range dag.epochList {
		if epochHash==v.BlockHash {
			return v, nil
		}
	}
	return nil, nil
}

func (dag *DAGCore)IsBlockInDAG(hash common.Hash)(IsOrNot bool){
	return dag.isInPreEpoch(hash) || dag.isInTipBlocks(hash)
}

// judge whether the block can be inserted into the DAG
func (dag *DAGCore)CanBlockInsert(dagBlock *DAGBlock)(canOrNot bool){
	return !dag.IsBlockInDAG(dagBlock.BlockHash) && dag.isInsertPrepared(dagBlock)
}

func (dag *DAGCore)PrintDagBlocks(){
	for hash, v := range dag.dagBlocks {
		fmt.Printf("dagBlocks[%s]:[\n", hash.String())
		dag.PrintDagBlock(v)
		fmt.Print("]\n")
	}
}

func (dag *DAGCore)PrintDagBlock(dagBlock *DAGBlock){

	fmt.Printf("BlockHash:%s\n", dagBlock.BlockHash.String())
	fmt.Printf("ParentHash:%s\n", dagBlock.ParentHash.String())
	fmt.Printf("Number:%d\n", dagBlock.Number)
	fmt.Printf("Difficulty:%s\n", dagBlock.Difficulty.String())

	fmt.Printf("ReferHashes:[" )
	for _, v := range dagBlock.ReferHashes {
		fmt.Printf("%s\n",v.String())
	}
	fmt.Printf("]" )
	fmt.Printf("\n---------------------\n" )
	fmt.Printf("SubtreeDifficulty:%s\n", dagBlock.SubtreeDifficulty)
	fmt.Printf("ChildHashes:[" )
	for _, v := range dagBlock.ChildHashes {
		fmt.Printf("%s\n",v.String())
	}
	fmt.Printf("]\n" )
	fmt.Printf("pivotChild:%d\n", dagBlock.pivotChild)
}

func (dag *DAGCore)PrintTips(){
	fmt.Printf("tipBlock counts:%d\n", len(dag.tipBlocks))
	for i, v := range dag.tipBlocks {
		fmt.Printf("tipBlock[%d]:%s\n", i, v.String())
	}
}

func (dag *DAGCore)PrintTipEnds(){
	fmt.Printf("tipEndBlock counts:%d\n", len(dag.tipEndBlocks))
	for i, v := range dag.tipEndBlocks {
		fmt.Printf("tipEndBlock[%d]:%s\n", i, v.String())
	}
}

func (dag *DAGCore)PrintEpoches(){
	for _, v := range dag.epochList {
		dag.PrintEpoch(v)
	}
}

func (dag *DAGCore)PrintEpoch( epoch *EpochMeta){
	fmt.Printf("epoch[%d]:%s\n", epoch.EpochNumber, epoch.BlockHash.String())
	for _, v := range epoch.MemberBlockHashes {
		fmt.Printf("\t+%s\n", v.String())
	}
}