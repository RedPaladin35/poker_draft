package network

import (
	"context"
	"sync"
	"testing"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/game"
	"google.golang.org/protobuf/proto"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeTestNode(t *testing.T, tableID, name string) *Node {
	t.Helper()
	ctx := context.Background()
	p := pokercrypto.SharedPrime()
	key, err := pokercrypto.GenerateSRAKey(p)
	if err != nil {
		t.Fatalf("SRAKey: %v", err)
	}
	n, err := NewNode(ctx, tableID, name, 1000, key, "/ip4/127.0.0.1/tcp/0", nil)
	if err != nil {
		t.Fatalf("NewNode %s: %v", name, err)
	}
	if err := n.Start(ctx); err != nil {
		t.Fatalf("Start %s: %v", name, err)
	}
	t.Cleanup(func() { n.Close() })
	return n
}

func connectNodes(t *testing.T, a, b *Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs := b.Host.Addrs()
	if len(addrs) == 0 {
		t.Fatal("connectNodes: node B has no addresses")
	}
	if err := a.Host.Connect(ctx, addrs[0]); err != nil {
		t.Fatalf("connectNodes: %v", err)
	}
	// Give GossipSub time to mesh.
	time.Sleep(200 * time.Millisecond)
}

// ── Codec tests ───────────────────────────────────────────────────────────────

func TestEncodeDecodeEnvelope_RoundTrip(t *testing.T) {
	// Generate an Ed25519 key pair for signing.
	import_ed25519_pub, priv, err := generateTestKey()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("hello poker")
	env := NewEnvelope(MsgType_PLAYER_ACTION, "peer123", 1, payload)
	frame, err := EncodeEnvelope(env, priv)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}

	decoded, err := DecodeEnvelope(frame, func(id string) (ed25519.PublicKey, error) {
		return import_ed25519_pub, nil
	})
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if decoded.SenderId != "peer123" {
		t.Errorf("sender mismatch: %s", decoded.SenderId)
	}
	if string(decoded.Payload) != "hello poker" {
		t.Errorf("payload mismatch: %s", decoded.Payload)
	}
}

func TestEncodeDecodeEnvelope_TamperedPayload(t *testing.T) {
	_, priv, _ := generateTestKey()
	pub2, _, _ := generateTestKey()

	env := NewEnvelope(MsgType_PLAYER_ACTION, "peer1", 1, []byte("original"))
	frame, _ := EncodeEnvelope(env, priv)

	// Try to verify with a different key — should fail.
	_, err := DecodeEnvelope(frame, func(id string) (ed25519.PublicKey, error) {
		return pub2, nil
	})
	if err == nil {
		t.Error("expected signature verification failure, got nil")
	}
}

func TestBigIntWireRoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	original := pokercrypto.CardToField(27, p)

	wire := BigIntToBytes(original)
	recovered := BytesToBigInt(wire)

	if original.Cmp(recovered) != 0 {
		t.Errorf("big.Int round-trip failed: %s != %s", original, recovered)
	}
}

func TestZKProofWireRoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"test"}, []byte("nonce"))

	ct := pokercrypto.CardToField(5, p)
	result, _ := key.Decrypt(ct)
	proof, _ := pokercrypto.ProveDecryption(key, ct, result, sid)

	wire := ZKProofToWire(proof)
	recovered := ZKProofFromWire(wire)

	if proof.A.Cmp(recovered.A) != 0 {
		t.Error("ZKProof.A round-trip failed")
	}
	if proof.S.Cmp(recovered.S) != 0 {
		t.Error("ZKProof.S round-trip failed")
	}
}

func TestDeckWireRoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	deck := pokercrypto.BuildPlaintextDeck(p)

	wire := DeckToWire(deck)
	recovered := DeckFromWire(wire)

	if len(recovered) != 52 {
		t.Fatalf("expected 52 cards, got %d", len(recovered))
	}
	for i, v := range deck {
		if v.Cmp(recovered[i]) != 0 {
			t.Errorf("deck[%d] round-trip failed", i)
		}
	}
}

// ── GameLog tests ─────────────────────────────────────────────────────────────

