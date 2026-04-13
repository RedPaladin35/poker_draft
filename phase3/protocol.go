package network

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// PokerProtocolID is the libp2p stream protocol for direct peer-to-peer messages.
// Used for private communication between two players — specifically the partial
// decryptions for a player's own hole cards, which should not be broadcast.
//
// All public game messages (actions, shuffle steps, community card decryptions)
// go through GossipSub instead.
const PokerProtocolID = protocol.ID("/poker/1.0.0")

// StreamHandler is called for each decoded envelope received on a direct stream.
type StreamHandler func(env *Envelope, from peer.ID)

// RegisterProtocolHandler installs the /poker/1.0.0 stream handler on a host.
// Each incoming stream is read, length-decoded, and dispatched to handler.
// The handler is called synchronously within the stream goroutine — if it does
// heavy work, it should dispatch to its own goroutine.
func RegisterProtocolHandler(h host.Host, handler StreamHandler) {
	h.SetStreamHandler(PokerProtocolID, func(s network.Stream) {
		defer s.Close()
		remotePeer := s.Conn().RemotePeer()
		reader := bufio.NewReaderSize(s, 4096)

		for {
			// Read 4-byte length prefix.
			lenBuf := make([]byte, 4)
			if _, err := io.ReadFull(reader, lenBuf); err != nil {
				return // EOF or reset — stream is done
			}
			msgLen := binary.BigEndian.Uint32(lenBuf)
			if msgLen == 0 || int(msgLen) > MaxMessageSize {
				return
			}

			msgBuf := make([]byte, msgLen)
			if _, err := io.ReadFull(reader, msgBuf); err != nil {
				return
			}

			// Reassemble the length-prefixed frame for DecodeEnvelope.
			frame := make([]byte, 4+msgLen)
			copy(frame[:4], lenBuf)
			copy(frame[4:], msgBuf)

			// Skip signature verification — Noise already authenticates the stream.
			env, err := DecodeEnvelope(frame, nil)
			if err != nil {
				continue // skip malformed messages, keep the stream open
			}
			handler(env, remotePeer)
		}
	})
}

// SendDirect opens a /poker/1.0.0 stream to peerID and writes one framed message.
// The stream is closed after the write. For multiple sequential messages to the
// same peer, callers should reuse an open stream (not yet implemented — use
// BroadcastPartialDecrypt via GossipSub for the current phase).
func SendDirect(ctx context.Context, h host.Host, peerID peer.ID, frame []byte) error {
	s, err := h.NewStream(ctx, peerID, PokerProtocolID)
	if err != nil {
		return fmt.Errorf("SendDirect to %s: open stream: %w", peerID, err)
	}
	defer s.Close()

	if _, err := s.Write(frame); err != nil {
		return fmt.Errorf("SendDirect to %s: write: %w", peerID, err)
	}
	return nil
}
