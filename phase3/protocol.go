package network

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// PokerProtocolID is the libp2p protocol identifier for direct streams.
// Used for point-to-point communication (e.g., private hole card reveals).
const PokerProtocolID = protocol.ID("/poker/1.0.0")

// StreamHandler is called with the decoded envelope from a direct stream.
type StreamHandler func(env *Envelope, peerID peer.ID)

// ProtocolHandler registers the /poker/1.0.0 stream handler on a host.
// Incoming streams are read, decoded, and dispatched to the provided handler.
//
// Direct streams are used only for private per-player communication
// (specifically: the partial decryptions for a player's own hole cards).
// All public game messages go through GossipSub.
func ProtocolHandler(h host.Host, handler StreamHandler) {
	h.SetStreamHandler(PokerProtocolID, func(s network.Stream) {
		defer s.Close()
		remotePeer := s.Conn().RemotePeer()
		reader := bufio.NewReader(s)

		// Read 4-byte length prefix.
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, lenBuf); err != nil {
			return
		}
		msgLen := uint32(lenBuf[0])<<24 | uint32(lenBuf[1])<<16 | uint32(lenBuf[2])<<8 | uint32(lenBuf[3])
		if msgLen > MaxMessageSize {
			return
		}

		msgBuf := make([]byte, msgLen)
		if _, err := io.ReadFull(reader, msgBuf); err != nil {
			return
		}

		frame := append(lenBuf, msgBuf...)
		env, err := DecodeEnvelope(frame, nil) // signature already verified by Noise
		if err != nil {
			return
		}
		handler(env, remotePeer)
	})
}

// SendDirect opens a direct stream to peerID and sends one framed message.
// Used for private partial decryptions that should not be broadcast.
func SendDirect(ctx context.Context, h host.Host, peerID peer.ID, frame []byte) error {
	s, err := h.NewStream(ctx, peerID, PokerProtocolID)
	if err != nil {
		return fmt.Errorf("SendDirect to %s: %w", peerID, err)
	}
	defer s.Close()

	if _, err := s.Write(frame); err != nil {
		return fmt.Errorf("SendDirect write to %s: %w", peerID, err)
	}
	return nil
}
