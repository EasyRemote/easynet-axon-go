// Dendrite native error projection. Keeps unverified response evidence as raw
// JSON; error classification and receipt signature verification remain separate.
package axon

import (
	"bytes"
	"encoding/json"
)

func decodeDendriteFailure(document []byte) DendriteError {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(document, &envelope) != nil {
		return DendriteError{Code: ErrCodeJSON, Message: "invalid bridge error json"}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope.Error, &fields) != nil || fields == nil {
		return DendriteError{Code: ErrCodeBridge, Message: "bridge error envelope missing object field 'error'"}
	}
	var code, message, source string
	if json.Unmarshal(fields["code"], &code) != nil ||
		json.Unmarshal(fields["message"], &message) != nil ||
		json.Unmarshal(fields["source"], &source) != nil ||
		code == "" || message == "" || source == "" {
		return DendriteError{Code: ErrCodeBridge, Message: "bridge error envelope missing code, message, or source"}
	}
	result := DendriteError{Code: code, Message: message, Source: source}
	evidence := bytes.TrimSpace(fields["invocation_response"])
	if len(evidence) != 0 && !bytes.Equal(evidence, []byte("null")) {
		// The outer JSON decoder already validated the complete document.
		if evidence[0] != '{' {
			return DendriteError{Code: ErrCodeBridge, Message: "bridge invocation_response must be an object"}
		}
		result.InvocationResponseJSON = string(evidence)
	}
	return result
}
