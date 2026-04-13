package network

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// LobbyState tracks the lifecycle of a table before a hand begins.
type LobbyState int

const (
	LobbyWaiting LobbyState = iota // accepting players
	LobbyReady                     // all seats filled and all players ready
	LobbyPlaying                   // hand in progress — no more joins
)

// SeatInfo holds the per-player metadata collected during the lobby phase.
type SeatInfo struct {
	PlayerID   string
	PlayerName string
	BuyIn      int64
	SRAKeyE    []byte // public SRA encryption exponent (big-endian)
	Nonce      []byte // player's session nonce contribution
	IsReady    bool
	JoinedAt   time.Time
}

// Lobby manages the pre-game table-formation protocol.
// It is safe for concurrent use from the gossip receive loop.
type Lobby struct {
	mu       sync.RWMutex
	tableID  string
	maxSeats int
	seats    map[string]*SeatInfo // keyed by playerID (PeerID)
	state    LobbyState
	readyCh  chan struct{} // closed when all seats are filled and ready
	once     sync.Once    // guards closing readyCh
}

// NewLobby creates a new lobby for the given table.
func NewLobby(tableID string, maxSeats int) *Lobby {
	return &Lobby{
		tableID:  tableID,
		maxSeats: maxSeats,
		seats:    make(map[string]*SeatInfo),
		readyCh:  make(chan struct{}),
	}
}

// HandleJoin processes a JoinTable message from a peer.
// Returns an error if the table is full, the player is already seated,
// or the buy-in is invalid.
func (l *Lobby) HandleJoin(msg *JoinTable, fromPeerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != LobbyWaiting {
		return fmt.Errorf("HandleJoin: table %s not accepting players (state=%d)", l.tableID, l.state)
	}
	if len(l.seats) >= l.maxSeats {
		return fmt.Errorf("HandleJoin: table %s is full (%d/%d)", l.tableID, len(l.seats), l.maxSeats)
	}
	if _, exists := l.seats[fromPeerID]; exists {
		return fmt.Errorf("HandleJoin: player %s already seated", fromPeerID)
	}
	if msg.BuyIn <= 0 {
		return fmt.Errorf("HandleJoin: invalid buy-in %d from %s", msg.BuyIn, fromPeerID)
	}

	l.seats[fromPeerID] = &SeatInfo{
		PlayerID:   fromPeerID,
		PlayerName: msg.PlayerName,
		BuyIn:      msg.BuyIn,
		SRAKeyE:    msg.SraPubKeyE,
		Nonce:      msg.SessionNonce,
		JoinedAt:   time.Now(),
	}
	return nil
}

// HandleReady processes a PlayerReady message.
// If all seats are now filled and ready, closes the readyCh.
func (l *Lobby) HandleReady(msg *PlayerReady, fromPeerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	seat, ok := l.seats[fromPeerID]
	if !ok {
		return fmt.Errorf("HandleReady: player %s not seated", fromPeerID)
	}
	seat.IsReady = true
	l.checkAllReady()
	return nil
}

// checkAllReady signals readyCh if all seats are filled and all players ready.
// Must be called with l.mu held.
func (l *Lobby) checkAllReady() {
	if len(l.seats) < l.maxSeats {
		return
	}
	for _, s := range l.seats {
		if !s.IsReady {
			return
		}
	}
	if l.state == LobbyWaiting {
		l.state = LobbyReady
		l.once.Do(func() { close(l.readyCh) })
	}
}

// WaitReady blocks until all seats are filled and all players are ready,
// or until the context is cancelled.
func (l *Lobby) WaitReady(ctx context.Context) error {
	select {
	case <-l.readyCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("WaitReady: %w", ctx.Err())
	}
}

// Seats returns all currently seated players, sorted by join time.
func (l *Lobby) Seats() []*SeatInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*SeatInfo, 0, len(l.seats))
	for _, s := range l.seats {
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].JoinedAt.Before(out[j].JoinedAt)
	})
	return out
}

// PlayerIDs returns seated player IDs in join order.
// This order is used as the canonical seat order for the game engine.
func (l *Lobby) PlayerIDs() []string {
	seats := l.Seats()
	ids := make([]string, len(seats))
	for i, s := range seats {
		ids[i] = s.PlayerID
	}
	return ids
}

// Count returns the number of currently seated players.
func (l *Lobby) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.seats)
}

// SetPlaying marks the lobby as in-game — no further joins are accepted.
func (l *Lobby) SetPlaying() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = LobbyPlaying
}

// State returns the current lobby state.
func (l *Lobby) State() LobbyState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state
}

// ── Lobby message marshal/unmarshal helpers ───────────────────────────────────

// MarshalJoinTable serialises a JoinTable to bytes.
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
