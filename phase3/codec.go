package network

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

// MaxMessageSize is the maximum byte length of any single envelope on the wire.
// Protects against memory exhaustion from malformed peers.
const MaxMessageSize = 4 * 1024 * 1024 // 4 MiB

// EncodeEnvelope serialises and signs an envelope, returning a
// length-prefixed frame ready to write to a stream or publish to GossipSub.
// The Ed25519 signature covers: type || sender_id || seq || timestamp || payload.
func EncodeEnvelope(env *Envelope, privKey ed25519.PrivateKey) ([]byte, error) {
	sigData := envelopeSignBytes(env)
	env.Signature = ed25519.Sign(privKey, sigData)

	b, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("EncodeEnvelope: marshal: %w", err)
	}
	if len(b) > MaxMessageSize {
		return nil, fmt.Errorf("EncodeEnvelope: message too large (%d bytes)", len(b))
	}

	// 4-byte big-endian length prefix for stream framing.
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(b)))
	copy(frame[4:], b)
	return frame, nil
}

// DecodeEnvelope reads a length-prefixed frame, unmarshals the envelope,
// and verifies the Ed25519 signature using pubKeyFn to look up the sender's key.
// Pass nil for pubKeyFn to skip signature verification (e.g. in tests, or when
// the transport layer already guarantees integrity via Noise).
func DecodeEnvelope(frame []byte, pubKeyFn func(peerID string) (ed25519.PublicKey, error)) (*Envelope, error) {
	if len(frame) < 4 {
		return nil, errors.New("DecodeEnvelope: frame too short")
	}
	msgLen := binary.BigEndian.Uint32(frame[:4])
	if int(msgLen) > MaxMessageSize {
		return nil, fmt.Errorf("DecodeEnvelope: message too large (%d bytes)", msgLen)
	}
	if int(msgLen) > len(frame)-4 {
		return nil, fmt.Errorf("DecodeEnvelope: incomplete frame (have %d, need %d)", len(frame)-4, msgLen)
	}

	env := &Envelope{}
	if err := proto.Unmarshal(frame[4:4+msgLen], env); err != nil {
		return nil, fmt.Errorf("DecodeEnvelope: unmarshal: %w", err)
	}

	// Signature verification (optional — skip when transport guarantees integrity).
	if pubKeyFn != nil && len(env.Signature) > 0 {
		pub, err := pubKeyFn(env.SenderId)
		if err != nil {
			return nil, fmt.Errorf("DecodeEnvelope: lookup pubkey for %s: %w", env.SenderId, err)
		}
		if pub != nil {
			sigData := envelopeSignBytes(env)
			if !ed25519.Verify(pub, sigData, env.Signature) {
				return nil, fmt.Errorf("DecodeEnvelope: invalid signature from %s", env.SenderId)
			}
		}
	}
	return env, nil
}

// envelopeSignBytes produces the canonical byte sequence that is signed.
// Layout: type(1) || sender_id(var) || 0x00(sep) || seq(8) || timestamp(8) || payload(var)
func envelopeSignBytes(env *Envelope) []byte {
	buf := make([]byte, 0, 1+len(env.SenderId)+1+8+8+len(env.Payload))
	buf = append(buf, byte(env.Type))
	buf = append(buf, []byte(env.SenderId)...)
	buf = append(buf, 0x00) // separator
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

// ── Big.Int wire encoding ─────────────────────────────────────────────────────

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

// ── ZKProof wire conversions ──────────────────────────────────────────────────

// ZKProofToWire converts a crypto.ZKProof to its protobuf wire type.
func ZKProofToWire(p *pokercrypto.ZKProof) *ZKProofWire {
	if p == nil {
		return nil
	}
	return &ZKProofWire{
		A: BigIntToBytes(p.A),
		B: BigIntToBytes(p.B),
		S: BigIntToBytes(p.S),
		H: BigIntToBytes(p.H),
	}
}

// ZKProofFromWire converts a wire ZKProofWire back to crypto.ZKProof.
func ZKProofFromWire(w *ZKProofWire) *pokercrypto.ZKProof {
	if w == nil {
		return nil
	}
	return &pokercrypto.ZKProof{
		A: BytesToBigInt(w.A),
		B: BytesToBigInt(w.B),
		S: BytesToBigInt(w.S),
		H: BytesToBigInt(w.H),
	}
}

// ── Deck wire encoding ────────────────────────────────────────────────────────

// DeckToWire converts a []*big.Int deck to [][]byte for protobuf.
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

// ── PeerID helpers ────────────────────────────────────────────────────────────

// PeerIDFromString parses a base58-encoded libp2p PeerID.
func PeerIDFromString(s string) (peer.ID, error) {
	pid, err := peer.Decode(s)
	if err != nil {
		return "", fmt.Errorf("PeerIDFromString: %w", err)
	}
	return pid, nil
}

// ExtractEd25519PubKey pulls the Ed25519 public key out of a libp2p PeerID.
// Works for Ed25519 keys that are small enough to be embedded in the PeerID.
func ExtractEd25519PubKey(pid peer.ID) (ed25519.PublicKey, error) {
	pubKey, err := pid.ExtractPublicKey()
	if err != nil {
		return nil, fmt.Errorf("ExtractEd25519PubKey: %w", err)
	}
	raw, err := pubKey.Raw()
	if err != nil {
		return nil, fmt.Errorf("ExtractEd25519PubKey: raw: %w", err)
	}
	return ed25519.PublicKey(raw), nil
}

// ── PartialDecrypt wire conversions ───────────────────────────────────────────

// PartialDecryptToWire converts a pokercrypto.PartialDecryption to its wire type.
func PartialDecryptToWire(tableID string, handNum int64, pd *pokercrypto.PartialDecryption) *PartialDecrypt {
	return &PartialDecrypt{
		TableId:    tableID,
		HandNum:    handNum,
		PlayerId:   pd.PlayerID,
		CardIndex:  int32(pd.CardIndex),
		Ciphertext: BigIntToBytes(pd.Ciphertext),
		Result:     BigIntToBytes(pd.Result),
		Proof:      ZKProofToWire(pd.Proof),
	}
}

// PartialDecryptFromWire converts a wire PartialDecrypt to pokercrypto.PartialDecryption.
func PartialDecryptFromWire(w *PartialDecrypt) *pokercrypto.PartialDecryption {
	return &pokercrypto.PartialDecryption{
		PlayerID:   w.PlayerId,
		CardIndex:  int(w.CardIndex),
		Ciphertext: BytesToBigInt(w.Ciphertext),
		Result:     BytesToBigInt(w.Result),
		Proof:      ZKProofFromWire(w.Proof),
	}
}

// MarshalPayload is a convenience wrapper around proto.Marshal.
func MarshalPayload(m proto.Message) ([]byte, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("MarshalPayload: %w", err)
	}
	return b, nil
}
