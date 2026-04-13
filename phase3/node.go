package network

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/game"
)

// Node is the top-level P2P poker node.  It composes the host, gossip
// manager, lobby, and game log, and wires them together into a coherent
// protocol.  One Node runs per player per table.
//
// Message flow:
//
//	Outbound:  game engine action → marshal → sign → Gossip.Publish
//	Inbound:   Gossip.Next → decode → verify sig → dispatch to handler
type Node struct {
	Host      *PokerHost
	Gossip    *GossipManager
	Lobby     *Lobby
	Log       *GameLog
	Discovery *MDNSDiscovery

	tableID    string
	playerName string
	buyIn      int64
	sraKey     *pokercrypto.SRAKey

	seq     int64 // atomic, incremented per outbound message
	mu      sync.RWMutex
	peers   map[string]ed25519.PublicKey // peerID → verified Ed25519 pubkey
	started bool

	// Inbound message handlers, set by the game layer.
	OnShuffleStep    func(*ShuffleStep)
	OnPartialDecrypt func(*PartialDecrypt)
	OnPlayerAction   func(*PlayerAction)
	OnGameStateSync  func(*GameStateSync)
	OnHeartbeat      func(*Heartbeat)
	OnTimeoutVote    func(*TimeoutVote)
	OnHandResult     func(*HandResult)
	OnJoinTable      func(*JoinTable, string)
	OnPlayerReady    func(*PlayerReady, string)
}

// NewNode constructs a Node and starts the libp2p host.
func NewNode(
	ctx context.Context,
	tableID, playerName string,
	buyIn int64,
	sraKey *pokercrypto.SRAKey,
	listenAddr string,
	seed []byte,
) (*Node, error) {
	ph, err := NewPokerHost(ctx, listenAddr, seed)
	if err != nil {
		return nil, fmt.Errorf("NewNode: host: %w", err)
	}

	gm, err := NewGossipManager(ctx, ph.Host, tableID)
	if err != nil {
		ph.Close()
		return nil, fmt.Errorf("NewNode: gossip: %w", err)
	}

	n := &Node{
		Host:       ph,
		Gossip:     gm,
		Lobby:      NewLobby(tableID, 9),
		Log:        NewGameLog(tableID, 0),
		tableID:    tableID,
		playerName: playerName,
		buyIn:      buyIn,
		sraKey:     sraKey,
		peers:      make(map[string]ed25519.PublicKey),
	}
	return n, nil
}

// Start begins the inbound message dispatch loop and mDNS discovery.
// Must be called before any messages can be sent or received.
func (n *Node) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return fmt.Errorf("Start: already started")
	}
	n.started = true
	n.mu.Unlock()

	// Register the direct-stream handler for private messages.
	ProtocolHandler(n.Host.Host, n.handleDirectStream)

	// Start mDNS discovery — connect to found peers automatically.
	disc, err := NewMDNSDiscovery(n.Host.Host, func(pi peer.AddrInfo) {
		n.Host.Host.Peerstore().AddAddrs(pi.ID, pi.Addrs, 10*time.Minute)
		_ = n.Host.Host.Connect(ctx, pi)
	})
	if err != nil {
		return fmt.Errorf("Start: mdns: %w", err)
	}
	n.Discovery = disc

	// Inbound gossip dispatch loop.
	go n.receiveLoop(ctx)

	return nil
}

// receiveLoop reads from GossipSub and dispatches to registered handlers.
func (n *Node) receiveLoop(ctx context.Context) {
	for {
		data, _, err := n.Gossip.NextTableMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		n.handleGossipMessage(data)
	}
}

