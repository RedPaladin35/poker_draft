package network

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/game"
	"google.golang.org/protobuf/proto"
)

// ── test helpers ──────────────────────────────────────────────────────────────

func generateTestEd25519() (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	return pub, priv
}

func makeTestNode(t *testing.T, tableID, name string) *Node {
	t.Helper()
	ctx := context.Background()
	p := pokercrypto.SharedPrime()
	key, err := pokercrypto.GenerateSRAKey(p)
	if err != nil {
		t.Fatalf("SRAKey for %s: %v", name, err)
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
	time.Sleep(200 * time.Millisecond) // GossipSub mesh formation
}

// ── Codec tests ───────────────────────────────────────────────────────────────

func TestEncodeDecodeEnvelope_RoundTrip(t *testing.T) {
	pub, priv := generateTestEd25519()

	env := NewEnvelope(MsgType_PLAYER_ACTION, "peer-abc", 1, []byte("test payload"))
	frame, err := EncodeEnvelope(env, priv)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}

	decoded, err := DecodeEnvelope(frame, func(_ string) (ed25519.PublicKey, error) {
		return pub, nil
	})
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if decoded.SenderId != "peer-abc" {
		t.Errorf("sender mismatch: got %q", decoded.SenderId)
	}
	if string(decoded.Payload) != "test payload" {
		t.Errorf("payload mismatch: got %q", decoded.Payload)
	}
	if decoded.Type != MsgType_PLAYER_ACTION {
		t.Errorf("type mismatch: got %v", decoded.Type)
	}
}

func TestEncodeDecodeEnvelope_WrongKey_Rejected(t *testing.T) {
	_, priv := generateTestEd25519()
	pub2, _ := generateTestEd25519() // different key

	env := NewEnvelope(MsgType_PLAYER_ACTION, "peer1", 1, []byte("data"))
	frame, _ := EncodeEnvelope(env, priv)

	_, err := DecodeEnvelope(frame, func(_ string) (ed25519.PublicKey, error) {
		return pub2, nil
	})
	if err == nil {
		t.Error("expected signature verification failure with wrong key")
	}
}

func TestEncodeDecodeEnvelope_FrameTooShort(t *testing.T) {
	_, err := DecodeEnvelope([]byte{0x00, 0x01}, nil)
	if err == nil {
		t.Error("expected error for too-short frame")
	}
}

func TestEncodeDecodeEnvelope_NoVerification(t *testing.T) {
	// nil pubKeyFn = skip verification (used when Noise guarantees auth)
	_, priv := generateTestEd25519()
	env := NewEnvelope(MsgType_HEARTBEAT, "peer2", 5, []byte("hb"))
	frame, _ := EncodeEnvelope(env, priv)

	decoded, err := DecodeEnvelope(frame, nil)
	if err != nil {
		t.Fatalf("DecodeEnvelope (no verify): %v", err)
	}
	if decoded.Seq != 5 {
		t.Errorf("seq mismatch: got %d", decoded.Seq)
	}
}

// ── Big.Int and ZKProof wire encoding tests ───────────────────────────────────

func TestBigIntWire_RoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	original := pokercrypto.CardToField(27, p)
	recovered := BytesToBigInt(BigIntToBytes(original))
	if original.Cmp(recovered) != 0 {
		t.Errorf("big.Int round-trip failed")
	}
}

func TestBigIntWire_Nil(t *testing.T) {
	if BigIntToBytes(nil) != nil {
		t.Error("BigIntToBytes(nil) should return nil")
	}
	if BytesToBigInt(nil) != nil {
		t.Error("BytesToBigInt(nil) should return nil")
	}
}

