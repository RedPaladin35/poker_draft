// Package fault implements Phase 5 fault tolerance for the P2P poker engine.
//
// It handles three categories of failure:
//
//  1. Accidental disconnect — heartbeat timeout → distributed fold vote
//  2. Key-recovery disconnect — player disappears during shuffle/deal
//     → Shamir share reconstruction lets others complete their decryption
//  3. Malicious behaviour — equivocation, wrong ZK proof, invalid action
//     → slash flag recorded in the game log for on-chain punishment (Phase 6)
//
// All fault decisions are made collectively: a 2/3-majority TimeoutVote is
// required before a player is removed.  This prevents any single peer from
// silently dropping an opponent.
package fault

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultHeartbeatInterval is how often each node broadcasts a liveness ping.
const DefaultHeartbeatInterval = 5 * time.Second

// DefaultHeartbeatTimeout is how long without a heartbeat before a peer is
// considered potentially disconnected.  Three missed intervals = 15 seconds.
const DefaultHeartbeatTimeout = 15 * time.Second

// PeerStatus represents the last known liveness state of a remote peer.
type PeerStatus uint8

const (
	PeerAlive      PeerStatus = iota // receiving heartbeats normally
	PeerSuspect                      // missed at least one interval
	PeerTimedOut                     // missed DefaultHeartbeatTimeout worth of beats
	PeerDisconnected                 // confirmed gone (voted out by majority)
)

func (s PeerStatus) String() string {
	return [...]string{"Alive", "Suspect", "TimedOut", "Disconnected"}[s]
}

// PeerLiveness tracks heartbeat timing for one remote peer.
type PeerLiveness struct {
	PeerID     string
	Status     PeerStatus
	LastSeen   time.Time
	MissedBeats int
}

// HeartbeatMonitor tracks liveness of all peers at a table.
// It is the single source of truth for "is player X still here?"
//
// Usage:
//
//	monitor := NewHeartbeatMonitor(timeout)
//	monitor.RegisterPeer("alice")
//	monitor.RecordHeartbeat("alice")   // call on every received heartbeat
//	timedOut := monitor.CheckTimeouts() // call periodically
type HeartbeatMonitor struct {
	mu      sync.RWMutex
	peers   map[string]*PeerLiveness
	timeout time.Duration

	// OnTimeout is called (in a goroutine) when a peer crosses the timeout threshold.
	// The caller should initiate a TimeoutVote when this fires.
	OnTimeout func(peerID string)
}

// NewHeartbeatMonitor creates a monitor with the given liveness timeout.
func NewHeartbeatMonitor(timeout time.Duration) *HeartbeatMonitor {
	if timeout == 0 {
		timeout = DefaultHeartbeatTimeout
	}
	return &HeartbeatMonitor{
		peers:   make(map[string]*PeerLiveness),
		timeout: timeout,
	}
}

// RegisterPeer adds a peer to the liveness table, recording its first-seen time.
func (hm *HeartbeatMonitor) RegisterPeer(peerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if _, exists := hm.peers[peerID]; !exists {
		hm.peers[peerID] = &PeerLiveness{
			PeerID:   peerID,
			Status:   PeerAlive,
			LastSeen: time.Now(),
		}
	}
}

// RecordHeartbeat updates the last-seen time for a peer and resets suspect status.
func (hm *HeartbeatMonitor) RecordHeartbeat(peerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	pl, ok := hm.peers[peerID]
	if !ok {
		hm.peers[peerID] = &PeerLiveness{PeerID: peerID}
		pl = hm.peers[peerID]
	}
	pl.LastSeen = time.Now()
	pl.Status = PeerAlive
	pl.MissedBeats = 0
}

// CheckTimeouts scans all peers and returns those that have exceeded the timeout.
// Call this on a ticker (e.g. every DefaultHeartbeatInterval).
// Peers that newly cross the timeout threshold trigger the OnTimeout callback.
func (hm *HeartbeatMonitor) CheckTimeouts() []string {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var timedOut []string
	now := time.Now()

	for _, pl := range hm.peers {
		if pl.Status == PeerDisconnected {
			continue
		}
		elapsed := now.Sub(pl.LastSeen)
		if elapsed >= hm.timeout {
			wasAlive := pl.Status != PeerTimedOut
			pl.Status = PeerTimedOut
			pl.MissedBeats = int(elapsed / DefaultHeartbeatInterval)
			timedOut = append(timedOut, pl.PeerID)
			if wasAlive && hm.OnTimeout != nil {
				peerID := pl.PeerID
				go hm.OnTimeout(peerID)
			}
		} else if elapsed >= DefaultHeartbeatInterval {
			pl.Status = PeerSuspect
			pl.MissedBeats++
		}
	}
	return timedOut
}

// MarkDisconnected marks a peer as confirmed disconnected after a majority vote.
func (hm *HeartbeatMonitor) MarkDisconnected(peerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if pl, ok := hm.peers[peerID]; ok {
		pl.Status = PeerDisconnected
	}
}

// Status returns the current liveness status of a peer.
func (hm *HeartbeatMonitor) Status(peerID string) PeerStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if pl, ok := hm.peers[peerID]; ok {
		return pl.Status
	}
	return PeerTimedOut // unknown peer treated as timed out
}

// AllStatuses returns a snapshot of all peer liveness records.
func (hm *HeartbeatMonitor) AllStatuses() map[string]PeerLiveness {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	out := make(map[string]PeerLiveness, len(hm.peers))
	for k, v := range hm.peers {
		out[k] = *v
	}
	return out
}

// AlivePeers returns the IDs of all peers currently considered alive.
func (hm *HeartbeatMonitor) AlivePeers() []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	var out []string
	for id, pl := range hm.peers {
		if pl.Status == PeerAlive || pl.Status == PeerSuspect {
			out = append(out, id)
		}
	}
	return out
}

// Run starts the background heartbeat check loop, firing CheckTimeouts every
// interval until the context is cancelled.
func (hm *HeartbeatMonitor) Run(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = DefaultHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hm.CheckTimeouts()
		}
	}
}

// HeartbeatSender broadcasts periodic liveness pings on behalf of the local node.
// The send function is provided by the caller (typically Node.BroadcastHeartbeat).
type HeartbeatSender struct {
	peerID   string
	interval time.Duration
	seq      int64
	send     func(seq int64) error
}

// NewHeartbeatSender creates a sender that calls send every interval.
func NewHeartbeatSender(peerID string, interval time.Duration, send func(seq int64) error) *HeartbeatSender {
	if interval == 0 {
		interval = DefaultHeartbeatInterval
	}
	return &HeartbeatSender{
		peerID:   peerID,
		interval: interval,
		send:     send,
	}
}

// Run starts sending heartbeats until the context is cancelled.
func (hs *HeartbeatSender) Run(ctx context.Context) error {
	ticker := time.NewTicker(hs.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			hs.seq++
			if err := hs.send(hs.seq); err != nil {
				return fmt.Errorf("HeartbeatSender: %w", err)
			}
		}
	}
}
