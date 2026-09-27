package axon

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func nativeSignedRequest(t *testing.T) DescriptorBoundInvocationRequest {
	t.Helper()
	payload := []byte{0, 255, 1, 128}
	draft := mustDescriptorBoundBuilderDraft(t, payload)
	request, err := SignDescriptorBoundInvocationRequest(CallModeRPC, draft.Envelope(), ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)), payload, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestDescriptorBoundNativePreservesSignedFacts(t *testing.T) {
	request := nativeSignedRequest(t)
	payload, err := request.DescriptorBoundNativePayload()
	if err != nil {
		t.Fatal(err)
	}
	envelope := request.Envelope().Envelope()
	expected := map[string]any{
		"version": "axon.descriptor-bound-invocation.v1", "mode": "unary",
		"envelope":       map[string]any{"caller": IdentityFromAgent(envelope.Caller), "callee": IdentityFromAgent(envelope.Callee), "subject": IdentityFromSubject(envelope.Subject), "ability": envelope.Ability, "nonce_base64": base64.StdEncoding.EncodeToString(envelope.InvocationNonce[:]), "causal_context": CausalFromCtx(envelope.CausalContext)},
		"signature":      map[string]any{"algorithm": "ed25519", "signature_base64": base64.StdEncoding.EncodeToString(request.Signature().Signature), "key_id_hint": "test-key"},
		"payload_base64": "AP8BgA==",
	}
	if !reflect.DeepEqual(payload, expected) {
		t.Fatal("signed facts changed")
	}
	if _, err := json.Marshal(payload); err != nil {
		t.Fatal(err)
	}
	canonical, err := request.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), canonical, request.Signature().Signature) {
		t.Fatal("invalid test signature")
	}
	payload["envelope"].(map[string]any)["ability"] = "mutated"
	payload["signature"].(map[string]any)["signature_base64"] = "mutated"
	fresh, err := request.DescriptorBoundNativePayload()
	if err != nil || !reflect.DeepEqual(fresh, expected) {
		t.Fatal("projection aliases request")
	}
}

func TestDescriptorBoundNativeRejectsInvalidRequests(t *testing.T) {
	for _, name := range []string{"unknown_mode", "parent", "supervisor", "payload", "signature", "nonce", "empty"} {
		t.Run(name, func(t *testing.T) {
			request := nativeSignedRequest(t)
			switch name {
			case "unknown_mode":
				request.callMode = CallMode("unknown")
			case "parent":
				request = request.WithParentInvocationID("parent")
			case "supervisor":
				request = request.WithSupervisor(&SupervisorSpec{})
			case "payload":
				request.payload = []byte("different")
			case "signature":
				request.signature.Signature = []byte{1}
			case "nonce":
				request.envelope.envelope.InvocationNonce = [16]byte{}
			case "empty":
				request = DescriptorBoundInvocationRequest{}
			}
			if _, err := request.DescriptorBoundNativePayload(); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestDescriptorBoundNativeStreamPreservesModeAndSignature(t *testing.T) {
	request := nativeSignedRequest(t)
	before, err := request.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	request.callMode = CallModeStream
	payload, err := request.DescriptorBoundNativePayload()
	if err != nil {
		t.Fatal(err)
	}
	if payload["mode"] != "server_stream" {
		t.Fatal("stream mode lost")
	}
	after, err := request.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("mode changed signed facts")
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), after, request.Signature().Signature) {
		t.Fatal("signature changed")
	}
}

func TestDescriptorBoundNativeBidiPreservesSignature(t *testing.T) {
	request := nativeSignedRequest(t)
	request.callMode = CallModeBidi
	payload, err := request.DescriptorBoundNativePayload()
	if err != nil {
		t.Fatal(err)
	}
	if payload["mode"] != "bidi" {
		t.Fatal("bidi mode lost")
	}
	canonical, err := request.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), canonical, request.Signature().Signature) {
		t.Fatal("signature changed")
	}
}
