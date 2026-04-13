package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// GameLog is an append-only, deterministically ordered log of all signed
// envelopes for a single hand.  Any peer can reconstruct the full game
// state by replaying entries in sequence-number order.
//
// The log is the source of truth for dispute resolution: if two peers
// disagree on the outcome, they submit their logs to the smart contract
// which re-executes the hand deterministically.
type GameLog struct {
	mu      sync.RWMutex
	tableID string
	handNum int64
	entries []*Envelope   // ordered by (senderID, seq)
	byKey   map[string]struct{} // dedup key: "senderID:seq"
}

// NewGameLog creates an empty game log for the given hand.
func NewGameLog(tableID string, handNum int64) *GameLog {
	return &GameLog{
		tableID: tableID,
		handNum: handNum,
		byKey:   make(map[string]struct{}),
	}
}

// Append adds an envelope to the log.
// Returns an error if the envelope is a duplicate (same sender + seq).
func (gl *GameLog) Append(env *Envelope) error {
	gl.mu.Lock()
	defer gl.mu.Unlock()

	key := fmt.Sprintf("%s:%d", env.SenderId, env.Seq)
	if _, exists := gl.byKey[key]; exists {
		return fmt.Errorf("GameLog.Append: duplicate entry %s", key)
	}
	gl.entries = append(gl.entries, env)
	gl.byKey[key] = struct{}{}
	return nil
}

// Entries returns a copy of all log entries, sorted by (timestamp, sender, seq).
func (gl *GameLog) Entries() []*Envelope {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	out := make([]*Envelope, len(gl.entries))
	copy(out, gl.entries)
	return out
}

// StateRoot computes a SHA-256 hash over all entries in insertion order.
// This root is included in HandResult messages and verified on-chain.
func (gl *GameLog) StateRoot() []byte {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	h := sha256.New()
	for _, env := range gl.entries {
		// Hash: type|senderID|seq|payload|signature
		h.Write([]byte{byte(env.Type)})
		h.Write([]byte(env.SenderId))
		var seq [8]byte
		seq[0] = byte(env.Seq >> 56)
		seq[1] = byte(env.Seq >> 48)
		seq[2] = byte(env.Seq >> 40)
		seq[3] = byte(env.Seq >> 32)
		seq[4] = byte(env.Seq >> 24)
		seq[5] = byte(env.Seq >> 16)
		seq[6] = byte(env.Seq >> 8)
		seq[7] = byte(env.Seq)
		h.Write(seq[:])
		h.Write(env.Payload)
		h.Write(env.Signature)
	}
	return h.Sum(nil)
}

// StateRootHex returns the hex-encoded state root.
func (gl *GameLog) StateRootHex() string {
	return hex.EncodeToString(gl.StateRoot())
}

// Len returns the number of entries in the log.
func (gl *GameLog) Len() int {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	return len(gl.entries)
}

// DetectEquivocation checks whether any sender has signed two different
// envelopes with the same sequence number (a protocol violation).
// Returns the offending sender ID and both conflicting envelopes, or nil.
func (gl *GameLog) DetectEquivocation() (string, *Envelope, *Envelope, error) {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	type key struct {
		senderID string
		seq      int64
	}
	seen := make(map[key]*Envelope)
	for _, env := range gl.entries {
		k := key{env.SenderId, env.Seq}
		if prev, exists := seen[k]; exists {
			// Same sender, same seq — check if payloads differ.
			if string(prev.Payload) != string(env.Payload) {
				return env.SenderId, prev, env, nil
			}
		} else {
			seen[k] = env
		}
	}
	return "", nil, nil, nil
}

// ValidateLog checks that all entries form a gapless sequence per sender.
// Returns an error if any sender has a gap in their sequence numbers.
func (gl *GameLog) ValidateLog(playerIDs []string) error {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	// Build per-player sequence.
	seqs := make(map[string][]int64)
	for _, env := range gl.entries {
		seqs[env.SenderId] = append(seqs[env.SenderId], env.Seq)
	}

	for _, pid := range playerIDs {
		s := seqs[pid]
		if len(s) == 0 {
			continue
		}
		// Sort.
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j] < s[j-1]; j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
		// Check gapless starting from 1.
		for i, v := range s {
			if v != int64(i+1) {
				return fmt.Errorf("ValidateLog: player %s has gap at seq %d (expected %d)", pid, v, i+1)
			}
		}
	}
	return nil
}

// ErrEquivocation is returned when a player signs two conflicting messages.
var ErrEquivocation = errors.New("equivocation detected")
