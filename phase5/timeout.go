package fault

import (
	"fmt"
	"sync"
	"time"
)

// TimeoutVoteThreshold is the fraction of remaining players that must vote
// before a timeout is confirmed.  2/3 prevents a single malicious peer from
// forcing a fold, while still allowing the game to continue with one dropout.
const TimeoutVoteThreshold = 2.0 / 3.0

// VoteStatus is the result of a timeout vote for one player.
type VoteStatus uint8

const (
	VotePending   VoteStatus = iota // not enough votes yet
	VoteConfirmed                   // majority reached — player is timed out
	VoteRejected                    // majority voted no (or vote expired)
)

// TimeoutVote tracks votes from all peers about one specific player timing out.
type TimeoutVote struct {
	TargetPeerID string
	HandNum      int64
	Votes        map[string]bool // voterID → voted yes
	TotalVoters  int             // total eligible voters (excluding the target)
	Status       VoteStatus
	CreatedAt    time.Time
	ConfirmedAt  time.Time
}

// AddVote records a vote from voterID. Returns the new VoteStatus.
func (tv *TimeoutVote) AddVote(voterID string, yes bool) VoteStatus {
	if tv.Status != VotePending {
		return tv.Status
	}
	tv.Votes[voterID] = yes

	yesCount := 0
	for _, v := range tv.Votes {
		if v {
			yesCount++
		}
	}

	threshold := int(float64(tv.TotalVoters)*TimeoutVoteThreshold + 0.5)
	if threshold < 1 {
		threshold = 1
	}

	if yesCount >= threshold {
		tv.Status = VoteConfirmed
		tv.ConfirmedAt = time.Now()
	} else if len(tv.Votes) == tv.TotalVoters {
		// All votes in and threshold not reached.
		tv.Status = VoteRejected
	}
	return tv.Status
}

// YesCount returns the number of yes votes cast so far.
func (tv *TimeoutVote) YesCount() int {
	n := 0
	for _, v := range tv.Votes {
		if v {
			n++
		}
	}
	return n
}

// TimeoutManager coordinates timeout votes for all players at a table.
// One TimeoutManager runs per hand.
//
// When a player's heartbeat expires, the local node calls StartVote.
// When a TimeoutVote message arrives from a peer, call RecordVote.
// When the vote is confirmed, call ApplyTimeout to mutate the game state.
type TimeoutManager struct {
	mu          sync.Mutex
	handNum     int64
	totalPeers  int
	votes       map[string]*TimeoutVote // targetPeerID → vote
	voteExpiry  time.Duration

	// OnConfirmed is called when a timeout vote reaches majority.
	// The caller should fold the player's hand and remove them from the action order.
	OnConfirmed func(targetPeerID string)
}

// NewTimeoutManager creates a manager for the given hand with totalPeers eligible voters.
// voteExpiry controls how long a vote stays open before it auto-expires (default 30s).
func NewTimeoutManager(handNum int64, totalPeers int, voteExpiry time.Duration) *TimeoutManager {
	if voteExpiry == 0 {
		voteExpiry = 30 * time.Second
	}
	return &TimeoutManager{
		handNum:    handNum,
		totalPeers: totalPeers,
		votes:      make(map[string]*TimeoutVote),
		voteExpiry: voteExpiry,
	}
}

// StartVote opens a new timeout vote for targetPeerID.
// If a vote is already open for this peer, it is a no-op.
// The calling node's own vote (yes) is automatically cast.
func (tm *TimeoutManager) StartVote(targetPeerID, callerPeerID string) *TimeoutVote {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if v, exists := tm.votes[targetPeerID]; exists && v.Status == VotePending {
		return v
	}

	tv := &TimeoutVote{
		TargetPeerID: targetPeerID,
		HandNum:      tm.handNum,
		Votes:        make(map[string]bool),
		TotalVoters:  tm.totalPeers - 1, // exclude the target
		Status:       VotePending,
		CreatedAt:    time.Now(),
	}
	tm.votes[targetPeerID] = tv

	// Cast our own yes vote immediately.
	tv.AddVote(callerPeerID, true)
	if tv.Status == VoteConfirmed && tm.OnConfirmed != nil {
		go tm.OnConfirmed(targetPeerID)
	}
	return tv
}

// RecordVote records an incoming TimeoutVote message from a remote peer.
// Returns an error if no vote is open for targetPeerID.
func (tm *TimeoutManager) RecordVote(targetPeerID, voterPeerID string, yes bool) (VoteStatus, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tv, exists := tm.votes[targetPeerID]
	if !exists {
		// First vote received — open a new vote.
		tv = &TimeoutVote{
			TargetPeerID: targetPeerID,
			HandNum:      tm.handNum,
			Votes:        make(map[string]bool),
			TotalVoters:  tm.totalPeers - 1,
			Status:       VotePending,
			CreatedAt:    time.Now(),
		}
		tm.votes[targetPeerID] = tv
	}
	if tv.Status != VotePending {
		return tv.Status, nil
	}

	status := tv.AddVote(voterPeerID, yes)
	if status == VoteConfirmed && tm.OnConfirmed != nil {
		go tm.OnConfirmed(targetPeerID)
	}
	return status, nil
}

// VoteFor returns the current vote record for a target, or nil.
func (tm *TimeoutManager) VoteFor(targetPeerID string) *TimeoutVote {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.votes[targetPeerID]
}

// ExpireStaleVotes removes votes that have been open longer than voteExpiry.
// Call periodically (e.g. on a 5-second ticker).
func (tm *TimeoutManager) ExpireStaleVotes() []string {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var expired []string
	cutoff := time.Now().Add(-tm.voteExpiry)
	for id, v := range tm.votes {
		if v.Status == VotePending && v.CreatedAt.Before(cutoff) {
			v.Status = VoteRejected
			expired = append(expired, id)
		}
	}
	return expired
}

// Summary returns a human-readable summary of all active votes.
func (tm *TimeoutManager) Summary() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.votes) == 0 {
		return "no timeout votes"
	}
	var out string
	for id, v := range tm.votes {
		out += fmt.Sprintf("  %s: %d/%d votes, status=%v\n",
			id[:min(16, len(id))], v.YesCount(), v.TotalVoters, v.Status)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
