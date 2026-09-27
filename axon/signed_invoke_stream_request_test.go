package axon

import "testing"

func TestSignedInvokeStreamRequestHelperPayloadAddsStreamOptions(t *testing.T) {
	req := SignedInvokeStreamRequest{
		SignedInvokeRequest: minimalValidRequest(),
		ChunkTimeoutMs:      5000,
		ChunkBufferSize:     8,
	}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload returned error: %v", err)
	}
	if got := payload["chunk_timeout_ms"]; got != 5000 {
		t.Fatalf("chunk_timeout_ms = %v, want 5000", got)
	}
	if got := payload["chunk_buffer_size"]; got != 8 {
		t.Fatalf("chunk_buffer_size = %v, want 8", got)
	}
	if got := payload["ability"]; got != "easynet:///r/acme/ability/authority.runtime.forward@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke" {
		t.Fatalf("ability = %v", got)
	}
	if _, ok := payload["subject_ref"]; ok {
		t.Fatalf("subject_ref must not be emitted by signed stream wire")
	}
	if _, ok := payload["descriptor_version"]; ok {
		t.Fatalf("descriptor_version must not be emitted by signed stream wire")
	}
}

func TestSignedInvokeStreamRequestHelperPayloadOmitsZeroStreamOptions(t *testing.T) {
	req := SignedInvokeStreamRequest{SignedInvokeRequest: minimalValidRequest()}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload returned error: %v", err)
	}
	if _, ok := payload["chunk_timeout_ms"]; ok {
		t.Fatalf("zero ChunkTimeoutMs must be omitted")
	}
	if _, ok := payload["chunk_buffer_size"]; ok {
		t.Fatalf("zero ChunkBufferSize must be omitted")
	}
}