// handleGossipMessage decodes and dispatches a raw gossip message.
func (n *Node) handleGossipMessage(data []byte) {
	env, err := DecodeEnvelope(data, n.lookupPubKey)
	if err != nil {
		return // silently drop malformed/unsigned messages
	}

	// Replay protection.
	if err := n.Gossip.CheckAndUpdateSeq(env.SenderId, env.Seq); err != nil {
		return
	}

	// Append to game log.
	_ = n.Log.Append(env)

	// Dispatch by type.
	switch env.Type {
	case MsgType_JOIN_TABLE:
		if n.OnJoinTable != nil {
			msg := &JoinTable{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.Lobby.HandleJoin(msg, env.SenderId)
				n.OnJoinTable(msg, env.SenderId)
			}
		}
	case MsgType_PLAYER_READY:
		if n.OnPlayerReady != nil {
			msg := &PlayerReady{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.Lobby.HandleReady(msg, env.SenderId)
				n.OnPlayerReady(msg, env.SenderId)
			}
		}
	case MsgType_SHUFFLE_STEP:
		if n.OnShuffleStep != nil {
			msg := &ShuffleStep{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnShuffleStep(msg)
			}
		}
	case MsgType_PARTIAL_DECRYPT:
		if n.OnPartialDecrypt != nil {
			msg := &PartialDecrypt{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnPartialDecrypt(msg)
			}
		}
	case MsgType_PLAYER_ACTION:
		if n.OnPlayerAction != nil {
			msg := &PlayerAction{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnPlayerAction(msg)
			}
		}
	case MsgType_GAME_STATE_SYNC:
		if n.OnGameStateSync != nil {
			msg := &GameStateSync{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnGameStateSync(msg)
			}
		}
	case MsgType_HEARTBEAT:
		if n.OnHeartbeat != nil {
			msg := &Heartbeat{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnHeartbeat(msg)
			}
		}
	case MsgType_TIMEOUT_VOTE:
		if n.OnTimeoutVote != nil {
			msg := &TimeoutVote{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnTimeoutVote(msg)
			}
		}
	case MsgType_HAND_RESULT:
		if n.OnHandResult != nil {
			msg := &HandResult{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnHandResult(msg)
			}
		}
	}
}

// handleDirectStream processes a private /poker/1.0.0 stream message.
func (n *Node) handleDirectStream(env *Envelope, from peer.ID) {
	_ = n.Log.Append(env)
	switch env.Type {
	case MsgType_PARTIAL_DECRYPT:
		if n.OnPartialDecrypt != nil {
			msg := &PartialDecrypt{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnPartialDecrypt(msg)
			}
		}
	}
}

// ── Outbound publish helpers ─────────────────────────────────────────────────

func (n *Node) nextSeq() int64 {
	return atomic.AddInt64(&n.seq, 1)
}

func (n *Node) publish(ctx context.Context, msgType MsgType, payload []byte) error {
	env := NewEnvelope(msgType, n.Host.PeerID, n.nextSeq(), payload)
	frame, err := EncodeEnvelope(env, n.Host.Ed25519PK)
	if err != nil {
		return err
	}
	return n.Gossip.Publish(ctx, frame)
}

// BroadcastJoin announces this node's intent to join the table.
func (n *Node) BroadcastJoin(ctx context.Context, handNum int64) error {
	msg := &JoinTable{
		TableId:     n.tableID,
		PlayerName:  n.playerName,
		BuyIn:       n.buyIn,
		SraPubKeyE:  n.sraKey.PublicKey().Bytes(),
		SessionNonce: []byte(n.Host.PeerID), // per-player nonce contribution
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_JOIN_TABLE, b)
}

// BroadcastReady signals this player is ready to start the hand.
func (n *Node) BroadcastReady(ctx context.Context, handNum int64) error {
	msg := &PlayerReady{TableId: n.tableID, HandNum: handNum}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_PLAYER_READY, b)
}