func TestZKProofWire_RoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"test"}, []byte("nonce"))

	ct := pokercrypto.CardToField(5, p)
	result, _ := key.Decrypt(ct)
	proof, err := pokercrypto.ProveDecryption(key, ct, result, sid)
	if err != nil {
		t.Fatalf("ProveDecryption: %v", err)
	}

	wire := ZKProofToWire(proof)
	recovered := ZKProofFromWire(wire)

	if proof.A.Cmp(recovered.A) != 0 {
		t.Error("ZKProof.A mismatch after wire round-trip")
	}
	if proof.B.Cmp(recovered.B) != 0 {
		t.Error("ZKProof.B mismatch after wire round-trip")
	}
	if proof.S.Cmp(recovered.S) != 0 {
		t.Error("ZKProof.S mismatch after wire round-trip")
	}
	if proof.H.Cmp(recovered.H) != 0 {
		t.Error("ZKProof.H mismatch after wire round-trip")
	}

	// The recovered proof must still verify.
	if err := pokercrypto.VerifyDecryption(recovered, ct, result, p, sid); err != nil {
		t.Errorf("recovered ZKProof failed verification: %v", err)
	}
}

func TestDeckWire_RoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	deck := pokercrypto.BuildPlaintextDeck(p)

	wire := DeckToWire(deck)
	if len(wire) != 52 {
		t.Fatalf("expected 52 wire entries, got %d", len(wire))
	}

	recovered := DeckFromWire(wire)
	for i, v := range deck {
		if v.Cmp(recovered[i]) != 0 {
			t.Errorf("deck[%d] mismatch after wire round-trip", i)
		}
	}
}

// ── GameLog tests ─────────────────────────────────────────────────────────────

func TestGameLog_AppendAndLen(t *testing.T) {
	gl := NewGameLog("t1", 1)
	for i := 1; i <= 5; i++ {
		e := &Envelope{Type: MsgType_PLAYER_ACTION, SenderId: "alice", Seq: int64(i)}
		if err := gl.Append(e); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if gl.Len() != 5 {
		t.Errorf("expected 5 entries, got %d", gl.Len())
	}
}

func TestGameLog_DuplicateRejected(t *testing.T) {
	gl := NewGameLog("t1", 1)
	e := &Envelope{SenderId: "alice", Seq: 1}
	if err := gl.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := gl.Append(e); err == nil {
		t.Error("expected duplicate error")
	}
}

func TestGameLog_StateRootChanges(t *testing.T) {
	gl := NewGameLog("t1", 1)
	gl.Append(&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("fold")})
	root1 := gl.StateRootHex()

	gl.Append(&Envelope{SenderId: "bob", Seq: 1, Payload: []byte("call")})
	root2 := gl.StateRootHex()

	if root1 == root2 {
		t.Error("state root did not change after appending a new entry")
	}
}

func TestGameLog_DifferentLogsProduceDifferentRoots(t *testing.T) {
	gl1 := NewGameLog("t1", 1)
	gl2 := NewGameLog("t1", 1)

	gl1.Append(&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("fold")})
	gl2.Append(&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("call")}) // different payload

	if gl1.StateRootHex() == gl2.StateRootHex() {
		t.Error("different logs produced the same state root")
	}
}

func TestGameLog_EquivocationDetected(t *testing.T) {
	gl := NewGameLog("t1", 1)
	// Manually insert two entries with same (sender, seq) but different payloads
	// — simulates what would happen if a malicious peer signs conflicting messages.
	gl.entries = append(gl.entries,
		&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("fold")},
		&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("raise 100")},
	)

	senderID, envA, envB, _ := gl.DetectEquivocation()
	if senderID != "alice" {
		t.Errorf("expected equivocation by alice, got %q", senderID)
	}
	if envA == nil || envB == nil {
		t.Error("expected both conflicting envelopes to be returned")
	}
}

