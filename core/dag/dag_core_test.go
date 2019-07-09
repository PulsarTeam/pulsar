package dag

import (
	"testing"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"github.com/ethereum/go-ethereum/common"
	"fmt"
	"math/rand"
	"github.com/pkg/errors"
)

func prepareHeader(parentHash common.Hash, number int64, uncles []*types.Header, difficulty int64, gasLimit int64) (*types.Header) {
	return  &types.Header{
		Number: big.NewInt(number),
		ParentHash: parentHash,
		Difficulty: big.NewInt(difficulty),
		GasLimit: uint64(gasLimit),
	}
}

func prepareBlock(parentHash common.Hash, number int64, uncles []*types.Header, difficulty int64, gasLimit int64) (*types.Block) {
	var header types.Header = types.Header{
		Number: big.NewInt(number),
		ParentHash: parentHash,
		Difficulty: big.NewInt(difficulty),
		GasLimit: uint64(gasLimit),
	}
	return types.NewBlock(&header, nil, uncles, nil)
}

// prepare blocks for test.
// for normal test, use real hash.
// for simple hash test, use gasLimit instead.
func prepareBlocks() ([]*types.Block) {

	rootblock := prepareBlock(common.BytesToHash([]byte{0x0}), 0,nil, 10000, 1)
	blocks := make([]*types.Block, 0, 16)

	// epoch 0, genesis
	blocks = append(blocks, rootblock)

	// for simple hash test, use gasLimit instead.

	// epoch 1
	premain := prepareBlock(rootblock.Header().Hash(), rootblock.Number().Int64()+1, nil, 10000, 2)
	//SimpleHashTest
	//premain := prepareBlock(common.BigToHash( big.NewInt( int64(rootblock.Header().GasLimit))), 1,nil, 10000, 2)

	blocks = append(blocks, premain)

	presub1 := rootblock
	presub2 := rootblock
	presub3 := rootblock

	// epoch i
	for i:=2; i<=5; i++ {
		// sub1
		var su []*types.Header = make([]*types.Header, 2, 2)
		su[0] = premain.Header()
		su[1] = presub2.Header()

		presub1 = prepareBlock(presub1.Header().Hash(), presub1.Number().Int64()+1, su,30001, int64(i+0x10))
		//SimpleHashTest
		//presub1 = prepareBlock(common.BigToHash( big.NewInt( int64(presub1.Header().GasLimit))), presub1.Number().Int64()+1, su,30001, int64((i-2)+0x11))
		blocks = append(blocks, presub1 )

		// sub2
		presub2 = prepareBlock(presub2.Header().Hash(), presub2.Number().Int64()+1,nil, 10002, int64(i+0x100))
		//SimpleHashTest
		//presub2 = prepareBlock(common.BigToHash( big.NewInt( int64(presub2.Header().GasLimit))), presub2.Number().Int64()+1,nil, 10002, int64((i-2)+0x101))
		blocks = append(blocks, presub2 )

		// sub3
		presub3 = prepareBlock(presub3.Header().Hash(), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1000))
		//SimpleHashTest
		//presub3 = prepareBlock(common.BigToHash( big.NewInt( int64(presub3.Header().GasLimit))), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1001))
		blocks = append(blocks, presub3 )
		presub3 = prepareBlock(presub3.Header().Hash(), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1001))
		//SimpleHashTest
		//presub3 = prepareBlock(common.BigToHash( big.NewInt( int64(presub3.Header().GasLimit))), presub3.Number().Int64()+1,nil, 10003, int64((i-2)*2+0x1002))
		blocks = append(blocks, presub3 )

		// main
		var uncles []*types.Header = make([]*types.Header, 3, 3)
		uncles[0] = presub1.Header()
		uncles[1] = presub2.Header()
		uncles[2] = presub3.Header()

		premain = prepareBlock(premain.Header().Hash(), premain.Number().Int64()+1, uncles, 10000, int64(i))
		//SimpleHashTest
		//premain = prepareBlock(common.BigToHash( big.NewInt( int64(premain.Header().GasLimit))), premain.Number().Int64()+1, uncles, 10000, int64(i+1))
		blocks = append(blocks, premain )
	}

	return blocks
}


