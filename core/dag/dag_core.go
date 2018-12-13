package dag


import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"github.com/ethereum/go-ethereum/swarm/log"
	"sort"
	"fmt"
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
}

func NewDAGCore() *DAGCore {
	return &DAGCore{
		make(map[common.Hash]*DAGBlock),
		make([]*EpochMeta,0, 16),
		make([]common.Hash,0, 16),
		make([]common.Hash,0, 8),
		common.BytesToHash([]byte{0x0}),
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
func removeIfExist(elem common.Hash, slice *[]common.Hash) bool {
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

func (dag *DAGCore)InitDAG(blocks []*types.Block){

	// add all blocks into the dag block total list and tip list
	for i, block := range blocks {

		//TODO
		//if !dag.IsBlockInDAG(block.Hash()) {
		if !dag.IsBlockInDAG(common.BigToHash( big.NewInt( int64(block.Header().GasLimit)))) {
			dag.insertBlockBaseData(block)
		} else {
			log.Error("InitDAG, when inserting block, but the block already in the DAG!", "hash", block.Hash())
		}

		if i==0 {
			//TODO
			//dag.root = block.Hash()
			dag.root = common.BigToHash( big.NewInt( int64(block.Header().GasLimit)))
		}
	}

	// update the child hash list
	for hash, dagBlock := range dag.dagBlocks {
		if parent, ok:= dag.dagBlocks[dagBlock.ParentHash]; ok {

			parent.ChildHashes = append(dag.dagBlocks[dagBlock.ParentHash].ChildHashes, hash)
			fmt.Printf("InitDAG [%s] add child:[%s]\n", dagBlock.ParentHash.String(), hash.String() )
			fmt.Printf(" ChildHashes.len=%d\n", len(parent.ChildHashes))

		} else {
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
}

func (dag *DAGCore)updateDescendantEpoches(dagBlock *DAGBlock){
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

// before calling, make sure the given dagBlock is in the pivot chain and all its ancestor epoches are available already.
func (dag *DAGCore)updateEpoch(dagBlock *DAGBlock) {

	epoch := EpochMeta{
		dagBlock.Number,
		dagBlock.BlockHash,
		make([]common.Hash, 0, 16),
	}

	if !removeIfExist(dagBlock.BlockHash, &dag.tipBlocks) {
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
	if !removeIfExist(dagBlock.BlockHash, &dag.tipBlocks) {
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

func (dag *DAGCore) isBeforeFence( hash common.Hash, members *[]common.Hash, fence int) bool {
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

func (dag *DAGCore) isTopoAvailable( hash common.Hash) bool {
	if dagBlock, ok:= dag.dagBlocks[hash]; ok {
		// parent
		if !dag.isInPreEpoch(dagBlock.ParentHash) {
			return false
		}
		// references
		for _, memberHash := range dagBlock.ReferHashes {
			if !dag.isInPreEpoch(memberHash) {
				return false
			}
		}
	} else {
		log.Error("isTopoAvailable, block not found!", "hash", hash)
		return false
	}
	return true
}

func (dag *DAGCore) isTopoPrepared( hash common.Hash, members *[]common.Hash, fence int ) bool {

	if dagBlock, ok:= dag.dagBlocks[hash]; ok {

		// parent
		if !dag.isInPreEpoch(dagBlock.ParentHash) &&
			!dag.isBeforeFence(dagBlock.ParentHash, members, fence) {
			return false
		}

		// references
		for _, memberHash := range dagBlock.ReferHashes {
			if !dag.isInPreEpoch(memberHash) &&
				!dag.isBeforeFence(memberHash, members, fence) {
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
	fmt.Printf("topoSortInEpoch, sorted! length:%d, sorted:%d\n", len(*members), fence)
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
func (dag *DAGCore)insertBlockBaseData(block *types.Block) ( *DAGBlock) {


	dagBlock := DAGBlock {
		//block.Header().Hash(),
		common.BigToHash( big.NewInt( int64(block.Header().GasLimit))),

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
		//dagBlock.ReferHashes = append(dagBlock.ReferHashes, refer.Hash())
		dagBlock.ReferHashes = append(dagBlock.ReferHashes, common.BigToHash( big.NewInt( int64(refer.GasLimit))))
	}

	//dag.dagBlocks[block.Hash()] = &dagBlock
	dag.dagBlocks[common.BigToHash( big.NewInt( int64(block.Header().GasLimit)))] = &dagBlock
	//dag.tipBlocks = append(dag.tipBlocks, block.Hash())
	dag.tipBlocks = append(dag.tipBlocks, common.BigToHash( big.NewInt( int64(block.Header().GasLimit))))


	return &dagBlock
}

//block insert into dag
func (dag *DAGCore)InsertBlock(block *types.Block) bool {
	if !dag.CanBlockInsert(block.Hash()) {
		return false
	}
	dagBlock := dag.insertBlockBaseData(block);
	if  dagBlock==nil {
		return false
	}

	if parent, ok:= dag.dagBlocks[dagBlock.ParentHash]; ok {
		parent.ChildHashes = append(parent.ChildHashes, dagBlock.BlockHash)
		pivotUpdateBlock := common.BytesToHash([]byte{0x0})

		//TODO root need to be changed to isolation block
		for parent.BlockHash!=dag.root {
			parent.SubtreeDifficulty = dag.calculateSubtreeDifficulty(parent, false)

			if !dag.isInPivot(parent.BlockHash) {
				dag.updatePivotChild(parent)
				dag.removeEpoch(parent.BlockHash)
			} else {
				pivotUpdateBlock = parent.BlockHash
			}
		}

		// calculate epoches
		if block, ok:= dag.dagBlocks[pivotUpdateBlock]; ok {
			dag.updateDescendantEpoches(block)
		} else {
			log.Error("InsertBlock, pivotUpdateBlock not found!", "hash", pivotUpdateBlock)
		}

	} else {
		log.Error("InsertBlock, parent not found!", "hash", dagBlock.BlockHash, "parentHash", dagBlock.ParentHash)
	}

	return true
}

func (dag *DAGCore)InsertBlocks(epochHeaders []*types.Header){
	//block insert into dag
}

func (dag *DAGCore)GetFutureReferenceBlock()(hashes []common.Hash, err error){
	return dag.tipEndBlocks, nil
}

func (dag *DAGCore)CurrentBlock()(hash common.Hash, err error){
	length := len(dag.epochList)
	return dag.epochList[length-1].BlockHash, nil
	//return nil, nil
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

func (dag *DAGCore)CanBlockInsert(hash common.Hash)(canOrNot bool){
	return !dag.IsBlockInDAG(hash) &&  dag.isTopoAvailable(hash)
}

func (dag *DAGCore)PrintDagBlocks(){
	for hash, v := range dag.dagBlocks {
		fmt.Printf("dagBlocks[%s]:[\n", hash.String())
		//fmt.Print(v)
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