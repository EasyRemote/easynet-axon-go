package axon

func acceptVerifiedReceipt(record receiptRecord) SignedInvocationReceipt {
	return SignedInvocationReceipt{receipt: cloneReceiptRecord(record)}
}

func emptySignedReceipt() SignedInvocationReceipt {
	return SignedInvocationReceipt{}
}