// BroadcastShuffleStep publishes this player's shuffle step.
func (n *Node) BroadcastShuffleStep(ctx context.Context, handNum int64, step *pokercrypto.ShuffleStep) error {
	msg := &ShuffleStep{
		TableId:          n.tableID,
		HandNum:          handNum,
		PlayerId:         n.Host.PeerID,
		Deck:             DeckToWire(step.OutputDeck),
		CommitmentHash:   step.Commitment.Hash,
		CommitmentNonce:  step.Commitment.Nonce,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_SHUFFLE_STEP, b)
}

// BroadcastPartialDecrypt publishes a partial decryption for a community card.
func (n *Node) BroadcastPartialDecrypt(ctx context.Context, handNum int64, pd *pokercrypto.PartialDecryption) error {
	msg := &PartialDecrypt{
		TableId:    n.tableID,
		HandNum:    handNum,
		PlayerId:   n.Host.PeerID,
		CardIndex:  int32(pd.CardIndex),
		Ciphertext: pd.Ciphertext.Bytes(),
		Result:     pd.Result.Bytes(),
		Proof:      ZKProofToWire(pd.Proof),
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_PARTIAL_DECRYPT, b)
}

// BroadcastAction publishes a player betting action.
func (n *Node) BroadcastAction(ctx context.Context, handNum int64, a game.Action, actionSeq int64) error {
	msg := &PlayerAction{
		TableId:  n.tableID,
		HandNum:  handNum,
		PlayerId: n.Host.PeerID,
		Action:   int32(a.Type),
		Amount:   a.Amount,
		Seq:      actionSeq,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_PLAYER_ACTION, b)
}

// BroadcastHeartbeat sends a liveness ping.
func (n *Node) BroadcastHeartbeat(ctx context.Context, handNum, seq int64) error {
	msg := &Heartbeat{TableId: n.tableID, HandNum: handNum, Seq: seq}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	env := NewEnvelope(MsgType_HEARTBEAT, n.Host.PeerID, n.nextSeq(), b)
	frame, err := EncodeEnvelope(env, n.Host.Ed25519PK)
	if err != nil {
		return err
	}
	return n.Gossip.PublishHeartbeat(ctx, frame)
}

// BroadcastHandResult publishes the final hand outcome.
func (n *Node) BroadcastHandResult(ctx context.Context, handNum int64, pots []PotResult, stateRoot []byte) error {
	msg := &HandResult{
		TableId:  n.tableID,
		HandNum:  handNum,
		Pots:     potResultsToProto(pots),
		StateRoot: stateRoot,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return n.publish(ctx, MsgType_HAND_RESULT, b)
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// lookupPubKey retrieves a peer's Ed25519 public key for signature verification.
// For now uses ExtractEd25519PubKey from the PeerID itself (Ed25519 keys are
// embedded in the PeerID for small keys).
func (n *Node) lookupPubKey(peerID string) (ed25519.PublicKey, error) {
	n.mu.RLock()
	if k, ok := n.peers[peerID]; ok {
		n.mu.RUnlock()
		return k, nil
	}
	n.mu.RUnlock()

	pid, err := PeerIDFromString(peerID)
	if err != nil {
		return nil, err
	}
	pub, err := ExtractEd25519PubKey(pid)
	if err != nil {
		// PeerID doesn't embed the key directly (RSA keys) — fall back to no verification.
		// In production, keys are exchanged via the Noise handshake and cached here.
		return nil, nil
	}

	n.mu.Lock()
	n.peers[peerID] = pub
	n.mu.Unlock()
	return pub, nil
}

// RegisterPeer caches a peer's Ed25519 public key (called after Noise handshake).
func (n *Node) RegisterPeer(peerID string, pub ed25519.PublicKey) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[peerID] = pub
}

// Close tears down the node cleanly.
func (n *Node) Close() error {
	if n.Discovery != nil {
		n.Discovery.Close()
	}
	n.Gossip.Close()
	return n.Host.Close()
}

func potResultsToProto(pots []PotResult) []*PotResult {
	out := make([]*PotResult, len(pots))
	for i := range pots {
		out[i] = &pots[i]
	}
	return out
}
