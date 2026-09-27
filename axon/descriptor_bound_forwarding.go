// Complete caller-owned request forwarding through application facades.
// Invocation owns signed facts; this layer owns transport lifetime and deadlines.
package axon

import (
	"context"
	"errors"
	"time"
)

// DescriptorBoundForwardingRequest exposes the signed ability for fluent binding
// checks without coupling transport to the canonical invocation package.
type DescriptorBoundForwardingRequest interface {
	DescriptorBoundNativeRequest
	DescriptorBoundAbility() string
}

// DescriptorBoundTransport is an optional capability of an application Transport.
type DescriptorBoundTransport interface {
	InvokeDescriptorBound(context.Context, DescriptorBoundForwardingRequest, DescriptorBoundNativeOptions) (map[string]any, error)
}

// InvokeDescriptorBound forwards the complete request without identity defaults.
func (c *Client) InvokeDescriptorBound(ctx context.Context, request DescriptorBoundForwardingRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	transport, ok := c.transport.(DescriptorBoundTransport)
	if !ok {
		return nil, errors.New("configured transport does not support complete-request forwarding")
	}
	if request == nil {
		return nil, errors.New("descriptor-bound request is required")
	}
	if c.principalID != "" {
		return nil, errors.New("principal(...) cannot override the signed request subject")
	}
	if c.resourceURA != "" && c.resourceURA != request.DescriptorBoundAbility() {
		return nil, errors.New("bound client ability does not match the signed request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return transport.InvokeDescriptorBound(ctx, request, options)
}

// InvokeDescriptorBound uses a transport-only session and never retries an
// invocation. Context deadlines cap the synchronous native request at dispatch;
// cancellation after dispatch cannot interrupt the C call. Native failures keep
// their receipt evidence even if the context expires while the call is running.
func (s *SidecarTransport) InvokeDescriptorBound(ctx context.Context, request DescriptorBoundForwardingRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	if s.Signing != nil {
		return nil, errors.New("complete-request forwarding requires a transport-only SidecarTransport")
	}
	if request == nil {
		return nil, errors.New("descriptor-bound request is required")
	}
	if options.TimeoutMs < 0 {
		return nil, errors.New("timeout_ms must not be negative")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureClient(); err != nil {
		return nil, err
	}
	// Keep the handle/library alive until the bounded native call finishes.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bridge == nil || s.handle == 0 {
		return nil, errors.New("transport closed before request dispatch")
	}
	if options.TimeoutMs == 0 {
		options.TimeoutMs = s.TimeoutMs
	}
	timeout, err := invocationTimeout(ctx, options.TimeoutMs)
	if err != nil {
		return nil, err
	}
	options.TimeoutMs = timeout
	return s.bridge.InvokeDescriptorBound(s.handle, request, options)
}

func invocationTimeout(ctx context.Context, timeoutMs int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		deadlineMs := int(remaining.Milliseconds())
		if deadlineMs < 1 {
			deadlineMs = 1
		}
		if deadlineMs < timeoutMs {
			timeoutMs = deadlineMs
		}
	}
	return timeoutMs, nil
}
