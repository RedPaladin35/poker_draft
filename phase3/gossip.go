package network

import (
	"context"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// topicName returns the GossipSub topic for a table's game messages.
// Each table gets its own scoped topic so multiple simultaneous games coexist.
func topicName(tableID string) string { return "poker/table/" + tableID }

// heartbeatTopicName returns the GossipSub topic for liveness heartbeats.
// Heartbeats are separated from game messages so a slow game loop never
// blocks the liveness detector.
func heartbeatTopicName(tableID string) string { return "poker/heartbeat/" + tableID }

// GossipManager manages GossipSub pub/sub for one poker table.
//
// All public game messages (actions, shuffle steps, partial decryptions) are
// published on the table topic and received by every peer in the mesh.
// Heartbeats have their own lower-traffic topic.
//
// Replay protection: CheckAndUpdateSeq must be called on every received
// envelope before processing — it rejects any message whose seq number is
// not strictly greater than the last seen from that sender.
type GossipManager struct {
	ps             *pubsub.PubSub
	tableID        string
	tableTopic     *pubsub.Topic
	heartbeatTopic *pubsub.Topic
	tableSub       *pubsub.Subscription
	heartbeatSub   *pubsub.Subscription
	mu             sync.Mutex
	seqNums        map[string]int64 // last accepted seq per senderID
}

// NewGossipManager creates a GossipSub instance, joins the table and heartbeat
// topics, and subscribes to both. Must be called after the host has at least
// one connection so the mesh can form.
func NewGossipManager(ctx context.Context, h host.Host, tableID string) (*GossipManager, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: gossipsub init: %w", err)
	}

	tt, err := ps.Join(topicName(tableID))
	if err != nil {
		return nil, fmt.Errorf("NewGossipManager: join table topic: %w", err)
	}
	ht, err := ps.Join(heartbeatTopicName(tableID))
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

// Publish broadcasts a signed, framed envelope to all table peers.
func (gm *GossipManager) Publish(ctx context.Context, frame []byte) error {
	if err := gm.tableTopic.Publish(ctx, frame); err != nil {
		return fmt.Errorf("GossipManager.Publish: %w", err)
	}
	return nil
}

// PublishHeartbeat broadcasts a heartbeat on the dedicated heartbeat topic.
func (gm *GossipManager) PublishHeartbeat(ctx context.Context, frame []byte) error {
	if err := gm.heartbeatTopic.Publish(ctx, frame); err != nil {
		return fmt.Errorf("GossipManager.PublishHeartbeat: %w", err)
	}
	return nil
}

// NextTableMessage blocks until the next table message arrives from any peer.
// Returns the raw framed bytes and the sender's libp2p PeerID.
func (gm *GossipManager) NextTableMessage(ctx context.Context) ([]byte, peer.ID, error) {
	msg, err := gm.tableSub.Next(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("GossipManager.NextTableMessage: %w", err)
	}
	return msg.Data, msg.ReceivedFrom, nil
}

// NextHeartbeat blocks until the next heartbeat message arrives.
func (gm *GossipManager) NextHeartbeat(ctx context.Context) ([]byte, peer.ID, error) {
	msg, err := gm.heartbeatSub.Next(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("GossipManager.NextHeartbeat: %w", err)
	}
	return msg.Data, msg.ReceivedFrom, nil
}

// CheckAndUpdateSeq validates the envelope sequence number for replay protection.
// A message is accepted only if seq > last seen seq from senderID.
// Returns nil on success, an error if the message is a replay or duplicate.
func (gm *GossipManager) CheckAndUpdateSeq(senderID string, seq int64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	last, exists := gm.seqNums[senderID]
	if exists && seq <= last {
		return fmt.Errorf("replay detected from %s: seq %d <= last %d", senderID, seq, last)
	}
	gm.seqNums[senderID] = seq
	return nil
}

// Peers returns the current set of peers subscribed to the table topic.
func (gm *GossipManager) Peers() []peer.ID {
	return gm.tableTopic.ListPeers()
}

// Close cancels all subscriptions and leaves both topics.
func (gm *GossipManager) Close() error {
	gm.tableSub.Cancel()
	gm.heartbeatSub.Cancel()
	_ = gm.tableTopic.Close()
	_ = gm.heartbeatTopic.Close()
	return nil
}