func TestGameLog_NoEquivocation(t *testing.T) {
	gl := NewGameLog("t1", 1)
	gl.Append(&Envelope{SenderId: "alice", Seq: 1, Payload: []byte("fold")})
	gl.Append(&Envelope{SenderId: "alice", Seq: 2, Payload: []byte("call")})
	gl.Append(&Envelope{SenderId: "bob", Seq: 1, Payload: []byte("raise")})

	senderID, _, _, _ := gl.DetectEquivocation()
	if senderID != "" {
		t.Errorf("unexpected equivocation detected for %q", senderID)
	}
}

func TestGameLog_ValidateSequences(t *testing.T) {
	gl := NewGameLog("t1", 1)
	gl.Append(&Envelope{SenderId: "alice", Seq: 1})
	gl.Append(&Envelope{SenderId: "alice", Seq: 2})
	gl.Append(&Envelope{SenderId: "alice", Seq: 3})

	if err := gl.ValidateSequences([]string{"alice"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Insert a gap.
	gl.Append(&Envelope{SenderId: "alice", Seq: 5}) // skipped 4
	if err := gl.ValidateSequences([]string{"alice"}); err == nil {
		t.Error("expected gap error, got nil")
	}
}

// ── Lobby tests ───────────────────────────────────────────────────────────────

func TestLobby_JoinAndReady_ThreePlayers(t *testing.T) {
	l := NewLobby("t1", 3)

	for _, pid := range []string{"p1", "p2", "p3"} {
		msg := &JoinTable{TableId: "t1", PlayerName: pid, BuyIn: 500}
		if err := l.HandleJoin(msg, pid); err != nil {
			t.Fatalf("join %s: %v", pid, err)
		}
	}
	if l.Count() != 3 {
		t.Errorf("expected 3 seated, got %d", l.Count())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
		if err := l.HandleReady(&PlayerReady{TableId: "t1"}, pid); err != nil {
			t.Errorf("ready %s: %v", pid, err)
		}
	}
	wg.Wait()

	if l.State() != LobbyReady {
		t.Errorf("expected LobbyReady, got %d", l.State())
	}
}

func TestLobby_TableFull(t *testing.T) {
	l := NewLobby("t1", 2)
	l.HandleJoin(&JoinTable{PlayerName: "a", BuyIn: 100}, "p1")
	l.HandleJoin(&JoinTable{PlayerName: "b", BuyIn: 100}, "p2")
	if err := l.HandleJoin(&JoinTable{PlayerName: "c", BuyIn: 100}, "p3"); err == nil {
		t.Error("expected full table error")
	}
}

func TestLobby_DuplicateJoin_Rejected(t *testing.T) {
	l := NewLobby("t1", 4)
	l.HandleJoin(&JoinTable{PlayerName: "a", BuyIn: 100}, "p1")
	if err := l.HandleJoin(&JoinTable{PlayerName: "a", BuyIn: 100}, "p1"); err == nil {
		t.Error("expected duplicate join error")
	}
}

func TestLobby_InvalidBuyIn_Rejected(t *testing.T) {
	l := NewLobby("t1", 4)
	if err := l.HandleJoin(&JoinTable{PlayerName: "a", BuyIn: 0}, "p1"); err == nil {
		t.Error("expected invalid buy-in error")
	}
}

func TestLobby_PlayerIDs_InJoinOrder(t *testing.T) {
	l := NewLobby("t1", 3)
	l.HandleJoin(&JoinTable{PlayerName: "first", BuyIn: 100}, "p1")
	time.Sleep(time.Millisecond) // ensure distinct timestamps
	l.HandleJoin(&JoinTable{PlayerName: "second", BuyIn: 100}, "p2")
	time.Sleep(time.Millisecond)
	l.HandleJoin(&JoinTable{PlayerName: "third", BuyIn: 100}, "p3")

	ids := l.PlayerIDs()
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}
	if ids[0] != "p1" || ids[1] != "p2" || ids[2] != "p3" {
		t.Errorf("unexpected order: %v", ids)
	}
}

// ── Replay protection tests ───────────────────────────────────────────────────

func TestReplayProtection_StrictlyIncreasing(t *testing.T) {
	gm := &GossipManager{seqNums: make(map[string]int64)}

	if err := gm.CheckAndUpdateSeq("alice", 1); err != nil {
		t.Fatalf("seq 1 should be accepted: %v", err)
	}
	if err := gm.CheckAndUpdateSeq("alice", 2); err != nil {
		t.Fatalf("seq 2 should be accepted: %v", err)
	}
	if err := gm.CheckAndUpdateSeq("alice", 10); err != nil {
		t.Fatalf("seq 10 should be accepted (gaps allowed): %v", err)
	}
}

func TestReplayProtection_DuplicateRejected(t *testing.T) {
	gm := &GossipManager{seqNums: make(map[string]int64)}
	gm.CheckAndUpdateSeq("bob", 5)

	if err := gm.CheckAndUpdateSeq("bob", 5); err == nil {
		t.Error("duplicate seq 5 should be rejected")
	}
}

func TestReplayProtection_OldSeqRejected(t *testing.T) {
	gm := &GossipManager{seqNums: make(map[string]int64)}
	gm.CheckAndUpdateSeq("carol", 10)

	if err := gm.CheckAndUpdateSeq("carol", 3); err == nil {
		t.Error("old seq 3 should be rejected after seq 10 was seen")
	}
}

func TestReplayProtection_IndependentPerPeer(t *testing.T) {
	gm := &GossipManager{seqNums: make(map[string]int64)}
	gm.CheckAndUpdateSeq("alice", 5)

	// bob's seq is independent — seq 1 from bob should be fine after seq 5 from alice.
	if err := gm.CheckAndUpdateSeq("bob", 1); err != nil {
		t.Errorf("bob seq 1 should be accepted: %v", err)
	}
}

// ── Proto round-trip tests ────────────────────────────────────────────────────

func TestProto_ShuffleStep_RoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	deck := pokercrypto.BuildPlaintextDeck(p)

	original := &ShuffleStep{
		TableId:  "table1",
		HandNum:  3,
		PlayerId: "alice",
		Deck:     DeckToWire(deck),
	}
	b, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	recovered := &ShuffleStep{}
	if err := proto.Unmarshal(b, recovered); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if recovered.HandNum != 3 || recovered.PlayerId != "alice" {
		t.Errorf("fields mismatch: %+v", recovered)
	}
	recoveredDeck := DeckFromWire(recovered.Deck)
	for i, v := range deck {
		if v.Cmp(recoveredDeck[i]) != 0 {
			t.Errorf("deck[%d] mismatch after proto round-trip", i)
		}
	}
}

