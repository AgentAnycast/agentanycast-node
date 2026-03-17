package node

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// CardProtocolID is the protocol for Agent Card exchange.
	CardProtocolID = protocol.ID("/agentanycast/card/1.0")
	// A2AProtocolID is the protocol for A2A message exchange.
	A2AProtocolID = protocol.ID("/agentanycast/a2a/1.0")

	// MaxMessageSize is the maximum size of a single framed message (16 MB).
	MaxMessageSize = 16 * 1024 * 1024
)

// RegisterCardProtocol registers the Agent Card exchange stream handler.
// When a peer connects and opens a card stream, we read their card and store it.
func (h *Host) RegisterCardProtocol(localCardFn func() []byte) {
	h.SetStreamHandler(CardProtocolID, func(s network.Stream) {
		defer s.Close()
		remotePeer := s.Conn().RemotePeer()

		// Read the remote peer's card.
		data, err := readFramed(s)
		if err != nil {
			h.logger.Warn("failed to read peer card", "peer", remotePeer, "error", err)
			return
		}
		h.SetPeerCard(remotePeer, data)
		h.logger.Info("received agent card", "peer", remotePeer, "size", len(data))

		// Send our card back.
		card := localCardFn()
		if card != nil {
			if err := writeFramed(s, card); err != nil {
				h.logger.Warn("failed to send card", "peer", remotePeer, "error", err)
			}
		}
	})
}

// ExchangeCard initiates a card exchange with a connected peer.
func (h *Host) ExchangeCard(ctx context.Context, pid peer.ID, localCard []byte) error {
	s, err := h.NewStream(ctx, pid, CardProtocolID)
	if err != nil {
		return fmt.Errorf("open card stream: %w", err)
	}
	defer s.Close()

	// Send our card.
	if err := writeFramed(s, localCard); err != nil {
		return fmt.Errorf("write card: %w", err)
	}

	// Read their card.
	data, err := readFramed(s)
	if err != nil {
		return fmt.Errorf("read peer card: %w", err)
	}
	h.SetPeerCard(pid, data)
	h.logger.Info("exchanged agent card", "peer", pid, "size", len(data))
	return nil
}

// RegisterA2AProtocol registers the A2A message stream handler.
// handler is called for each incoming A2AEnvelope (serialized protobuf bytes).
func (h *Host) RegisterA2AProtocol(handler func(remotePeer peer.ID, data []byte)) {
	h.SetStreamHandler(A2AProtocolID, func(s network.Stream) {
		defer s.Close()
		remotePeer := s.Conn().RemotePeer()
		reader := bufio.NewReader(s)

		for {
			data, err := readFramedFrom(reader)
			if err != nil {
				if err != io.EOF {
					h.logger.Warn("a2a stream read error", "peer", remotePeer, "error", err)
				}
				return
			}
			handler(remotePeer, data)
		}
	})
}

// SendA2AMessage sends a framed A2A message to a peer over the a2a protocol.
func (h *Host) SendA2AMessage(ctx context.Context, pid peer.ID, data []byte, logger *slog.Logger) error {
	s, err := h.NewStream(ctx, pid, A2AProtocolID)
	if err != nil {
		return fmt.Errorf("open a2a stream to %s: %w", pid, err)
	}
	defer s.Close()

	if err := writeFramed(s, data); err != nil {
		return fmt.Errorf("write a2a message: %w", err)
	}
	return nil
}

// ── Framing helpers (4-byte big-endian length prefix) ──────

func writeFramed(w io.Writer, data []byte) error {
	length := uint32(len(data))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readFramed(r io.Reader) ([]byte, error) {
	return readFramedFrom(r)
}

func readFramedFrom(r io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", length, MaxMessageSize)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
