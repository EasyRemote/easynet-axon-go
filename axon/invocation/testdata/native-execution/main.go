// External Go consumer of the public signed native and receipt APIs.
// Fixed keys are synthetic fixture identities, never deployment credentials.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	transport "axon.run/sdk/go/axon"
	invocation "axon.run/sdk/go/axon/invocation"
)

const callerURA = "easynet:///r/test/agent/alice.caller"
const calleeURA = "easynet:///r/test/agent/bob.callee"
const subjectURA = "easynet:///r/test/resource/alice.items/item"

var ability = "easynet:///r/test/ability/bob.callee.echo@descriptor.v1#" + strings.Repeat("aa", 32) + "!invoke"

func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func check(err error) {
	if err != nil {
		panic(err)
	}
}

type receiptPin struct{}

func (receiptPin) Resolve(identity string) (ed25519.PublicKey, error) {
	if identity != calleeURA {
		return nil, fmt.Errorf("unexpected receipt signer")
	}
	key, err := hex.DecodeString("d759793bbc13a2819a827c76adb6fba8a49aee007f49f2d0992d99b825ad2c48")
	return ed25519.PublicKey(key), err
}
func request(mode invocation.CallMode, payload []byte, nonce [16]byte, key byte) invocation.DescriptorBoundInvocationRequest {
	var prior [32]byte
	for i := range prior {
		prior[i] = 3
	}
	envelope, err := invocation.NewDescriptorBoundEnvelope(invocation.InvocationEnvelope{
		Caller:  invocation.NewAgentIdentity(callerURA, invocation.ProfileStrictV2),
		Callee:  invocation.NewAgentIdentity(calleeURA, invocation.ProfileStrictV2),
		Subject: invocation.NewSubjectIdentity(subjectURA, invocation.ProfileStrictV2),
		Ability: ability, ArgsDigest: invocation.Sha256(payload), InvocationNonce: nonce,
		CausalContext: invocation.CausalScalarCtx(invocation.ReceiptRef{ReceiptHash: prior, ReceiptURA: "easynet:///r/test/resource/alice.receipts/prior"}),
	})
	check(err)
	signed, err := invocation.SignDescriptorBoundInvocationRequest(mode, envelope, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{key}, 32)), payload, "fixture-caller")
	check(err)
	return signed
}
func result(response map[string]any) []byte {
	output, err := base64.StdEncoding.DecodeString(response["result_base64"].(string))
	check(err)
	return output
}
func verifyResponse(response map[string]any, state float64, output []byte, nonceHex string) {
	require(response["state"] == state, "incorrect response state")
	require(bytes.Equal(result(response), output), "incorrect result")
	receipts := []invocation.ReceiptJSON{}
	for _, name := range []string{"admission_receipt", "terminal_receipt"} {
		raw, err := json.Marshal(response[name])
		check(err)
		unverified, err := invocation.ParseInvocationReceiptJSON(raw)
		check(err)
		_, err = unverified.Verify(receiptPin{})
		check(err)
		var receipt invocation.ReceiptJSON
		check(json.Unmarshal(raw, &receipt))
		require(receipt.CallerBinding.URA == callerURA && receipt.CalleeBinding.URA == calleeURA && receipt.SubjectBinding.URA == subjectURA && receipt.AbilityBinding == ability, "receipt binding changed")
		var altered map[string]any
		check(json.Unmarshal(raw, &altered))
		altered["reason"] = "tampered receipt"
		alteredRaw, err := json.Marshal(altered)
		check(err)
		tampered, err := invocation.ParseInvocationReceiptJSON(alteredRaw)
		check(err)
		_, err = tampered.Verify(receiptPin{})
		require(err != nil, "tampered receipt accepted")
		require(receipt.InvocationNonceHex == nonceHex, "receipt nonce changed")
		require(receipt.CausalBinding.Form == "scalar" && receipt.CausalBinding.ReceiptHashHex == strings.Repeat("03", 32) && receipt.CausalBinding.ReceiptURA == "easynet:///r/test/resource/alice.receipts/prior", "receipt causal binding changed")
		receipts = append(receipts, receipt)
	}
	require(receipts[0].InvocationID != "" && receipts[1].InvocationID == receipts[0].InvocationID && receipts[1].Index > receipts[0].Index, "receipt sequence changed")
	digest := invocation.Sha256(output)
	require(receipts[1].PayloadSha256Hex == hex.EncodeToString(digest[:]), "signed output digest changed")
	want := "completed"
	if state == 6 {
		want = "failed"
		require(strings.Contains(receipts[1].Reason, "business_refused"), "failure reason changed")
	}
	require(strings.ToLower(receipts[1].State) == want, "signed terminal state changed")
}
func main() {
	require(len(os.Args) == 3, "endpoint and native library required")
	if os.Getenv("AXON_GO_EXECUTION_ENTRYPOINT") == "bidi" {
		runBidi()
		return
	}
	if os.Getenv("AXON_GO_EXECUTION_ENTRYPOINT") == "stream" {
		runStream()
		return
	}
	var invoke func(transport.DescriptorBoundForwardingRequest, transport.DescriptorBoundNativeOptions) (map[string]any, error)
	switch os.Getenv("AXON_GO_EXECUTION_ENTRYPOINT") {
	case "", "bridge":
		bridge, err := transport.OpenDendriteBridge(os.Args[2])
		check(err)
		defer bridge.CloseLibrary()
		handle, err := bridge.OpenClient(os.Args[1], 1500)
		check(err)
		defer bridge.CloseClient(handle)
		invoke = func(request transport.DescriptorBoundForwardingRequest, options transport.DescriptorBoundNativeOptions) (map[string]any, error) {
			return bridge.InvokeDescriptorBound(handle, request, options)
		}
	case "client":
		sidecar := &transport.SidecarTransport{Endpoint: os.Args[1], LibraryPath: os.Args[2], ConnectTimeoutMs: 1500}
		defer sidecar.Close()
		client := transport.NewClient(sidecar).Ability(ability)
		invoke = func(request transport.DescriptorBoundForwardingRequest, options transport.DescriptorBoundNativeOptions) (map[string]any, error) {
			return client.InvokeDescriptorBound(context.Background(), request, options)
		}
	default:
		panic("unknown Go execution entrypoint")
	}
	var err error

	options := transport.DescriptorBoundNativeOptions{RequestID: "go-native-execution", ContentType: "application/octet-stream", TimeoutMs: 3000}
	payload := []byte("exact\x00payload\xff")
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	_, err = invoke(request(invocation.CallModeRPC, payload, nonce, 8), options)
	require(err != nil && strings.Contains(err.Error(), "AXON_CALLER_SIGNATURE_INVALID"), "invalid signature accepted")
	signed := request(invocation.CallModeRPC, payload, nonce, 7)
	response, err := invoke(signed, options)
	check(err)
	_, err = invoke(signed, options)
	require(err != nil && strings.Contains(err.Error(), "AXON_NONCE_REPLAY"), "replay accepted")
	for i := range nonce {
		nonce[i] = 17
	}
	_, err = invoke(request(invocation.CallModeRPC, []byte("business-failure"), nonce, 7), options)
	var failure transport.DendriteError
	require(errors.As(err, &failure) && strings.Contains(failure.Message, "business_refused") && failure.InvocationResponseJSON != "", "business failure lost receipts")
	var failed map[string]any
	check(json.Unmarshal([]byte(failure.InvocationResponseJSON), &failed))
	verifyResponse(response, 5, payload, "000102030405060708090a0b0c0d0e0f")
	failedPayload := result(failed)
	require(failed["result_content_type"] == "application/axon-error+json", "failure content type changed")
	var errorBody map[string]any
	check(json.Unmarshal(failedPayload, &errorBody))
	require(errorBody["kind"] == "invalid_argument" && errorBody["reason"] == "business_refused", "failure output changed")
	verifyResponse(failed, 6, failedPayload, strings.Repeat("11", 16))
	fmt.Println("execution_and_receipt_signatures_verified")
}

