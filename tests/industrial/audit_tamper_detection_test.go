// I-07 audit_tamper_detection
package industrial

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_audit_tamper_detection(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "tamper", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		_ = ac.EmitProgress([]byte("a"), "")
		_ = ac.EmitProgress([]byte("b"), "")
		_ = ac.EmitProgress([]byte("c"), "")
		return []byte("done"), nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "tamper", nil, "", nil)
	h.Wait(context.Background())
	receipts := rt.CoreOf(h.InvocationID()).SnapshotReceipts()
	if !inv.VerifyReceiptChain(receipts).OK {
		t.Fatal("baseline chain failed")
	}

	index := len(receipts) / 2
	projection, err := inv.ProjectSignedInvocationReceipt(receipts[index])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["reason"] = document["reason"].(string) + "_tampered"
	tampered, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	unverified, err := inv.ParseInvocationReceiptJSON(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unverified.Verify(
		&industrialKeyResolver{key: key.Public().(ed25519.PublicKey)},
	); err == nil {
		t.Fatal("tampered wire receipt verified")
	}
}
