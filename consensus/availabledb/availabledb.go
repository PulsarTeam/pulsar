package availabledb

import (
	"errors"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/state"
)

type AvailableDb struct {
	//Delegate available cycle
	DsPowCycle uint64
}

//based Dspowcycle calculate available stateDb
func (availableDb *AvailableDb) GetAvailableDb(chain consensus.ChainReader, header *types.Header) (*state.StateDB, error) {
	cylce := header.Number.Uint64() / availableDb.DsPowCycle
	if(cylce < 2){
		err := errors.New(`no available DelegateData!`)
		return nil,err
	}
	number := (cylce - 1) * availableDb.DsPowCycle
	headAvai := chain.GetHeaderByNumber(number)
	var state, err = chain.GetState(chain.GetBlock(headAvai.ParentHash, headAvai.Number.Uint64() - 1).Root())
	return state,err
}
