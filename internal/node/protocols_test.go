package node

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// newTestHost creates a minimal libp2p host for testing on localhost.
func newTestHost(t *testing.T) *Host {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	h, err := NewHost(context.Background(), HostConfig{
		PrivateKey:  priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	}, testLogger())
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	return h
}

// ── Framing helper unit tests ──────────────────────────────

func TestWriteFramedAndReadFramed(t *testing.T) {
	payload := []byte("hello agentanycast")
	var buf bytes.Buffer

	if err := writeFramed(&buf, payload); err != nil {
		t.Fatalf("writeFramed: %v", err)
	}

	// Verify the wire format: 4-byte big-endian length + payload.
	if buf.Len() != 4+len(payload) {
		t.Fatalf("expected %d bytes, got %d", 4+len(payload), buf.Len())
	}

	data, err := readFramed(&buf)
	if err != nil {
		t.Fatalf("readFramed: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("got %q, want %q", data, payload)
	}
}

func TestReadFramedEmptyStream(t *testing.T) {
	var buf bytes.Buffer
	_, err := readFramed(&buf)
	if err == nil {
		t.Fatal("expected error on empty stream")
	}
}

func TestReadFramedExceedsMaxSize(t *testing.T) {
	// Write a length header that exceeds MaxMessageSize.
	var buf bytes.Buffer
	bigLen := uint32(MaxMessageSize + 1)
	_ = binary.Write(&buf, binary.BigEndian, bigLen)

	_, err := readFramed(&buf)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}

func TestReadFramedTruncatedPayload(t *testing.T) {
	// Write a valid length header but short payload.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(100))
	buf.Write([]byte("short"))

	_, err := readFramed(&buf)
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestWriteFramedMultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	msgs := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third message with more data"),
	}

	for _, m := range msgs {
		if err := writeFramed(&buf, m); err != nil {
			t.Fatalf("writeFramed: %v", err)
		}
	}

	reader := &buf
	for i, want := range msgs {
		got, err := readFramedFrom(reader)
		if err != nil {
			t.Fatalf("readFramedFrom message %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("message %d: got %q, want %q", i, got, want)
		}
	}

	// Should get EOF after all messages.
	_, err := readFramedFrom(reader)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

// ── Constants tests ────────────────────────────────────────

func TestStreamTimeoutConstants(t *testing.T) {
	if StreamReadTimeout != 30*time.Second {
		t.Fatalf("StreamReadTimeout = %v, want 30s", StreamReadTimeout)
	}
	if StreamWriteTimeout != 10*time.Second {
		t.Fatalf("StreamWriteTimeout = %v, want 10s", StreamWriteTimeout)
	}
	if MaxMessageSize != 16*1024*1024 {
		t.Fatalf("MaxMessageSize = %d, want 16MB", MaxMessageSize)
	}
}

// ── Integration: Two-host card exchange ────────────────────

func TestTwoHostCardExchange(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	card1Data := []byte("card-from-host1")
	card2Data := []byte("card-from-host2")

	// Register card protocol on host2.
	host2.RegisterCardProtocol(func() []byte {
		return card2Data
	})

	// Connect host1 -> host2.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Exchange cards.
	if err := host1.ExchangeCard(context.Background(), host2.ID(), card1Data); err != nil {
		t.Fatalf("ExchangeCard: %v", err)
	}

	// Verify host1 received host2's card.
	got1, ok := host1.GetPeerCard(host2.ID())
	if !ok {
		t.Fatal("host1 should have host2's card")
	}
	if !bytes.Equal(got1, card2Data) {
		t.Fatalf("host1 got card %q, want %q", got1, card2Data)
	}

	// Verify host2 received host1's card.
	got2, ok := host2.GetPeerCard(host1.ID())
	if !ok {
		t.Fatal("host2 should have host1's card")
	}
	if !bytes.Equal(got2, card1Data) {
		t.Fatalf("host2 got card %q, want %q", got2, card1Data)
	}
}

// ── Integration: Two-host A2A message exchange ─────────────

func TestTwoHostA2AMessageExchange(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	// Track received messages on host2.
	received := make(chan []byte, 10)
	host2.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		received <- data
	})

	// Connect host1 -> host2.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send an A2A envelope from host1 to host2.
	env := &pb.A2AEnvelope{
		EnvelopeId: "integ-env-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:    "integ-task-1",
				ContextId: "ctx-integ",
				Message: &pb.Message{
					Role: pb.MessageRole_MESSAGE_ROLE_USER,
					Parts: []*pb.Part{
						{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "hello from host1"}}},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := host1.SendA2AMessage(context.Background(), host2.ID(), data, testLogger()); err != nil {
		t.Fatalf("SendA2AMessage: %v", err)
	}

	// Wait for message on host2.
	select {
	case msg := <-received:
		// Unmarshal and verify.
		var gotEnv pb.A2AEnvelope
		if err := proto.Unmarshal(msg, &gotEnv); err != nil {
			t.Fatalf("unmarshal received: %v", err)
		}
		if gotEnv.EnvelopeId != "integ-env-1" {
			t.Fatalf("envelope ID = %q, want integ-env-1", gotEnv.EnvelopeId)
		}
		payload := gotEnv.GetSendTask()
		if payload == nil || payload.TaskId != "integ-task-1" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for A2A message")
	}
}

// ── Integration: Multiple A2A messages on same stream ──────

func TestTwoHostMultipleA2AMessages(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	received := make(chan []byte, 10)
	host2.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		received <- data
	})

	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send 3 messages.
	for i := 0; i < 3; i++ {
		env := &pb.A2AEnvelope{
			EnvelopeId: "multi-" + string(rune('a'+i)),
			Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
			Payload: &pb.A2AEnvelope_SendTask{
				SendTask: &pb.SendTaskPayload{TaskId: "task-" + string(rune('a'+i))},
			},
		}
		data, _ := proto.Marshal(env)
		if err := host1.SendA2AMessage(context.Background(), host2.ID(), data, testLogger()); err != nil {
			t.Fatalf("SendA2AMessage %d: %v", i, err)
		}
	}

	// Each SendA2AMessage opens a new stream and sends one message, so we should receive 3 messages.
	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for message %d", i)
		}
	}
}
