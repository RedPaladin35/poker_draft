package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// LobbyState tracks which players have joined and are ready for a hand.
type LobbyState int

const (
	LobbyWaiting LobbyState = iota // waiting for players to join
	LobbyReady                     // all seats filled, ready to start
	LobbyPlaying                   // hand in progress
)

// SeatInfo holds information about a player at the table.
type SeatInfo struct {
	PlayerID   string
	PlayerName string
	BuyIn      int64
	SRAKeyE    []byte // public SRA encryption exponent
	IsReady    bool
	JoinedAt   time.Time
}

// Lobby manages the pre-game table formation protocol.
// It tracks who has joined, verifies buy-ins, and signals when the
// table is full and all players are ready.
type Lobby struct {
	mu       sync.RWMutex
	tableID  string
	maxSeats int
	seats    map[string]*SeatInfo // keyed by playerID
	state    LobbyState
	readyCh  chan struct{} // closed when all seats are ready
}

// NewLobby creates a lobby for the given tableID with a maximum seat count.
func NewLobby(tableID string, maxSeats int) *Lobby {
	return &Lobby{
		tableID:  tableID,
		maxSeats: maxSeats,
		seats:    make(map[string]*SeatInfo),
		readyCh:  make(chan struct{}),
	}
}

// HandleJoin processes a JoinTable message from a peer.
func (l *Lobby) HandleJoin(msg *JoinTable, fromPeerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != LobbyWaiting {
		return fmt.Errorf("HandleJoin: table %s is not accepting players (state=%d)", l.tableID, l.state)
	}
	if len(l.seats) >= l.maxSeats {
		return fmt.Errorf("HandleJoin: table %s is full", l.tableID)
	}
	if _, exists := l.seats[fromPeerID]; exists {
		return fmt.Errorf("HandleJoin: player %s already seated", fromPeerID)
	}
	if msg.BuyIn <= 0 {
		return fmt.Errorf("HandleJoin: invalid buy-in %d", msg.BuyIn)
	}

	l.seats[fromPeerID] = &SeatInfo{
		PlayerID:   fromPeerID,
		PlayerName: msg.PlayerName,
		BuyIn:      msg.BuyIn,
		SRAKeyE:    msg.SraPubKeyE,
		JoinedAt:   time.Now(),
	}
	return nil
}

// HandleReady processes a PlayerReady message.
func (l *Lobby) HandleReady(msg *PlayerReady, fromPeerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	seat, ok := l.seats[fromPeerID]
	if !ok {
		return fmt.Errorf("HandleReady: player %s not seated", fromPeerID)
	}
	seat.IsReady = true

	// Check if all seats are filled and ready.
	if len(l.seats) == l.maxSeats {
		allReady := true
		for _, s := range l.seats {
			if !s.IsReady {
				allReady = false
				break
			}
		}
		if allReady && l.state == LobbyWaiting {
			l.state = LobbyReady
			close(l.readyCh)
		}
	}
	return nil
}

// WaitReady blocks until all seats are filled and all players are ready,
// or until the context is cancelled.
func (l *Lobby) WaitReady(ctx context.Context) error {
	select {
	case <-l.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Seats returns a snapshot of all current seats in join order.
func (l *Lobby) Seats() []*SeatInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*SeatInfo, 0, len(l.seats))
	for _, s := range l.seats {
		out = append(out, s)
	}
	return out
}

// PlayerIDs returns the ordered list of player IDs (sorted by join time).
func (l *Lobby) PlayerIDs() []string {
	seats := l.Seats()
	// Sort by join time for deterministic seat order.
	for i := 1; i < len(seats); i++ {
		for j := i; j > 0 && seats[j].JoinedAt.Before(seats[j-1].JoinedAt); j-- {
			seats[j], seats[j-1] = seats[j-1], seats[j]
		}
	}
	ids := make([]string, len(seats))
	for i, s := range seats {
		ids[i] = s.PlayerID
	}
	return ids
}

// SetPlaying marks the lobby as in-game (no more joins).
func (l *Lobby) SetPlaying() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = LobbyPlaying
}

// Count returns the current number of seated players.
func (l *Lobby) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.seats)
}

// ── Lobby message helpers ────────────────────────────────────────────────────

// MarshalJoinTable serialises a JoinTable message to bytes.
func MarshalJoinTable(msg *JoinTable) ([]byte, error) {
	b, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("MarshalJoinTable: %w", err)
	}
	return b, nil
}

// UnmarshalJoinTable deserialises a JoinTable from bytes.
func UnmarshalJoinTable(b []byte) (*JoinTable, error) {
	msg := &JoinTable{}
	if err := proto.Unmarshal(b, msg); err != nil {
		return nil, fmt.Errorf("UnmarshalJoinTable: %w", err)
	}
	return msg, nil
}

// MarshalPlayerReady serialises a PlayerReady message.
func MarshalPlayerReady(msg *PlayerReady) ([]byte, error) {
	b, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("MarshalPlayerReady: %w", err)
	}
	return b, nil
}

// UnmarshalPlayerReady deserialises a PlayerReady message.
func UnmarshalPlayerReady(b []byte) (*PlayerReady, error) {
	msg := &PlayerReady{}
	if err := proto.Unmarshal(b, msg); err != nil {
		return nil, fmt.Errorf("UnmarshalPlayerReady: %w", err)
	}
	return msg, nil
}
