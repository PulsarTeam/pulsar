package types

type EpochData struct {
	PivotBlockHeader      *Header
	ReferenceBlockHeader  []*Header
	Transactions 		  Transactions
	Receipts 			  []*Receipt
}