// Return exact wire chunks to the independently pinned canonical Rust verifier.
func runStream() {
	bridge, err := transport.OpenDendriteBridge(os.Args[2])
	check(err)
	defer bridge.CloseLibrary()
	handle, err := bridge.OpenClient(os.Args[1], 1500)
	check(err)
	defer bridge.CloseClient(handle)
	options := transport.DescriptorBoundNativeStreamOptions{
		DescriptorBoundNativeOptions: transport.DescriptorBoundNativeOptions{
			RequestID: "go-native-stream", ContentType: "application/octet-stream", TimeoutMs: 3000,
		}, ChunkTimeoutMs: 1000, ChunkBufferSize: 4,
	}
	call := func(signed invocation.DescriptorBoundInvocationRequest) ([]string, error) {
		stream, opened, err := bridge.StreamDescriptorBound(handle, signed, options)
		if err != nil {
			return nil, err
		}
		defer func() { check(bridge.StreamClose(stream)); check(bridge.StreamClose(stream)) }()
		require(opened != nil && opened.StreamHandle == stream, "stream handle changed")
		chunks := []string{}
		for i := 0; i < 16; i++ {
			next, err := bridge.StreamNext(stream, 1000)
			if err != nil {
				return nil, err
			}
			require(!next.Timeout, "stream timed out")
			if next.Done {
				return chunks, nil
			}
			chunks = append(chunks, base64.StdEncoding.EncodeToString(next.Chunk))
		}
		return nil, fmt.Errorf("stream exceeded bounded chunk count")
	}
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	payload := []byte("stream-result")
	_, err = call(request(invocation.CallModeStream, payload, nonce, 8))
	require(err != nil && strings.Contains(err.Error(), "AXON_CALLER_SIGNATURE_INVALID"), "invalid stream signature accepted")
	signed := request(invocation.CallModeStream, payload, nonce, 7)
	success, err := call(signed)
	check(err)
	_, err = call(signed)
	require(err != nil && strings.Contains(err.Error(), "AXON_NONCE_REPLAY"), "stream replay accepted")
	for i := range nonce {
		nonce[i] = 17
	}
	failure, err := call(request(invocation.CallModeStream, []byte("business-failure"), nonce, 7))
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{"success": success, "failure": failure}))
}

