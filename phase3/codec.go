package network

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/p2p-poker/internal/crypto"
	gocrypto "crypto"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

// MaxMessageSize is the maximum byte length of any single envelope on the wire.
// Protects against memory exhaustion from malformed peers.
const MaxMessageSize = 4 * 1024 * 1024 // 4 MiB

// EncodeEnvelope serialises and signs an envelope.
// The signature covers: type || sender_id || seq || timestamp || payload.
func EncodeEnvelope(env *Envelope, privKey ed25519.PrivateKey) ([]byte, error) {
	sigData := envelopeSignBytes(env)
	sig := ed25519.Sign(privKey, sigData)
	env.Signature = sig

	b, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("EncodeEnvelope: marshal: %w", err)
	}

	// Prefix with 4-byte big-endian length for stream framing.
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(b)))
	copy(frame[4:], b)
	return frame, nil
}

// DecodeEnvelope reads a length-prefixed envelope from a byte slice
// and verifies the sender's Ed25519 signature.
func DecodeEnvelope(frame []byte, pubKeyFn func(peerID string) (ed25519.PublicKey, error)) (*Envelope, error) {
	if len(frame) < 4 {
		return nil, errors.New("DecodeEnvelope: frame too short")
	}
	msgLen := binary.BigEndian.Uint32(frame[:4])
	if int(msgLen) > MaxMessageSize {
		return nil, fmt.Errorf("DecodeEnvelope: message too large (%d bytes)", msgLen)
	}
	if int(msgLen) > len(frame)-4 {
		return nil, fmt.Errorf("DecodeEnvelope: incomplete frame")
	}

	env := &Envelope{}
	if err := proto.Unmarshal(frame[4:4+msgLen], env); err != nil {
		return nil, fmt.Errorf("DecodeEnvelope: unmarshal: %w", err)
	}

	// Verify signature.
	if pubKeyFn != nil {
		pub, err := pubKeyFn(env.SenderId)
		if err != nil {
			return nil, fmt.Errorf("DecodeEnvelope: lookup pubkey for %s: %w", env.SenderId, err)
		}
		sigData := envelopeSignBytes(env)
		if !ed25519.Verify(pub, sigData, env.Signature) {
			return nil, fmt.Errorf("DecodeEnvelope: invalid signature from %s", env.SenderId)
		}
	}
	return env, nil
}

// envelopeSignBytes produces the canonical byte sequence that is signed/verified.
func envelopeSignBytes(env *Envelope) []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, byte(env.Type))
	buf = append(buf, []byte(env.SenderId)...)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], uint64(env.Seq))
	buf = append(buf, seq[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(env.Timestamp))
	buf = append(buf, ts[:]...)
	buf = append(buf, env.Payload...)
	return buf
}

// NewEnvelope constructs an Envelope for the given message type and payload.
func NewEnvelope(msgType MsgType, senderID string, seq int64, payload []byte) *Envelope {
	return &Envelope{
		Type:      msgType,
		SenderId:  senderID,
		Seq:       seq,
		Timestamp: time.Now().UnixMilli(),
		Payload:   payload,
	}
}

// ── Big.Int wire encoding ────────────────────────────────────────────────────

// BigIntToBytes encodes a big.Int as big-endian bytes (nil-safe).
func BigIntToBytes(n *big.Int) []byte {
	if n == nil {
		return nil
	}
	return n.Bytes()
}

// BytesToBigInt decodes big-endian bytes to a big.Int (nil-safe).
func BytesToBigInt(b []byte) *big.Int {
	if b == nil {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

// ── ZKProof wire conversions ─────────────────────────────────────────────────

// ZKProofToWire converts a crypto.ZKProof to its protobuf wire type.
func ZKProofToWire(p *crypto.ZKProof) *ZKProofWire {
	return &ZKProofWire{
		A: BigIntToBytes(p.A),
		B: BigIntToBytes(p.B),
		S: BigIntToBytes(p.S),
		H: BigIntToBytes(p.H),
	}
}

// ZKProofFromWire converts a wire ZKProofWire back to crypto.ZKProof.
func ZKProofFromWire(w *ZKProofWire) *crypto.ZKProof {
	if w == nil {
		return nil
	}
	return &crypto.ZKProof{
		A: BytesToBigInt(w.A),
		B: BytesToBigInt(w.B),
		S: BytesToBigInt(w.S),
		H: BytesToBigInt(w.H),
	}
}

// ── Deck wire encoding ───────────────────────────────────────────────────────

// DeckToWire converts a []*big.Int deck to a [][]byte for protobuf.
func DeckToWire(deck []*big.Int) [][]byte {
	out := make([][]byte, len(deck))
	for i, v := range deck {
		out[i] = BigIntToBytes(v)
	}
	return out
}

// DeckFromWire converts [][]byte back to []*big.Int.
func DeckFromWire(wire [][]byte) []*big.Int {
	out := make([]*big.Int, len(wire))
	for i, b := range wire {
		out[i] = BytesToBigInt(b)
	}
	return out
}

// ── PeerID helpers ───────────────────────────────────────────────────────────

// PeerIDFromString parses a base58-encoded libp2p PeerID.
func PeerIDFromString(s string) (peer.ID, error) {
	pid, err := peer.Decode(s)
	if err != nil {
		return "", fmt.Errorf("PeerIDFromString: %w", err)
	}
	return pid, nil
}

// ExtractEd25519PubKey pulls the Ed25519 public key out of a libp2p PeerID.
func ExtractEd25519PubKey(pid peer.ID) (ed25519.PublicKey, error) {
	pubKey, err := pid.ExtractPublicKey()
	if err != nil {
		return nil, fmt.Errorf("ExtractEd25519PubKey: %w", err)
	}
	raw, err := pubKey.Raw()
	if err != nil {
		return nil, fmt.Errorf("ExtractEd25519PubKey: raw: %w", err)
	}
	_ = gocrypto.SHA256 // suppress import
	return ed25519.PublicKey(raw), nil
}
