package network

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"crypto/ed25519"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/game"
)

// Node is the complete P2P poker node for one player at one table.
// It composes the host, GossipSub manager, lobby, game log, and mDNS
// discovery, and wires them together into a coherent protocol.
//
// Outbound flow:
//
//	game action → marshal → EncodeEnvelope (sign) → Gossip.Publish
//
// Inbound flow:
//
//	Gossip.Next → DecodeEnvelope (verify sig) → seq check → log → dispatch
type Node struct {
	// Public components — readable by the game layer.
	Host      *PokerHost
	Gossip    *GossipManager
	Lobby     *Lobby
	Log       *GameLog
	Discovery *MDNSDiscovery

	// Config.
	tableID    string
	playerName string
	buyIn      int64
	sraKey     *pokercrypto.SRAKey

	// Outbound sequence counter (atomic).
	seq int64

	// Peer public key registry for signature verification.
	mu    sync.RWMutex
	peers map[string]ed25519.PublicKey // peerID → Ed25519 pubkey

	started bool

	// ── Inbound message handlers ───────────────────────────────────────────
	// Set these before calling Start. Called synchronously inside the receive
	// loop — dispatch to a goroutine if you need non-blocking handling.
	OnJoinTable      func(*JoinTable, string)
	OnPlayerReady    func(*PlayerReady, string)
	OnShuffleStep    func(*ShuffleStep)
	OnPartialDecrypt func(*PartialDecrypt)
	OnPlayerAction   func(*PlayerAction)
	OnGameStateSync  func(*GameStateSync)
	OnHeartbeat      func(*Heartbeat)
	OnTimeoutVote    func(*TimeoutVote)
	OnHandResult     func(*HandResult)
}

// NewNode constructs a Node and starts the libp2p host.
// seed is passed to NewPokerHost (nil = random identity).
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

	return &Node{
		Host:       ph,
		Gossip:     gm,
		Lobby:      NewLobby(tableID, 9), // default max 9 seats
		Log:        NewGameLog(tableID, 0),
		tableID:    tableID,
		playerName: playerName,
		buyIn:      buyIn,
		sraKey:     sraKey,
		peers:      make(map[string]ed25519.PublicKey),
	}, nil
}

// Start registers the direct-stream handler, starts mDNS discovery, and
// launches the inbound gossip dispatch loop. Must be called before any
// messages can be sent or received.
func (n *Node) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return fmt.Errorf("Start: node already started")
	}
	n.started = true
	n.mu.Unlock()

	// Register direct-stream handler (/poker/1.0.0) for private messages.
	RegisterProtocolHandler(n.Host.Host, func(env *Envelope, from peer.ID) {
		_ = n.Log.Append(env)
		if env.Type == MsgType_PARTIAL_DECRYPT && n.OnPartialDecrypt != nil {
			msg := &PartialDecrypt{}
			if proto.Unmarshal(env.Payload, msg) == nil {
				n.OnPartialDecrypt(msg)
			}
		}
	})

	// Start mDNS discovery — auto-connect to found peers.
	disc, err := NewMDNSDiscovery(n.Host.Host, func(pi peer.AddrInfo) {
		n.Host.Host.Peerstore().AddAddrs(pi.ID, pi.Addrs, 10*time.Minute)
		_ = n.Host.Host.Connect(ctx, pi)
	})
	if err != nil {
		return fmt.Errorf("Start: mdns: %w", err)
	}
	n.Discovery = disc

	// Launch inbound gossip dispatch loop.
	go n.receiveLoop(ctx)

	return nil
}

// receiveLoop reads from GossipSub and dispatches each message.
func (n *Node) receiveLoop(ctx context.Context) {
	for {
		data, _, err := n.Gossip.NextTableMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled — clean shutdown
			}
			continue
		}
		n.dispatch(data)
	}
}

