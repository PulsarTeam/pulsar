package dag

import (
	"testing"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"github.com/ethereum/go-ethereum/common"
	"fmt"
)

func prepareBlock(parentHash common.Hash, number int64, uncles []*types.Header, difficulty int64, gasLimit int64) (*types.Block) {
	var header types.Header = types.Header{
		Number: big.NewInt(number),
		ParentHash: parentHash,
		Difficulty: big.NewInt(difficulty),
		GasLimit: uint64(gasLimit),
	}
	return types.NewBlock(&header, nil, uncles, nil)
}

func prepareBlocks() ([]*types.Block) {

	rootblock := prepareBlock(common.BytesToHash([]byte{0x0}), 0,nil, 10000, 1)
	blocks := make([]*types.Block, 0, 16)

	// epoch 0, genesis
	blocks = append(blocks, rootblock)

	// epoch 1
	premain := prepareBlock(common.BigToHash( big.NewInt( int64(rootblock.Header().GasLimit))), 1,nil, 10000, 2)

	presub1 := rootblock
	presub2 := rootblock
	presub3 := rootblock
	blocks = append(blocks, premain)

	// epoch i
	for i:=2; i<=5; i++ {
		// sub1
		var su []*types.Header = make([]*types.Header, 2, 2)
		su[0] = premain.Header()
		su[1] = presub2.Header()

		//presub1 = prepareBlock(presub1.Header().Hash(), presub1.Number().Int64()+1, nil,10001, int64(i+0x10))
		presub1 = prepareBlock(common.BigToHash( big.NewInt( int64(presub1.Header().GasLimit))), presub1.Number().Int64()+1, su,10001, int64((i-2)+0x11))
		blocks = append(blocks, presub1 )

		// sub2
		//presub2 = prepareBlock(presub2.Header().Hash(), presub2.Number().Int64()+1,nil, 10002, int64(i+0x100))
		presub2 = prepareBlock(common.BigToHash( big.NewInt( int64(presub2.Header().GasLimit))), presub2.Number().Int64()+1,nil, 30002, int64((i-2)+0x101))
		blocks = append(blocks, presub2 )

		// sub3
		//presub3 = prepareBlock(presub3.Header().Hash(), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1000))
		presub3 = prepareBlock(common.BigToHash( big.NewInt( int64(presub3.Header().GasLimit))), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1001))
		blocks = append(blocks, presub3 )
		//presub3 = prepareBlock(presub3.Header().Hash(), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1001))
		presub3 = prepareBlock(common.BigToHash( big.NewInt( int64(presub3.Header().GasLimit))), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1002))
		blocks = append(blocks, presub3 )

		// main
		var uncles []*types.Header = make([]*types.Header, 3, 3)
		uncles[0] = presub1.Header()
		uncles[1] = presub2.Header()
		uncles[2] = presub3.Header()

		//premain = prepareBlock(premain.Header().Hash(), premain.Number().Int64()+1, uncles, 30000, int64(i))
		premain = prepareBlock(common.BigToHash( big.NewInt( int64(premain.Header().GasLimit))), premain.Number().Int64()+1, uncles, 10000, int64(i+1))
		blocks = append(blocks, premain )
	}

	//fmt.Printf("================================================\n")
	//for i, _ :=range blocks {
	//	fmt.Printf("====idx[%d]subNum[%d]=%s, p:%s\n", i, blocks[i].NumberU64(), blocks[i].Hash().String(), blocks[i].ParentHash().String())
	//}

	return blocks
}



func TestInitDAG(t *testing.T) {


	dagCore := NewDAGCore()
	blocks := prepareBlocks()
	dagCore.InitDAG(blocks)
	dagCore.PrintDagBlocks()

	dagCore.PrintTips()
	dagCore.PrintTipEnds()
	dagCore.PrintEpoches()
	hash,_ := dagCore.CurrentBlock()
	fmt.Printf("currentBlock:%s", hash.String() )


}