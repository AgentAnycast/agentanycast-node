// Package anp provides a ProtocolAdapter implementation for the Agent
// Network Protocol (ANP). It wraps the existing internal/anp package
// behind the adapter.ProtocolAdapter interface.
package anp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// Adapter implements adapter.ProtocolAdapter for the ANP protocol.
// ANP uses JSON-RPC 2.0 over HTTP. This adapter translates between
// JSON-RPC bytes and the internal Envelope format.
type Adapter struct{}

// Compile-time interface check.
var _ adapter.ProtocolAdapter = (*Adapter)(nil)

// New creates a new ANP protocol adapter.
func New() *Adapter {
	return &Adapter{}
}

// Name returns "anp".
func (a *Adapter) Name() string { return "anp" }

// jsonRPCRequest is the minimal structure of a JSON-RPC 2.0 request,
// sufficient for extracting method name and ID.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

// methodToEnvelopeType maps ANP JSON-RPC method names to envelope types.
var methodToEnvelopeType = map[string]envelope.Type{
	"tasks/send":    envelope.TypeTask,
	"tasks/get":     envelope.TypeTask,
	"tasks/cancel":  envelope.TypeTaskCancel,
	"agent/card":    envelope.TypeCard,
}

// Ingest translates raw JSON-RPC bytes into an Envelope.
func (a *Adapter) Ingest(ctx context.Context, raw []byte, metadata map[string]string) (*envelope.Envelope, error) {
	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("anp: unmarshal json-rpc: %w", err)
	}

	envType, ok := methodToEnvelopeType[req.Method]
	if !ok {
		envType = envelope.TypeCustom
	}

	id := ""
	if req.ID != nil {
		id = fmt.Sprintf("%v", req.ID)
	}

	env := &envelope.Envelope{
		ID:       id,
		Type:     envType,
		Payload:  raw, // passthrough
		Metadata: map[string]string{
			envelope.MetaKeyProtocol: "anp",
			"jsonrpc.method":        req.Method,
		},
	}

	for k, v := range metadata {
		if _, exists := env.Metadata[k]; !exists {
			env.Metadata[k] = v
		}
	}

	return env, nil
}

// Emit translates an Envelope back into JSON-RPC bytes.
// In v0.7 passthrough mode, this returns the original Payload.
func (a *Adapter) Emit(ctx context.Context, env *envelope.Envelope) ([]byte, error) {
	if env.Payload == nil {
		return nil, fmt.Errorf("anp: envelope has nil payload")
	}
	return env.Payload, nil
}

// Endpoints returns nil — ANP endpoints are managed by the ANP bridge
// server directly.
func (a *Adapter) Endpoints() []adapter.Endpoint {
	return nil
}