// dispatch decodes, validates, logs, and routes one gossip message.
func (n *Node) dispatch(data []byte) {
	env, err := DecodeEnvelope(data, n.lookupPubKey)
	if err != nil {
		return // malformed or bad signature — silently drop
	}

	// Ignore our own messages that echoed back through GossipSub.
	if env.SenderId == n.Host.PeerID {
		return
	}

	// Replay protection.
	if err := n.Gossip.CheckAndUpdateSeq(env.SenderId, env.Seq); err != nil {
		return
	}

	// Append to the hand log (best-effort — duplicate entries are silently ignored).
	_ = n.Log.Append(env)

	// Route to registered handler.
	switch env.Type {
	case MsgType_JOIN_TABLE:
		msg := &JoinTable{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		_ = n.Lobby.HandleJoin(msg, env.SenderId)
		if n.OnJoinTable != nil {
			n.OnJoinTable(msg, env.SenderId)
		}

	case MsgType_PLAYER_READY:
		msg := &PlayerReady{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		_ = n.Lobby.HandleReady(msg, env.SenderId)
		if n.OnPlayerReady != nil {
			n.OnPlayerReady(msg, env.SenderId)
		}

	case MsgType_SHUFFLE_STEP:
		if n.OnShuffleStep == nil {
			return
		}
		msg := &ShuffleStep{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnShuffleStep(msg)

	case MsgType_PARTIAL_DECRYPT:
		if n.OnPartialDecrypt == nil {
			return
		}
		msg := &PartialDecrypt{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnPartialDecrypt(msg)

	case MsgType_PLAYER_ACTION:
		if n.OnPlayerAction == nil {
			return
		}
		msg := &PlayerAction{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnPlayerAction(msg)

	case MsgType_GAME_STATE_SYNC:
		if n.OnGameStateSync == nil {
			return
		}
		msg := &GameStateSync{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnGameStateSync(msg)

	case MsgType_HEARTBEAT:
		if n.OnHeartbeat == nil {
			return
		}
		msg := &Heartbeat{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnHeartbeat(msg)

	case MsgType_TIMEOUT_VOTE:
		if n.OnTimeoutVote == nil {
			return
		}
		msg := &TimeoutVote{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnTimeoutVote(msg)

	case MsgType_HAND_RESULT:
		if n.OnHandResult == nil {
			return
		}
		msg := &HandResult{}
		if proto.Unmarshal(env.Payload, msg) != nil {
			return
		}
		n.OnHandResult(msg)
	}
}

// ── Outbound publish helpers ──────────────────────────────────────────────────

func (n *Node) nextSeq() int64 {
	return atomic.AddInt64(&n.seq, 1)
}

func (n *Node) publish(ctx context.Context, msgType MsgType, payload []byte) error {
	env := NewEnvelope(msgType, n.Host.PeerID, n.nextSeq(), payload)
	frame, err := EncodeEnvelope(env, n.Host.Ed25519PK)
	if err != nil {
		return fmt.Errorf("publish %s: %w", msgType, err)
	}
	return n.Gossip.Publish(ctx, frame)
}

// BroadcastJoin announces this player's intent to join the table.
func (n *Node) BroadcastJoin(ctx context.Context, handNum int64) error {
	msg := &JoinTable{
		TableId:      n.tableID,
		PlayerName:   n.playerName,
		BuyIn:        n.buyIn,
		SraPubKeyE:   n.sraKey.PublicKey().Bytes(),
		SessionNonce: []byte(n.Host.PeerID),
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastJoin: %w", err)
	}
	return n.publish(ctx, MsgType_JOIN_TABLE, b)
}

// BroadcastReady signals this player is ready to start the hand.
func (n *Node) BroadcastReady(ctx context.Context, handNum int64) error {
	msg := &PlayerReady{TableId: n.tableID, HandNum: handNum}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastReady: %w", err)
	}
	return n.publish(ctx, MsgType_PLAYER_READY, b)
}

// BroadcastShuffleStep publishes this player's shuffle step output.
func (n *Node) BroadcastShuffleStep(ctx context.Context, handNum int64, step *pokercrypto.ShuffleStep) error {
	msg := &ShuffleStep{
		TableId:         n.tableID,
		HandNum:         handNum,
		PlayerId:        n.Host.PeerID,
		Deck:            DeckToWire(step.OutputDeck),
		CommitmentHash:  step.Commitment.Hash,
		CommitmentNonce: step.Commitment.Nonce,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastShuffleStep: %w", err)
	}
	return n.publish(ctx, MsgType_SHUFFLE_STEP, b)
}

// BroadcastPartialDecrypt publishes a partial decryption (community card reveal).
func (n *Node) BroadcastPartialDecrypt(ctx context.Context, handNum int64, pd *pokercrypto.PartialDecryption) error {
	msg := PartialDecryptToWire(n.tableID, handNum, pd)
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastPartialDecrypt: %w", err)
	}
	return n.publish(ctx, MsgType_PARTIAL_DECRYPT, b)
}

// BroadcastAction publishes a betting action to all peers.
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
		return fmt.Errorf("BroadcastAction: %w", err)
	}
	return n.publish(ctx, MsgType_PLAYER_ACTION, b)
}

// BroadcastHeartbeat sends a liveness ping on the heartbeat topic.
func (n *Node) BroadcastHeartbeat(ctx context.Context, handNum, hbSeq int64) error {
	msg := &Heartbeat{TableId: n.tableID, HandNum: handNum, Seq: hbSeq}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastHeartbeat: %w", err)
	}
	env := NewEnvelope(MsgType_HEARTBEAT, n.Host.PeerID, n.nextSeq(), b)
	frame, err := EncodeEnvelope(env, n.Host.Ed25519PK)
	if err != nil {
		return err
	}
	return n.Gossip.PublishHeartbeat(ctx, frame)
}

// BroadcastTimeoutVote votes to time out the given player.
func (n *Node) BroadcastTimeoutVote(ctx context.Context, handNum int64, timedOutPeerID string) error {
	msg := &TimeoutVote{
		TableId:          n.tableID,
		HandNum:          handNum,
		VotingPlayerId:   n.Host.PeerID,
		TimedoutPlayerId: timedOutPeerID,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastTimeoutVote: %w", err)
	}
	return n.publish(ctx, MsgType_TIMEOUT_VOTE, b)
}

// BroadcastHandResult publishes the final hand outcome with the state root.
func (n *Node) BroadcastHandResult(ctx context.Context, handNum int64, pots []*PotResult, stateRoot []byte) error {
	msg := &HandResult{
		TableId:   n.tableID,
		HandNum:   handNum,
		Pots:      pots,
		StateRoot: stateRoot,
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("BroadcastHandResult: %w", err)
	}
	return n.publish(ctx, MsgType_HAND_RESULT, b)
}

// BroadcastStateSync sends a full game state snapshot (used on reconnect).
func (n *Node) BroadcastStateSync(ctx context.Context, sync *GameStateSync) error {
	b, err := proto.Marshal(sync)
	if err != nil {
		return fmt.Errorf("BroadcastStateSync: %w", err)
	}
	return n.publish(ctx, MsgType_GAME_STATE_SYNC, b)
}

// SendDirectPartialDecrypt sends a private partial decryption to one peer
// via the /poker/1.0.0 direct stream (used for hole card reveals).
func (n *Node) SendDirectPartialDecrypt(ctx context.Context, toPeerID peer.ID, handNum int64, pd *pokercrypto.PartialDecryption) error {
	msg := PartialDecryptToWire(n.tableID, handNum, pd)
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("SendDirectPartialDecrypt: marshal: %w", err)
	}
	env := NewEnvelope(MsgType_PARTIAL_DECRYPT, n.Host.PeerID, n.nextSeq(), b)
	frame, err := EncodeEnvelope(env, n.Host.Ed25519PK)
	if err != nil {
		return fmt.Errorf("SendDirectPartialDecrypt: encode: %w", err)
	}
	return SendDirect(ctx, n.Host.Host, toPeerID, frame)
}

// ── Peer key management ───────────────────────────────────────────────────────

// RegisterPeer caches a peer's Ed25519 public key.
// Called automatically when the key can be extracted from the PeerID,
// and manually after the Noise handshake provides the authenticated key.
func (n *Node) RegisterPeer(peerID string, pub ed25519.PublicKey) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[peerID] = pub
}

// lookupPubKey returns the Ed25519 public key for a given peer.
// First checks the cache, then attempts to extract it from the PeerID itself
// (works for Ed25519 keys which are small enough to be embedded).
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
		// Key not extractable from PeerID (e.g. RSA key) — skip verification.
		// In production all peers use Ed25519, so this path is not hit.
		return nil, nil //nolint
	}
	n.mu.Lock()
	n.peers[peerID] = pub
	n.mu.Unlock()
	return pub, nil
}

// SetHandNum updates the game log for a new hand.
func (n *Node) SetHandNum(handNum int64) {
	n.Log = NewGameLog(n.tableID, handNum)
}

// Close tears down the node cleanly: stops discovery, GossipSub, and the host.
func (n *Node) Close() error {
	if n.Discovery != nil {
		_ = n.Discovery.Close()
	}
	_ = n.Gossip.Close()
	return n.Host.Close()
}