func TestInitDAG(t *testing.T) {

	dagCore := NewDAGCore(1)
	blocks := prepareBlocks()
	dagCore.InitDAG(blocks)
	//dagCore.PrintDagBlocks()
	//
	dagCore.PrintTips()
	dagCore.PrintTipEnds()
	dagCore.PrintEpoches()
	hash,_ := dagCore.CurrentBlock()
	fmt.Printf("currentBlock:%s", hash.String() )

}

func TestMine(t *testing.T) {

	dagCore := NewDAGCore(1)
	blocks := prepareBlocks()
	dagCore.InitDAG(blocks)

	hash,_ := dagCore.CurrentBlock()
	fmt.Printf("currentBlock:%s\n", hash.String() )

	dagCore.PrintTips()
	dagCore.PrintTipEnds()
	dagCore.PrintEpoches()

	parent := dagCore.GetBlockByHash(hash)
	tipEnds ,err := dagCore.GetFutureReferenceBlock()
	if err!=nil {
		fmt.Printf(err.Error())
	}
	ends := tipEnds

	var base int64 = 0x100000

	gasLimit := base+1
	newBlockHash := common.BigToHash( big.NewInt( gasLimit ))
	dagBlock := dagCore.generateDAGBlockByParams(newBlockHash, parent.BlockHash, parent.Number+1, ends, 10000)

	fmt.Println( dagCore.InsertDAGBlock(dagBlock))

	hash2,_ := dagCore.CurrentBlock()
	fmt.Printf("currentBlock:%s\n", hash2.String() )

	dagCore.PrintTips()
	dagCore.PrintTipEnds()
	dagCore.PrintEpoches()

}