func runBidi() {
	bridge, err := transport.OpenDendriteBridge(os.Args[2])
	check(err)
	defer bridge.CloseLibrary()
	handle, err := bridge.OpenClient(os.Args[1], 1500)
	check(err)
	defer bridge.CloseClient(handle)
	options := transport.DescriptorBoundNativeBidiOptions{DescriptorBoundNativeStreamOptions: transport.DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: transport.DescriptorBoundNativeOptions{RequestID: "go-native-bidi", ContentType: "application/octet-stream", TimeoutMs: 3000}, ChunkTimeoutMs: 1000, ChunkBufferSize: 8}, RequestBufferSize: 4, Streams: []transport.StreamDescriptor{{StreamID: 0, ContentType: "application/octet-stream", Ordering: "STRICT"}}}
	call := func(signed invocation.DescriptorBoundInvocationRequest, state float64, nonceHex string) error {
		stream, _, err := bridge.BidiDescriptorBound(handle, signed, options)
		if err != nil {
			return err
		}
		defer func() { check(stream.Dispose()); check(stream.Dispose()) }()
		check(stream.Send(0, []byte{0, 255, 10, 42}, 0))
		check(stream.SendEOF())
		frames := []transport.BidiFrame{}
		for i := 0; i < 8; i++ {
			frame, err := stream.Recv(1000)
			check(err)
			if frame.Kind == transport.BidiFrameDone {
				break
			}
			require(frame.Kind != transport.BidiFrameTimeout, "bidi timed out")
			frames = append(frames, frame)
		}
		require(len(frames) == 4, "bidi frame count changed")
		for i, frame := range frames {
			require(frame.Sequence == uint64(i), "bidi sequence changed")
		}
		require(frames[0].Kind == transport.BidiFrameReceipt && !frames[0].Terminal, "admission missing")
		require(frames[1].Kind == transport.BidiFrameBinary && bytes.Equal(frames[1].Data, []byte{0, 255, 10, 42}), "binary progress changed")
		require(frames[2].Kind == transport.BidiFrameBinary, "output missing")
		require(frames[3].Kind == transport.BidiFrameReceipt && frames[3].Terminal, "terminal missing")
		output := frames[2].Data
		if state == 5 {
			require(bytes.Equal(output, []byte("bidi-result")), "bidi output changed")
		} else {
			var failure map[string]any
			check(json.Unmarshal(output, &failure))
			require(failure["kind"] == "invalid_argument" && failure["reason"] == "business_refused", "bidi failure changed")
		}
		verifyResponse(map[string]any{"state": state, "result_base64": base64.StdEncoding.EncodeToString(output), "admission_receipt": frames[0].Receipt, "terminal_receipt": frames[3].Receipt}, state, output, nonceHex)
		frame, err := stream.Recv(1000)
		check(err)
		require(frame.Kind == transport.BidiFrameDone, "terminal read repeated")
		require(stream.Send(0, []byte("late"), 0) != nil, "send after terminal accepted")
		return nil
	}
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	payload := []byte("bidi-result")
	nonceHex := hex.EncodeToString(nonce[:])
	err = call(request(invocation.CallModeBidi, payload, nonce, 8), 5, nonceHex)
	require(err != nil && strings.Contains(err.Error(), "AXON_CALLER_SIGNATURE_INVALID"), "invalid bidi signature accepted")
	signed := request(invocation.CallModeBidi, payload, nonce, 7)
	check(call(signed, 5, nonceHex))
	err = call(signed, 5, nonceHex)
	require(err != nil && strings.Contains(err.Error(), "AXON_NONCE_REPLAY"), "bidi replay accepted")
	for i := range nonce {
		nonce[i] = 17
	}
	check(call(request(invocation.CallModeBidi, []byte("business-failure"), nonce, 7), 6, hex.EncodeToString(nonce[:])))
	fmt.Println("execution_and_receipt_signatures_verified")
}
