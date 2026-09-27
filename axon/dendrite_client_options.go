package axon

// DendriteClientOptions configures native transport trust and connection timing.
// Certificate validation belongs to the native bridge. TLSCAPEM must contain
// public CA certificates only; nil omits the field, while an explicit empty
// string is forwarded for native rejection.
type DendriteClientOptions struct {
	ConnectTimeoutMs int
	TLSCAPEM         *string
}

func (o DendriteClientOptions) openPayload(endpoint string) map[string]any {
	timeout := o.ConnectTimeoutMs
	if timeout <= 0 {
		timeout = DefaultConnectTimeoutMs
	}
	payload := map[string]any{"endpoint": endpoint, "connect_timeout_ms": timeout}
	if o.TLSCAPEM != nil {
		payload["tls_ca_pem"] = *o.TLSCAPEM
	}
	return payload
}
