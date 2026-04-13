package network

import (
	"context"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Topic name conventions.
// Each table gets its own scoped topic so multiple games can coexist.
func tableTopic(tableID string) string     { return "poker/table/" + tableID }
func heartbeatTopic(tableID string) string { return "poker/heartbeat/" + tableID }

// GossipManager manages GossipSub pub/sub for a poker table.
// All game messages (actions, shuffle steps, partial decryptions) are
// published on the table topic.  Heartbeats have their own topic.
type GossipManager struct {
	ps            *pubsub.PubSub
	tableID       string
	tableTopic    *pubsub.Topic
	heartbeatTopic *pubsub.Topic
	tableSub      *pubsub.Subscription
	heartbeatSub  *pubsub.Subscription
	mu            sync.Mutex
	seqNums       map[string]int64 // last seen seq per peerID (replay protection)
}

// NewGossipManager creates a GossipSub manager and joins the table topics.
func NewGossipManager(ctx context.Context, h host.Host, tableID string) (*GossipManager, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: gossipsub: %w", err)
	}

	tt, err := ps.Join(tableTopic(tableID))
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: join table topic: %w", err)
	}
	ht, err := ps.Join(heartbeatTopic(tableID))
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: join heartbeat topic: %w", err)
	}

	ts, err := tt.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: subscribe table: %w", err)
	}
	hs, err := ht.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: subscribe heartbeat: %w", err)
	}

	return &GossipManager{
		ps:             ps,
		tableID:        tableID,
		tableTopic:     tt,
		heartbeatTopic: ht,
		tableSub:       ts,
		heartbeatSub:   hs,
		seqNums:        make(map[string]int64),
	}, nil
}

// Publish broadcasts a signed envelope to all table peers.
func (gm *GossipManager) Publish(ctx context.Context, data []byte) error {
	if err := gm.tableTopic.Publish(ctx, data); err != nil {
		return fmt.Errorf("Publish: %w", err)
	}
	return nil
}

// PublishHeartbeat broadcasts a heartbeat on the heartbeat topic.
func (gm *GossipManager) PublishHeartbeat(ctx context.Context, data []byte) error {
	if err := gm.heartbeatTopic.Publish(ctx, data); err != nil {
		return fmt.Errorf("PublishHeartbeat: %w", err)
	}
	return nil
}

// NextTableMessage blocks until the next table message arrives.
// Returns raw framed bytes ready for DecodeEnvelope.
func (gm *GossipManager) NextTableMessage(ctx context.Context) ([]byte, peer.ID, error) {
	msg, err := gm.tableSub.Next(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("NextTableMessage: %w", err)
	}
	return msg.Data, msg.ReceivedFrom, nil
}

// NextHeartbeat blocks until the next heartbeat message arrives.
func (gm *GossipManager) NextHeartbeat(ctx context.Context) ([]byte, peer.ID, error) {
	msg, err := gm.heartbeatSub.Next(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("NextHeartbeat: %w", err)
	}
	return msg.Data, msg.ReceivedFrom, nil
}

// CheckAndUpdateSeq validates the sequence number for replay protection.
// Returns an error if the seq is not strictly greater than the last seen.
func (gm *GossipManager) CheckAndUpdateSeq(senderID string, seq int64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	last, ok := gm.seqNums[senderID]
	if ok && seq <= last {
		return fmt.Errorf("CheckAndUpdateSeq: replay detected from %s (seq %d <= %d)", senderID, seq, last)
	}
	gm.seqNums[senderID] = seq
	return nil
}

// Peers returns the current set of peers in the table topic.
func (gm *GossipManager) Peers() []peer.ID {
	return gm.tableTopic.ListPeers()
}

// Close cancels all subscriptions and leaves topics.
func (gm *GossipManager) Close() error {
	gm.tableSub.Cancel()
	gm.heartbeatSub.Cancel()
	_ = gm.tableTopic.Close()
	_ = gm.heartbeatTopic.Close()
	return nil
}
