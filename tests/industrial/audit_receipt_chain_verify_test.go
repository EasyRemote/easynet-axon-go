// I-06 audit_receipt_chain_verify
package industrial

import (
	"context"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_audit_receipt_chain_verify(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "chain_verify", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		for i := 0; i < 5; i++ {
			if e := ac.EmitProgress([]byte{byte('a' + i)}, ""); e != nil {
				return nil, e
			}
		}
		return []byte("ok"), nil
	})
	h, e := invokeIndustrial(t, context.Background(), rt, key, "chain_verify", nil, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	h.Wait(context.Background())
	receipts := rt.CoreOf(h.InvocationID()).SnapshotReceipts()
	r := inv.VerifyReceiptChain(receipts)
	if !r.OK {
		t.Fatalf("chain broken: %s", r.Detail)
	}
	if len(receipts) < 10 {
		t.Fatalf("receipts=%d", len(receipts))
	}
}