func TestProto_PartialDecrypt_RoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"a", "b"}, []byte("n"))

	ct := pokercrypto.CardToField(10, p)
	result, _ := key.Decrypt(ct)
	proof, _ := pokercrypto.ProveDecryption(key, ct, result, sid)

	pd := &pokercrypto.PartialDecryption{
		PlayerID:   "alice",
		CardIndex:  10,
		Ciphertext: ct,
		Result:     result,
		Proof:      proof,
	}

	wire := PartialDecryptToWire("t1", 1, pd)
	b, _ := proto.Marshal(wire)
	recovered := &PartialDecrypt{}
	proto.Unmarshal(b, recovered)

	recoveredProof := ZKProofFromWire(recovered.Proof)
	if err := pokercrypto.VerifyDecryption(recoveredProof, ct, result, p, sid); err != nil {
		t.Errorf("ZK proof failed verification after proto round-trip: %v", err)
	}
}

func TestProto_HandResult_RoundTrip(t *testing.T) {
	original := &HandResult{
		TableId: "t1",
		HandNum: 7,
		Pots: []*PotResult{
			{Amount: 500, WinnerIds: []string{"alice"}},
			{Amount: 200, WinnerIds: []string{"bob", "carol"}},
		},
		StateRoot: []byte("abc123"),
	}
	b, _ := proto.Marshal(original)
	recovered := &HandResult{}
	proto.Unmarshal(b, recovered)

	if recovered.HandNum != 7 {
		t.Errorf("HandNum: got %d", recovered.HandNum)
	}
	if len(recovered.Pots) != 2 {
		t.Fatalf("expected 2 pots, got %d", len(recovered.Pots))
	}
	if recovered.Pots[0].Amount != 500 {
		t.Errorf("Pot[0] amount: got %d", recovered.Pots[0].Amount)
	}
	if len(recovered.Pots[1].WinnerIds) != 2 {
		t.Errorf("Pot[1] winners: got %v", recovered.Pots[1].WinnerIds)
	}
}

