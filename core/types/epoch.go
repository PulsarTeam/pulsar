package types

type EpochData struct {
	pivotBlockHeader      *Header
	referenceBlockHeader  []*Header
	transactions 		  Transactions
}