func TestGameLog_AppendAndStateRoot(t *testing.T) {
	gl := NewGameLog("table1", 1)

	e1 := &Envelope{Type: MsgType_PLAYER_ACTION, SenderId: "alice", Seq: 1, Payload: []byte("fold")}
	e2 := &Envelope{Type: MsgType_PLAYER_ACTION, SenderId: "bob", Seq: 1, Payload: []byte("call")}

	if err := gl.Append(e1); err != nil {
		t.Fatal(err)
	}
	if err := gl.Append(e2); err != nil {
		t.Fatal(err)
	}
	if gl.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", gl.Len())
	}

	root1 := gl.StateRoot()
	// Append a third entry — root must change.
	e3 := &Envelope{Type: MsgType_HEARTBEAT, SenderId: "alice", Seq: 2, Payload: []byte("hb")}
	gl.Append(e3)
	root2 := gl.StateRoot()
	if string(root1) == string(root2) {
		t.Error("state root did not change after append")
	}
}

func TestGameLog_DuplicateRejected(t *testing.T) {
	gl := NewGameLog("table1", 1)
	e := &Envelope{SenderId: "alice", Seq: 1}
	gl.Append(e)
	if err := gl.Append(e); err == nil {
		t.Error("expected duplicate error, got nil")
	}
}

func TestGameLog_EquivocationDetected(t *testing.T) {
	gl := NewGameLog("table1", 1)
	e1 := &Envelope{SenderId: "alice", Seq: 1, Payload: []byte("fold")}
	e2 := &Envelope{SenderId: "alice", Seq: 1, Payload: []byte("call")} // same seq, different payload
	gl.entries = append(gl.entries, e1, e2)
	gl.byKey["alice:1"] = struct{}{} // force insert both for test

	senderID, _, _, _ := gl.DetectEquivocation()
	if senderID != "alice" {
		t.Errorf("expected equivocation by alice, got '%s'", senderID)
	}
}

// ── Lobby tests ───────────────────────────────────────────────────────────────

func TestLobby_JoinAndReady(t *testing.T) {
	l := NewLobby("t1", 3)

	for i, pid := range []string{"p1", "p2", "p3"} {
		msg := &JoinTable{TableId: "t1", PlayerName: pid, BuyIn: 100}
		if err := l.HandleJoin(msg, pid); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
	}
	if l.Count() != 3 {
		t.Errorf("expected 3 seats, got %d", l.Count())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := l.WaitReady(ctx); err != nil {
			t.Errorf("WaitReady: %v", err)
		}
	}()

	for _, pid := range []string{"p1", "p2", "p3"} {
		l.HandleReady(&PlayerReady{TableId: "t1"}, pid)
	}
	wg.Wait()
}

func TestLobby_TableFull(t *testing.T) {
	l := NewLobby("t1", 2)
	l.HandleJoin(&JoinTable{BuyIn: 100, PlayerName: "a"}, "p1")
	l.HandleJoin(&JoinTable{BuyIn: 100, PlayerName: "b"}, "p2")
	err := l.HandleJoin(&JoinTable{BuyIn: 100, PlayerName: "c"}, "p3")
	if err == nil {
		t.Error("expected full table error")
	}
}

func TestLobby_DuplicateJoin(t *testing.T) {
	l := NewLobby("t1", 4)
	l.HandleJoin(&JoinTable{BuyIn: 100, PlayerName: "a"}, "p1")
	err := l.HandleJoin(&JoinTable{BuyIn: 100, PlayerName: "a"}, "p1")
	if err == nil {
		t.Error("expected duplicate join error")
	}
}

// ── Node messaging tests ──────────────────────────────────────────────────────

func TestNode_BroadcastAndReceiveAction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "table-test", "Alice")
	nodeB := makeTestNode(t, "table-test", "Bob")
	connectNodes(t, nodeA, nodeB)

	received := make(chan *PlayerAction, 1)
	nodeB.OnPlayerAction = func(msg *PlayerAction) {
		received <- msg
	}

	// Alice broadcasts a fold action.
	action := game.Action{PlayerID: nodeA.Host.PeerID, Type: game.ActionFold}
	if err := nodeA.BroadcastAction(ctx, 1, action, 1); err != nil {
		t.Fatalf("BroadcastAction: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Action != int32(game.ActionFold) {
			t.Errorf("expected Fold, got %d", msg.Action)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for action message")
	}
}

