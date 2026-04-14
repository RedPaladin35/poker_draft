package fault

// LogEntry is the minimal representation of a signed network message
// that the fault package needs for equivocation detection.
// This mirrors the fields of network.Envelope without importing the
// network package (which requires libp2p).
type LogEntry struct {
	SenderID  string
	Seq       int64
	Payload   []byte
	Signature []byte
}

// EquivocationChecker is the interface the fault package expects from a
// game log — implemented by network.GameLog in Phase 3.
// Using an interface here breaks the circular dependency:
//
//	fault → network → (libp2p, protobuf) -- avoided
//	fault uses interface → network.GameLog implements it
type EquivocationChecker interface {
	// DetectEquivocation returns the offending senderID and both conflicting
	// log entries, or empty strings / nil if the log is clean.
	DetectEquivocation() (senderID string, a, b *LogEntry)
}
