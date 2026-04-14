package fault

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/game"
)

// FaultManager is the single entry point for all Phase 5 fault-tolerance logic.
// It composes HeartbeatMonitor, TimeoutManager, KeyShareStore, and SlashDetector.
//
// Lifecycle:
//
//	fm := NewFaultManager(localPeerID, handNum, cfg)
//	fm.RegisterPlayers(playerIDs)              // at table formation
//	fm.RecordHeartbeat(peerID)                 // on every heartbeat received
//	fm.HandleTimeoutVote(target, voter, yes)   // on TimeoutVote message
//	fm.StoreKeyShare(ownerID, share)           // on share received
//	fm.AddReconstructionShare(ownerID, share)  // when collecting for recovery
//	fm.TryReconstructKey(ownerID)              // attempt recovery
//	fm.CheckZKProof(pd, prime, sessionID)      // on every partial decryption
//	fm.CheckEquivocation(log)                  // after hand settles
type FaultManager struct {
	mu            sync.RWMutex
	cfg           FaultConfig
	handNum       int64
	localPeerID   string
	playerIDs     []string
	heartbeat     *HeartbeatMonitor
	timeouts      *TimeoutManager
	keyShares     *KeyShareStore
	slashDetector *SlashDetector

	// Callbacks — set by the game layer before starting.

	// OnPlayerFolded is called when a timeout vote confirms a player should be
	// auto-folded. The game layer must call game.Machine.ApplyAction with Fold.
	OnPlayerFolded func(peerID string)

	// OnKeyShareNeeded is called when we should broadcast our share for ownerID.
	OnKeyShareNeeded func(ownerID string, share pokercrypto.ShamirShare)

	// OnSlash is called when a protocol violation is detected.
	OnSlash func(record *SlashRecord)

	// OnTimeoutVoteNeeded is called when we detect a peer has timed out.
	OnTimeoutVoteNeeded func(targetPeerID string)
}

// FaultConfig holds tunable parameters for the fault manager.
type FaultConfig struct {
	HeartbeatInterval time.Duration // how often to expect heartbeats (default 5s)
	HeartbeatTimeout  time.Duration // missing for this long = timed out (default 15s)
	VoteExpiry        time.Duration // timeout vote stays open for (default 30s)
	ShamirThreshold   int           // 0 = auto (ceil N/2)
	Prime             *big.Int      // SRA field prime
}

// NewFaultManager creates a FaultManager. localPeerID is this node's own PeerID.
func NewFaultManager(localPeerID string, handNum int64, cfg FaultConfig) *FaultManager {
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.VoteExpiry == 0 {
		cfg.VoteExpiry = 30 * time.Second
	}
	if cfg.Prime == nil {
		cfg.Prime = pokercrypto.SharedPrime()
	}

	fm := &FaultManager{
		cfg:           cfg,
		handNum:       handNum,
		localPeerID:   localPeerID,
		heartbeat:     NewHeartbeatMonitor(cfg.HeartbeatTimeout),
		keyShares:     NewKeyShareStore(cfg.Prime),
		slashDetector: NewSlashDetector(handNum),
	}

	// Wire heartbeat timeouts → trigger a TimeoutVote.
	fm.heartbeat.OnTimeout = func(peerID string) {
		if fm.OnTimeoutVoteNeeded != nil {
			fm.OnTimeoutVoteNeeded(peerID)
		}
		fm.mu.RLock()
		tm := fm.timeouts
		fm.mu.RUnlock()
		if tm != nil {
			tm.StartVote(peerID, localPeerID)
		}
	}

	return fm
}

// RegisterPlayers registers all players and initialises the timeout manager.
// Must be called once the lobby is complete and the player list is known.
func (fm *FaultManager) RegisterPlayers(playerIDs []string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	fm.playerIDs = playerIDs
	for _, id := range playerIDs {
		if id != fm.localPeerID {
			fm.heartbeat.RegisterPeer(id)
		}
	}

	n := len(playerIDs)
	fm.timeouts = NewTimeoutManager(fm.handNum, n, fm.cfg.VoteExpiry)
	fm.timeouts.OnConfirmed = func(targetPeerID string) {
		fm.heartbeat.MarkDisconnected(targetPeerID)
		if fm.OnPlayerFolded != nil {
			fm.OnPlayerFolded(targetPeerID)
		}
	}

	if fm.cfg.ShamirThreshold == 0 {
		fm.cfg.ShamirThreshold = (n + 1) / 2 // ceil(N/2)
		if fm.cfg.ShamirThreshold < 2 {
			fm.cfg.ShamirThreshold = 2
		}
	}
}

// RecordHeartbeat updates liveness for peerID. Call on every heartbeat received.
func (fm *FaultManager) RecordHeartbeat(peerID string) {
	fm.heartbeat.RecordHeartbeat(peerID)
}

// HandleTimeoutVote processes an incoming vote about a peer timing out.
func (fm *FaultManager) HandleTimeoutVote(targetPeerID, voterPeerID string, yes bool) (VoteStatus, error) {
	fm.mu.RLock()
	tm := fm.timeouts
	fm.mu.RUnlock()
	if tm == nil {
		return VotePending, fmt.Errorf("HandleTimeoutVote: timeout manager not initialised")
	}
	return tm.RecordVote(targetPeerID, voterPeerID, yes)
}