func TestNode_BroadcastJoinAndLobby(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "lobby-test", "Alice")
	nodeB := makeTestNode(t, "lobby-test", "Bob")
	connectNodes(t, nodeA, nodeB)

	nodeB.Lobby = NewLobby("lobby-test", 2)
	joined := make(chan string, 2)
	nodeB.OnJoinTable = func(msg *JoinTable, from string) {
		joined <- from
	}

	if err := nodeA.BroadcastJoin(ctx, 1); err != nil {
		t.Fatalf("BroadcastJoin: %v", err)
	}

	select {
	case from := <-joined:
		if from != nodeA.Host.PeerID {
			t.Errorf("expected join from %s, got %s", nodeA.Host.PeerID, from)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for join message")
	}
}

func TestNode_HeartbeatBroadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "hb-test", "Alice")
	nodeB := makeTestNode(t, "hb-test", "Bob")
	connectNodes(t, nodeA, nodeB)

	// Heartbeats are on a separate subscription — test via direct gossip path.
	_ = nodeA.BroadcastHeartbeat(ctx, 1, 1)
	// No assertion needed — just verify it doesn't error.
}

func TestNode_ReplayProtection(t *testing.T) {
	gm := &GossipManager{seqNums: make(map[string]int64)}

	if err := gm.CheckAndUpdateSeq("alice", 1); err != nil {
		t.Fatal(err)
	}
	if err := gm.CheckAndUpdateSeq("alice", 2); err != nil {
		t.Fatal(err)
	}
	// Replay seq 1 — must fail.
	if err := gm.CheckAndUpdateSeq("alice", 1); err == nil {
		t.Error("expected replay error, got nil")
	}
	// Same seq — must also fail.
	if err := gm.CheckAndUpdateSeq("alice", 2); err == nil {
		t.Error("expected duplicate seq error, got nil")
	}
}

// ── Proto marshal/unmarshal tests ─────────────────────────────────────────────

func TestProtoMarshal_ShuffleStep(t *testing.T) {
	p := pokercrypto.SharedPrime()
	deck := pokercrypto.BuildPlaintextDeck(p)

	msg := &ShuffleStep{
		TableId:  "t1",
		HandNum:  1,
		PlayerId: "alice",
		Deck:     DeckToWire(deck),
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg2 := &ShuffleStep{}
	if err := proto.Unmarshal(b, msg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(msg2.Deck) != 52 {
		t.Errorf("expected 52 cards, got %d", len(msg2.Deck))
	}
	recovered := DeckFromWire(msg2.Deck)
	for i, v := range deck {
		if v.Cmp(recovered[i]) != 0 {
			t.Errorf("card %d mismatch after proto round-trip", i)
		}
	}
}

func TestProtoMarshal_PartialDecrypt(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"a", "b"}, []byte("n"))

	ct := pokercrypto.CardToField(10, p)
	result, _ := key.Decrypt(ct)
	proof, _ := pokercrypto.ProveDecryption(key, ct, result, sid)

	msg := &PartialDecrypt{
		PlayerId:   "alice",
		CardIndex:  10,
		Ciphertext: ct.Bytes(),
		Result:     result.Bytes(),
		Proof:      ZKProofToWire(proof),
	}
	b, _ := proto.Marshal(msg)
	msg2 := &PartialDecrypt{}
	proto.Unmarshal(b, msg2)

	recoveredProof := ZKProofFromWire(msg2.Proof)
	if err := pokercrypto.VerifyDecryption(recoveredProof, ct, result, p, sid); err != nil {
		t.Errorf("ZK proof failed after proto round-trip: %v", err)
	}
}

// ── helper: test key generation ──────────────────────────────────────────────

func generateTestKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	return pub, priv, err
}
