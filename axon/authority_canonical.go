package axon

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func canonicalAuthorityJSONBytes(payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("authority canonical JSON: trailing value")
	}
	return json.Marshal(value)
}
