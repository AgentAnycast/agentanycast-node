package telemetry

import (
	"context"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// envelopeCarrier adapts A2AEnvelope for W3C Trace Context propagation.
type envelopeCarrier struct {
	env *pb.A2AEnvelope
}

var _ propagation.TextMapCarrier = (*envelopeCarrier)(nil)

func (c *envelopeCarrier) Get(key string) string {
	if c.env == nil {
		return ""
	}
	switch key {
	case "traceparent":
		return c.env.TraceParent
	case "tracestate":
		return c.env.TraceState
	default:
		return ""
	}
}

func (c *envelopeCarrier) Set(key, value string) {
	if c.env == nil {
		return
	}
	switch key {
	case "traceparent":
		c.env.TraceParent = value
	case "tracestate":
		c.env.TraceState = value
	}
}

func (c *envelopeCarrier) Keys() []string {
	if c.env == nil {
		return nil
	}
	return []string{"traceparent", "tracestate"}
}

// InjectTraceContext writes the current span's trace context into the A2AEnvelope.
func InjectTraceContext(ctx context.Context, env *pb.A2AEnvelope) {
	otel.GetTextMapPropagator().Inject(ctx, &envelopeCarrier{env: env})
}

// ExtractTraceContext reads trace context from the A2AEnvelope and returns
// a new context with the extracted span context.
func ExtractTraceContext(ctx context.Context, env *pb.A2AEnvelope) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, &envelopeCarrier{env: env})
}