func parallelExecute( dagcounts int, dags *[]*DAGCore, minedBlocks *[][] *DAGBlock, fun func(idx int, dags *[]*DAGCore, minedBlocks *[][] *DAGBlock)(error) ) (chan<- struct{}, <-chan error) {

	var (
		done   = make(chan int, dagcounts)
		errors = make([]error, dagcounts)
		abort  = make(chan struct{})
	)

	for i:=0;i< dagcounts;i++ {
		go func(index int) {
			errors[index] = fun(index, dags, minedBlocks )
			done <- index
		}(i)
	}

	errorsOut := make(chan error, dagcounts)
	func() {
		var (
			out = 0
			checked = make([]bool, dagcounts)
		)
		for {
			select {
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == dagcounts-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()

	return abort, errorsOut

}

func TestMultiMine(t *testing.T) {

	const (
		DAGCOUNTS = 10
	)

	// init miners
	var dags []*DAGCore = make([]*DAGCore, DAGCOUNTS, DAGCOUNTS)
	var lens []int = make([]int, DAGCOUNTS, DAGCOUNTS)
	for i:=0; i<DAGCOUNTS; i++ {
		lens[i] = rand.Intn(10)+1
	}
	var minedBlocks [][] *DAGBlock = make([][]*DAGBlock, DAGCOUNTS, DAGCOUNTS)
	for i, _ := range dags {
		minedBlocks[i] = make([]*DAGBlock, 0, 0)
	}

	for i:=0;i< len(dags);i++ {
		// miner i
		dags[i] = NewDAGCore(i + 1)
		blocks := prepareBlocks()
		err := dags[i].InitDAG(blocks)
		hash, _ := dags[i].CurrentBlock()
		fmt.Printf("dag[%d] init: currentBlock:%s\n", i, hash.String())
		if err!=nil {
			t.Errorf("dag[%d] InitDAG failed!", i, err.Error())
		}
	}

	// check consensus
	for i:=0; i<len(dags); i++ {
		for j:=i+1; j< len(dags); j++ {
			if err :=dags[i].CheckConsensus(dags[j]);err!=nil {
				t.Error("=checking consensus after init and before mining=\n",err)
			}
		}
	}
	for round:=0; round<20; round++ {
		var roundBase int64 = int64(0x10000000 * (round + 1))
		//mining separately
		for i, _ := range dags {
			for j := 0; j < lens[i]; j++ {
				hash, _ := dags[i].CurrentBlock()
				parent := dags[i].GetBlockByHash(hash)
				tipEnds, err := dags[i].GetFutureReferenceBlock()
				if err != nil {
					fmt.Printf(err.Error())
				}
				ends := tipEnds
				var base int64 = int64(0x100000 * (i + 1))
				gasLimit := base + roundBase + int64(j+1)
				newBlockHash := common.BigToHash( big.NewInt( gasLimit ))
				dagBlock := dags[i].generateDAGBlockByParams(newBlockHash, parent.BlockHash, parent.Number+1, ends, 10000)
				minedBlocks[i] = append(minedBlocks[i], dagBlock)
				if !dags[i].InsertDAGBlock(dagBlock) {
					t.Errorf("InsertDAGBlock:%s failed!", dagBlock.BlockHash.String())
				}
			}
		}

		//// print dags
		//for i, _ := range dags {
		//	fmt.Printf("1=============dags[%d]begin=================\n", i)
		//	dags[i].PrintTips()
		//	dags[i].PrintTipEnds()
		//	dags[i].PrintEpoches()
		//	fmt.Printf("1=============dags[%d]end=================\n", i)
		//}

		// before broadcast, the dags are centainly not equal
		//
		// check consensus
		//for i:=0; i<len(dags); i++ {
		//	for j:=i+1; j< len(dags); j++ {
		//		if err :=dags[i].CheckConsensus(dags[j]);err!=nil {
		//			t.Error("=checking consensus before broadcast=\n",err)
		//		}
		//	}
		//}

		// boradcast blocks can use either of way 1 or way 2
		way :=2
		if way==1 {
			// broadcast blocks, way 1
			for i, _ := range minedBlocks {
				for j := 0; j < len(minedBlocks[i]); j++ {
					dagBlock := minedBlocks[i][j]
					for k := 0; k < len(dags); k++ {
						if i != k {
							if !dags[k].InsertDAGBlock(dagBlock) {
								t.Errorf("dag[%d], InsertDAGBlock:%s failed!", k, dagBlock.BlockHash.String())
							}
						}
					}
				}
			}
		} else {
			//broadcast blocks, way 2
			for i, _ := range dags {
				for j := 0; j < len(dags); j++ {
					if j != i {

						if err:=dags[i].InsertDagBlocks(minedBlocks[j]); err!=nil {
							t.Error(err)
						}

						//for k := 0; k < len(minedBlocks[j]); k++ {
						//	dagBlock := minedBlocks[j][k]
						//	if !dags[i].InsertDAGBlock(dagBlock) {
						//
						//		t.Errorf("dag[%d], InsertDAGBlock:%s failed!", i, dagBlock.BlockHash.String())
						//	}
						//}
					}
				}
			}
		}
		// print dags
		for i, _ := range dags {
			fmt.Printf("2=============dags[%d]begin=================\n", i)
			dags[i].PrintTips()
			dags[i].PrintTipEnds()
			dags[i].PrintEpoches()
			fmt.Printf("2=============dags[%d]end=================\n", i)
		}

		// check consensus
		for i := 0; i < len(dags); i++ {
			for j := i + 1; j < len(dags); j++ {
				if err := dags[i].CheckConsensus(dags[j]); err != nil {
					t.Error("=checking consensus after broadcast=\n", err)
				}
			}
		}

		// clear the mined blocks in this round
		for i := 0; i < len(minedBlocks); i++ {
			minedBlocks[i] = minedBlocks[i][0:0]
		}
		fmt.Printf("round %d passed!\n", round)
	}
}
//
//
func TestMultiMineCoroutine(t *testing.T) {

	const (
		DAGCOUNTS = 10
	)

	// init miners
	var dags []*DAGCore = make([]*DAGCore, DAGCOUNTS, DAGCOUNTS)
	var lens []int = make([]int, DAGCOUNTS, DAGCOUNTS)
	for i:=0; i<DAGCOUNTS; i++ {
		lens[i] = rand.Intn(10)+1
	}
	var minedBlocks [][] *DAGBlock = make([][]*DAGBlock, DAGCOUNTS, DAGCOUNTS)
	for i, _ := range dags {
		minedBlocks[i] = make([]*DAGBlock, 0, 0)
	}

	abort, results := parallelExecute(DAGCOUNTS, &dags, &minedBlocks,
		func(index int,dags1 *[]*DAGCore, minedBlocks1 *[][] *DAGBlock) (error) {
			(*dags1)[index] = NewDAGCore(index + 1)
			blocks := prepareBlocks()
			err := (*dags1)[index].InitDAG(blocks)
			hash, _ := (*dags1)[index].CurrentBlock()
			fmt.Printf("dag[%d] init: currentBlock:%s\n", index, hash.String())
			if err!=nil {
				errmsg := fmt.Sprintf("dags[%d] InitDAG failed!", index)
				errors.WithMessage(err, errmsg)
			}
			return err
		})
	close(abort)
	err := <-results
	if err !=nil {
		t.Error(err)
	}


	// check consensus
	for i:=0; i<len(dags); i++ {
		for j:=i+1; j< len(dags); j++ {
			if err :=dags[i].CheckConsensus(dags[j]);err!=nil {
				t.Error("=checking consensus after init and before mining=\n",err)
			}
		}
	}

	for round:=0; round<20; round++ {
		var roundBase int64 = int64(0x10000000 * (round + 1))
		// mining separately
		abort1, results1 := parallelExecute(DAGCOUNTS, &dags, &minedBlocks,
			func(index int, dags *[]*DAGCore, minedBlocks *[][] *DAGBlock) (error) {

				for j := 0; j < lens[index]; j++ {
					hash, _ := (*dags)[index].CurrentBlock()
					parent := (*dags)[index].GetBlockByHash(hash)
					tipEnds, err := (*dags)[index].GetFutureReferenceBlock()
					if err != nil {
						fmt.Printf(err.Error())
					}
					ends := tipEnds
					var base int64 = int64(0x100000 * (index + 1))
					gasLimit := base + roundBase + int64(j+1)
					newBlockHash := common.BigToHash( big.NewInt( gasLimit ))
					dagBlock := (*dags)[index].generateDAGBlockByParams(newBlockHash, parent.BlockHash, parent.Number+1, ends, 10000)
					(*minedBlocks)[index] = append((*minedBlocks)[index], dagBlock)
					if !(*dags)[index].InsertDAGBlock(dagBlock) {
						return errors.Errorf("dag[%d], InsertDAGBlock:%s failed!", index, dagBlock.BlockHash.String())
					}

				}
				return nil
			})
		close(abort1)
		err1 := <-results1
		if err1 != nil {
			t.Error(err1)
		}

		//// print dags
		//for i, _ := range dags {
		//	fmt.Printf("1=============dags[%d]begin=================\n", i)
		//	dags[i].PrintTips()
		//	dags[i].PrintTipEnds()
		//	dags[i].PrintEpoches()
		//	fmt.Printf("1=============dags[%d]end=================\n", i)
		//}

		// before broadcast, the dags are centainly not equal
		//
		// check consensus
		//for i:=0; i<len(dags); i++ {
		//	for j:=i+1; j< len(dags); j++ {
		//		if err :=dags[i].CheckConsensus(dags[j]);err!=nil {
		//			t.Error("=checking consensus before broadcast=\n",err)
		//		}
		//	}
		//}

		// broadcast blocks
		abort2, results2 := parallelExecute(DAGCOUNTS, &dags, &minedBlocks,
			func(index int, dags *[]*DAGCore, minedBlocks *[][] *DAGBlock) (error) {
				for j := 0; j < len(*minedBlocks); j++ {
					if j != index {

						if err:=(*dags)[index].InsertDagBlocks((*minedBlocks)[j]); err!=nil {
							return err
						}

						//for k := 0; k < len((*minedBlocks)[j]); k++ {
						//	dagBlock := (*minedBlocks)[j][k]
						//	if !(*dags)[index].InsertDAGBlock(dagBlock) {
						//		return errors.Errorf("dag[%d], InsertDAGBlock:%s failed!", index, dagBlock.BlockHash.String())
						//	}
						//}
					}
				}
				return nil
			})
		close(abort2)
		err2 := <-results2
		if err2 != nil {
			t.Error(err2)
		}

		// print dags
		for i, _ := range dags {
			fmt.Printf("2=============dags[%d]begin=================\n", i)
			dags[i].PrintTips()
			dags[i].PrintTipEnds()
			dags[i].PrintEpoches()
			fmt.Printf("2=============dags[%d]end=================\n", i)
		}

		// check consensus
		for i := 0; i < len(dags); i++ {
			for j := i + 1; j < len(dags); j++ {
				if err := dags[i].CheckConsensus(dags[j]); err != nil {
					t.Error("=checking consensus after broadcast=\n", err)
				}
			}
		}

		// clear the mined blocks in this round
		for i := 0; i < len(minedBlocks); i++ {
			minedBlocks[i] = minedBlocks[i][0:0]
		}
		fmt.Printf("round %d passed!\n", round)
	}
}