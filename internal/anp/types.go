// Package anp implements the ANP (Agent Network Protocol) interop bridge for
// bidirectional translation between AgentAnycast A2A agents and ANP agents.
//
// ANP uses HTTP + JSON-RPC 2.0 with Agent Description (JSON-LD) and OpenRPC
// interface specifications. This bridge is stateless — it translates between
// ANP HTTP JSON format and internal Protobuf representations.
package anp

// AgentDescription is the ANP Agent Description document (JSON-LD format),
// served at /agent/ad.json.
type AgentDescription struct {
	Context     map[string]string `json:"@context"`
	Type        string            `json:"@type"`
	ID          string            `json:"@id"`
	DID         string            `json:"did,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Interfaces  []InterfaceEntry  `json:"interfaces,omitempty"`
}

// InterfaceEntry describes a protocol interface in the Agent Description.
type InterfaceEntry struct {
	Type     string `json:"@type"`
	Protocol string `json:"protocol"`
	URL      string `json:"url"`
}

// OpenRPCSpec is an ANP OpenRPC specification document, served at
// /agent/interface.json.
type OpenRPCSpec struct {
	OpenRPC string      `json:"openrpc"`
	Info    OpenRPCInfo `json:"info"`
	Methods []RPCMethod `json:"methods"`
}

// OpenRPCInfo contains metadata about the OpenRPC specification.
type OpenRPCInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

// RPCMethod describes a single method in the OpenRPC specification.
type RPCMethod struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Params      []RPCParam `json:"params,omitempty"`
	Result      *RPCResult `json:"result,omitempty"`
}

// RPCParam describes a parameter of an RPC method.
type RPCParam struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Schema      interface{} `json:"schema,omitempty"`
	Required    bool        `json:"required,omitempty"`
}

// RPCResult describes the result of an RPC method.
type RPCResult struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Schema      interface{} `json:"schema,omitempty"`
}

// JSONRPCRequest is a JSON-RPC 2.0 request, used for POST /agent/rpc calls.
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
	ID      interface{}   `json:"id"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