// StartTimeoutVote initiates a vote to fold targetPeerID.
func (fm *FaultManager) StartTimeoutVote(targetPeerID string) {
	fm.mu.RLock()
	tm := fm.timeouts
	fm.mu.RUnlock()
	if tm != nil {
		tm.StartVote(targetPeerID, fm.localPeerID)
	}
	if fm.OnTimeoutVoteNeeded != nil {
		fm.OnTimeoutVoteNeeded(targetPeerID)
	}
}

// ── Key share management ──────────────────────────────────────────────────────

// StoreKeyShare records a Shamir share received from ownerID.
func (fm *FaultManager) StoreKeyShare(ownerID string, share pokercrypto.ShamirShare) {
	fm.keyShares.StoreMyShare(ownerID, share)
}

// BroadcastMyShareFor signals we should contribute our share for ownerID.
func (fm *FaultManager) BroadcastMyShareFor(ownerID string) {
	share, ok := fm.keyShares.ContributeShare(ownerID)
	if !ok {
		return
	}
	if fm.OnKeyShareNeeded != nil {
		fm.OnKeyShareNeeded(ownerID, share)
	}
}

// AddReconstructionShare records a contributed share from another peer.
func (fm *FaultManager) AddReconstructionShare(ownerID string, share pokercrypto.ShamirShare) {
	fm.keyShares.AddReconstructionShare(ownerID, share)
}

// TryReconstructKey attempts to rebuild the SRAKey for ownerID.
// Returns nil, false if not enough shares have been collected yet.
func (fm *FaultManager) TryReconstructKey(ownerID string) (*pokercrypto.SRAKey, bool) {
	fm.mu.RLock()
	threshold := fm.cfg.ShamirThreshold
	fm.mu.RUnlock()

	if !fm.keyShares.CanReconstruct(ownerID, threshold) {
		return nil, false
	}
	key, err := fm.keyShares.ReconstructSRAKey(ownerID, threshold)
	if err != nil {
		return nil, false
	}
	return key, true
}

// ── Slash detection ───────────────────────────────────────────────────────────

// CheckZKProof verifies a partial decryption proof and slashes on failure.
func (fm *FaultManager) CheckZKProof(
	pd *pokercrypto.PartialDecryption,
	prime *big.Int,
	sessionID []byte,
) *SlashRecord {
	record := fm.slashDetector.CheckPartialDecryption(pd, prime, sessionID)
	if record != nil && fm.OnSlash != nil {
		go fm.OnSlash(record)
	}
	return record
}

// CheckEquivocation scans the log for conflicting signed messages.
// log must implement EquivocationChecker (satisfied by network.GameLog).
func (fm *FaultManager) CheckEquivocation(log EquivocationChecker) []*SlashRecord {
	records := fm.slashDetector.CheckEquivocation(log)
	if len(records) > 0 && fm.OnSlash != nil {
		for _, r := range records {
			go fm.OnSlash(r)
		}
	}
	return records
}

// RecordInvalidAction slashes a player for sending a rule-violating action.
func (fm *FaultManager) RecordInvalidAction(peerID, errText string) *SlashRecord {
	record := fm.slashDetector.CheckInvalidAction(peerID, errText)
	if fm.OnSlash != nil {
		go fm.OnSlash(record)
	}
	return record
}

// RecordKeyWithholding slashes a player for refusing to decrypt a required card.
func (fm *FaultManager) RecordKeyWithholding(peerID string, cardIdx int) *SlashRecord {
	record := fm.slashDetector.CheckKeyWithholding(peerID, cardIdx)
	if fm.OnSlash != nil {
		go fm.OnSlash(record)
	}
	return record
}

// SlashRecords returns all recorded protocol violations for this hand.
func (fm *FaultManager) SlashRecords() []*SlashRecord {
	return fm.slashDetector.Records()
}

// IsSlashed returns true if the peer has been flagged for protocol violations.
func (fm *FaultManager) IsSlashed(peerID string) bool {
	return fm.slashDetector.IsSlashed(peerID)
}

// ── Game state integration ────────────────────────────────────────────────────

// ApplyTimeoutFold creates a fold action for a disconnected player.
// The caller passes this to game.Machine.ApplyAction and broadcasts it.
func ApplyTimeoutFold(gs *game.GameState, peerID string) (game.Action, error) {
	idx := gs.SeatIndex(peerID)
	if idx == -1 {
		return game.Action{}, fmt.Errorf("ApplyTimeoutFold: player %s not found", peerID)
	}
	p := gs.Players[idx]
	if p.Status == game.StatusFolded || p.Status == game.StatusSittingOut {
		return game.Action{}, fmt.Errorf("ApplyTimeoutFold: player %s already folded/out", peerID)
	}
	return game.Action{PlayerID: peerID, Type: game.ActionFold}, nil
}

// Run starts the background heartbeat check loop.
// Call in a goroutine; cancel the context to stop.
func (fm *FaultManager) Run(ctx context.Context) {
	fm.heartbeat.Run(ctx, fm.cfg.HeartbeatInterval)
}

// PeerStatus returns the current liveness status of a remote peer.
func (fm *FaultManager) PeerStatus(peerID string) PeerStatus {
	return fm.heartbeat.Status(peerID)
}

// LivePeers returns the IDs of all currently alive peers.
func (fm *FaultManager) LivePeers() []string {
	return fm.heartbeat.AlivePeers()
}
