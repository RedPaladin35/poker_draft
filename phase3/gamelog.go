package network

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// GameLog is the append-only, content-addressed log of every signed envelope
// for a single hand. It serves two purposes:
//
//  1. State reconstruction: any peer that missed messages can replay the log
//     in sequence order to arrive at the same game state as everyone else.
//
//  2. Dispute evidence: if two players disagree on the outcome, either can
//     submit their signed log to the on-chain arbitration contract (Phase 6),
//     which re-executes the hand deterministically and pays out accordingly.
//
// The log is NOT a blockchain — it does not have consensus. It is simply each
// node's local record of what it observed. The state root ties it to the
// on-chain settlement.
type GameLog struct {
	mu      sync.RWMutex
	tableID string
	handNum int64
	entries []*Envelope
	byKey   map[string]struct{} // deduplication: "senderID:seq"
}

// NewGameLog creates an empty log for the given hand.
func NewGameLog(tableID string, handNum int64) *GameLog {
	return &GameLog{
		tableID: tableID,
		handNum: handNum,
		byKey:   make(map[string]struct{}),
	}
}

// Append adds an envelope to the log.
// Returns ErrDuplicateEntry if the (senderID, seq) pair already exists.
func (gl *GameLog) Append(env *Envelope) error {
	gl.mu.Lock()
	defer gl.mu.Unlock()

	key := fmt.Sprintf("%s:%d", env.SenderId, env.Seq)
	if _, exists := gl.byKey[key]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateEntry, key)
	}
	gl.entries = append(gl.entries, env)
	gl.byKey[key] = struct{}{}
	return nil
}

// Entries returns a copy of all log entries in insertion order.
func (gl *GameLog) Entries() []*Envelope {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	out := make([]*Envelope, len(gl.entries))
	copy(out, gl.entries)
	return out
}

// Len returns the number of entries in the log.
func (gl *GameLog) Len() int {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	return len(gl.entries)
}

// StateRoot computes SHA-256 over the entire log in insertion order.
// The root is deterministic: two peers with identical logs produce identical roots.
// It is submitted to the on-chain contract as the authoritative game record.
//
// Each entry contributes: type(1) || senderID(var) || seq(8) || payload(var) || signature(var)
func (gl *GameLog) StateRoot() []byte {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	h := sha256.New()
	var seq [8]byte
	for _, env := range gl.entries {
		h.Write([]byte{byte(env.Type)})
		h.Write([]byte(env.SenderId))
		h.Write([]byte{0x00}) // separator
		binary.BigEndian.PutUint64(seq[:], uint64(env.Seq))
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

// DetectEquivocation checks whether any sender signed two different messages
// with the same sequence number — a protocol violation that triggers slashing.
//
// Returns (senderID, envA, envB, nil) if equivocation is found,
// or ("", nil, nil, nil) if the log is clean.
func (gl *GameLog) DetectEquivocation() (string, *Envelope, *Envelope, error) {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	type seqKey struct {
		senderID string
		seq      int64
	}
	seen := make(map[seqKey]*Envelope, len(gl.entries))

	for _, env := range gl.entries {
		k := seqKey{env.SenderId, env.Seq}
		if prev, exists := seen[k]; exists {
			if string(prev.Payload) != string(env.Payload) {
				return env.SenderId, prev, env, nil
			}
		} else {
			seen[k] = env
		}
	}
	return "", nil, nil, nil
}

// ValidateSequences checks that each player's sequence numbers are
// gapless and start from 1. Returns the first gap found, or nil.
// Called before submitting the log to the on-chain contract.
func (gl *GameLog) ValidateSequences(playerIDs []string) error {
	gl.mu.RLock()
	defer gl.mu.RUnlock()

	// Collect sequences per player.
	perPlayer := make(map[string][]int64, len(playerIDs))
	for _, env := range gl.entries {
		perPlayer[env.SenderId] = append(perPlayer[env.SenderId], env.Seq)
	}

	for _, pid := range playerIDs {
		seqs := perPlayer[pid]
		if len(seqs) == 0 {
			continue
		}
		// Sort ascending.
		for i := 1; i < len(seqs); i++ {
			for j := i; j > 0 && seqs[j] < seqs[j-1]; j-- {
				seqs[j], seqs[j-1] = seqs[j-1], seqs[j]
			}
		}
		for i, v := range seqs {
			expected := int64(i + 1)
			if v != expected {
				return fmt.Errorf("player %s: sequence gap — expected %d, got %d", pid, expected, v)
			}
		}
	}
	return nil
}

// EntriesBySender returns all log entries from a specific sender, in order.
func (gl *GameLog) EntriesBySender(senderID string) []*Envelope {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	var out []*Envelope
	for _, env := range gl.entries {
		if env.SenderId == senderID {
			out = append(out, env)
		}
	}
	return out
}

// Sentinel errors.
var (
	ErrDuplicateEntry = errors.New("duplicate log entry")
	ErrEquivocation   = errors.New("equivocation detected")
)