// ── Network integration tests (require working libp2p) ───────────────────────

func TestNode_BroadcastAndReceiveAction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "net-test", "Alice")
	nodeB := makeTestNode(t, "net-test", "Bob")
	connectNodes(t, nodeA, nodeB)

	received := make(chan *PlayerAction, 1)
	nodeB.OnPlayerAction = func(msg *PlayerAction) {
		received <- msg
	}

	action := game.Action{PlayerID: nodeA.Host.PeerID, Type: game.ActionRaise, Amount: 100}
	if err := nodeA.BroadcastAction(ctx, 1, action, 1); err != nil {
		t.Fatalf("BroadcastAction: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Action != int32(game.ActionRaise) {
			t.Errorf("expected Raise, got %d", msg.Action)
		}
		if msg.Amount != 100 {
			t.Errorf("expected amount 100, got %d", msg.Amount)
		}
	case <-time.After(10 * time.Second):
		t.Error("timeout: action not received within 10s")
	}
}

func TestNode_BroadcastJoin_LobbyUpdated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "lobby-net-test", "Alice")
	nodeB := makeTestNode(t, "lobby-net-test", "Bob")
	connectNodes(t, nodeA, nodeB)

	joined := make(chan string, 1)
	nodeB.OnJoinTable = func(msg *JoinTable, from string) {
		joined <- from
	}

	if err := nodeA.BroadcastJoin(ctx, 1); err != nil {
		t.Fatalf("BroadcastJoin: %v", err)
	}

	select {
	case from := <-joined:
		if from != nodeA.Host.PeerID {
			t.Errorf("join from unexpected peer: got %s, want %s", from, nodeA.Host.PeerID)
		}
		if nodeB.Lobby.Count() != 1 {
			t.Errorf("lobby should have 1 player, got %d", nodeB.Lobby.Count())
		}
	case <-time.After(10 * time.Second):
		t.Error("timeout: join message not received")
	}
}

func TestNode_ThreePeerMesh_AllReceive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nodeA := makeTestNode(t, "mesh-test", "Alice")
	nodeB := makeTestNode(t, "mesh-test", "Bob")
	nodeC := makeTestNode(t, "mesh-test", "Carol")
	connectNodes(t, nodeA, nodeB)
	connectNodes(t, nodeB, nodeC)
	connectNodes(t, nodeA, nodeC)

	var mu sync.Mutex
	receivedBy := make(map[string]bool)

	for name, node := range map[string]*Node{"Bob": nodeB, "Carol": nodeC} {
		n := name
		nd := node
		nd.OnPlayerAction = func(msg *PlayerAction) {
			mu.Lock()
			receivedBy[n] = true
			mu.Unlock()
		}
	}

	action := game.Action{PlayerID: nodeA.Host.PeerID, Type: game.ActionFold}
	if err := nodeA.BroadcastAction(ctx, 1, action, 1); err != nil {
		t.Fatalf("BroadcastAction: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		both := receivedBy["Bob"] && receivedBy["Carol"]
		mu.Unlock()
		if both {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Errorf("not all peers received the action: %v", receivedBy)
			mu.Unlock()
			return
		default:
		}
	}
}
