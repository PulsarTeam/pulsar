package params

const (
	//TxType for Ds-pow
	NormalTx                uint8 = 0
	DelegateMinerRegisterTx uint8 = 1
	DelegateStakesTx        uint8 = 2
	DelegateStakesCancel    uint8 = 3

	//max fee for Ds-pow
	MaxDelegateFeeLimit     uint32 = 1000000


)
