package fault

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
)

// SlashReason describes why a player is being slashed (penalised on-chain).
type SlashReason uint8

const (
	SlashEquivocation   SlashReason = iota // signed two conflicting messages at same seq
	SlashBadZKProof                        // provided an invalid partial decryption proof
	SlashInvalidAction                     // sent an action that violates the game rules
	SlashKeyWithholding                    // refused to provide a partial decryption
)

func (r SlashReason) String() string {
	return [...]string{
		"Equivocation",
		"Bad ZK Proof",
		"Invalid Action",
		"Key Withholding",
	}[r]
}

// SlashRecord is an immutable record of a single protocol violation.
// It carries enough evidence for the on-chain arbitration contract to verify
// the offence independently.
type SlashRecord struct {
	PeerID     string
	Reason     SlashReason
	HandNum    int64
	DetectedAt time.Time
	Evidence   []byte // serialised proof

	// For equivocation: both conflicting log entries.
	EnvA *LogEntry
	EnvB *LogEntry

	// For bad ZK proof: the card slot that failed.
	BadProofCardIdx int
	BadProofResult  *big.Int
}

func (sr *SlashRecord) String() string {
	short := sr.PeerID
	if len(short) > 16 {
		short = short[:16]
	}
	return fmt.Sprintf("SLASH[%s] peer=%s hand=%d at=%s",
		sr.Reason, short, sr.HandNum, sr.DetectedAt.Format("15:04:05"))
}

// SlashDetector monitors the game log and partial decryptions for protocol violations.
type SlashDetector struct {
	mu      sync.RWMutex
	handNum int64
	records []*SlashRecord
	slashed map[string]bool

	// OnSlash is called (in a goroutine) when a new slash record is created.
	OnSlash func(record *SlashRecord)
}

// NewSlashDetector creates a detector for the given hand.
func NewSlashDetector(handNum int64) *SlashDetector {
	return &SlashDetector{
		handNum: handNum,
		slashed: make(map[string]bool),
	}
}

// CheckEquivocation scans the log for any player that signed two different
// messages with the same sequence number. Returns all slash records found.
func (sd *SlashDetector) CheckEquivocation(log EquivocationChecker) []*SlashRecord {
	senderID, envA, envB := log.DetectEquivocation()
	if senderID == "" {
		return nil
	}
	record := &SlashRecord{
		PeerID:     senderID,
		Reason:     SlashEquivocation,
		HandNum:    sd.handNum,
		DetectedAt: time.Now(),
		EnvA:       envA,
		EnvB:       envB,
	}
	sd.addRecord(record)
	return []*SlashRecord{record}
}

// CheckPartialDecryption verifies a partial decryption ZK proof.
// Returns nil if valid, or a slash record if the proof fails.
func (sd *SlashDetector) CheckPartialDecryption(
	pd *pokercrypto.PartialDecryption,
	prime *big.Int,
	sessionID []byte,
) *SlashRecord {
	if err := pd.Verify(prime, sessionID); err == nil {
		return nil
	}
	record := &SlashRecord{
		PeerID:          pd.PlayerID,
		Reason:          SlashBadZKProof,
		HandNum:         sd.handNum,
		DetectedAt:      time.Now(),
		BadProofCardIdx: pd.CardIndex,
		BadProofResult:  pd.Result,
		Evidence:        pd.Ciphertext.Bytes(),
	}
	sd.addRecord(record)
	return record
}

// CheckInvalidAction records a slash for a rule-violating game action.
func (sd *SlashDetector) CheckInvalidAction(peerID string, errText string) *SlashRecord {
	record := &SlashRecord{
		PeerID:     peerID,
		Reason:     SlashInvalidAction,
		HandNum:    sd.handNum,
		DetectedAt: time.Now(),
		Evidence:   []byte(errText),
	}
	sd.addRecord(record)
	return record
}

// CheckKeyWithholding records a slash when a player refuses to decrypt a card.
func (sd *SlashDetector) CheckKeyWithholding(peerID string, cardIdx int) *SlashRecord {
	record := &SlashRecord{
		PeerID:          peerID,
		Reason:          SlashKeyWithholding,
		HandNum:         sd.handNum,
		DetectedAt:      time.Now(),
		BadProofCardIdx: cardIdx,
	}
	sd.addRecord(record)
	return record
}

// Records returns all slash records for this hand.
func (sd *SlashDetector) Records() []*SlashRecord {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	out := make([]*SlashRecord, len(sd.records))
	copy(out, sd.records)
	return out
}

// IsSlashed returns true if the given peer has been slashed this hand.
func (sd *SlashDetector) IsSlashed(peerID string) bool {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return sd.slashed[peerID]
}

// HasViolations returns true if any slash records exist.
func (sd *SlashDetector) HasViolations() bool {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return len(sd.records) > 0
}

func (sd *SlashDetector) addRecord(record *SlashRecord) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.records = append(sd.records, record)
	sd.slashed[record.PeerID] = true
	if sd.OnSlash != nil {
		go sd.OnSlash(record)
	}
}
