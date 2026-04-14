package fault

import (
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"context"
	"github.com/p2p-poker/internal/game"
)

// ── HeartbeatMonitor tests ────────────────────────────────────────────────────

func TestHeartbeat_RegisterAndRecord(t *testing.T) {
	hm := NewHeartbeatMonitor(10 * time.Second)
	hm.RegisterPeer("alice")

	if hm.Status("alice") != PeerAlive {
		t.Errorf("expected PeerAlive after register, got %s", hm.Status("alice"))
	}

	hm.RecordHeartbeat("alice")
	if hm.Status("alice") != PeerAlive {
		t.Errorf("expected PeerAlive after heartbeat, got %s", hm.Status("alice"))
	}
}

func TestHeartbeat_UnknownPeerTimedOut(t *testing.T) {
	hm := NewHeartbeatMonitor(10 * time.Second)
	if hm.Status("unknown") != PeerTimedOut {
		t.Error("unknown peer should return PeerTimedOut")
	}
}

func TestHeartbeat_TimeoutDetected(t *testing.T) {
	// Use a very short timeout so we can trigger it in a test.
	hm := NewHeartbeatMonitor(50 * time.Millisecond)
	hm.RegisterPeer("bob")

	// Don't send any heartbeats — bob should time out quickly.
	time.Sleep(100 * time.Millisecond)
	timedOut := hm.CheckTimeouts()

	found := false
	for _, id := range timedOut {
		if id == "bob" {
			found = true
		}
	}
	if !found {
		t.Error("bob should be in the timed-out list")
	}
	if hm.Status("bob") != PeerTimedOut {
		t.Errorf("expected PeerTimedOut, got %s", hm.Status("bob"))
	}
}

func TestHeartbeat_RecoveryAfterTimeout(t *testing.T) {
	hm := NewHeartbeatMonitor(50 * time.Millisecond)
	hm.RegisterPeer("carol")

	time.Sleep(100 * time.Millisecond)
	hm.CheckTimeouts()

	// Carol recovers — sends a heartbeat.
	hm.RecordHeartbeat("carol")
	if hm.Status("carol") != PeerAlive {
		t.Errorf("expected PeerAlive after recovery, got %s", hm.Status("carol"))
	}
}

func TestHeartbeat_OnTimeoutCallback(t *testing.T) {
	hm := NewHeartbeatMonitor(30 * time.Millisecond)
	hm.RegisterPeer("dave")

	var called int32
	hm.OnTimeout = func(peerID string) {
		if peerID == "dave" {
			atomic.AddInt32(&called, 1)
		}
	}

	time.Sleep(80 * time.Millisecond)
	hm.CheckTimeouts()
	time.Sleep(20 * time.Millisecond) // wait for goroutine

	if atomic.LoadInt32(&called) == 0 {
		t.Error("OnTimeout callback should have fired for dave")
	}
}

func TestHeartbeat_MarkDisconnected(t *testing.T) {
	hm := NewHeartbeatMonitor(10 * time.Second)
	hm.RegisterPeer("eve")
	hm.MarkDisconnected("eve")

	if hm.Status("eve") != PeerDisconnected {
		t.Errorf("expected PeerDisconnected, got %s", hm.Status("eve"))
	}

	// Disconnected peers should not appear in AlivePeers.
	for _, id := range hm.AlivePeers() {
		if id == "eve" {
			t.Error("disconnected peer should not be in AlivePeers")
		}
	}
}

func TestHeartbeat_AlivePeers(t *testing.T) {
	hm := NewHeartbeatMonitor(50 * time.Millisecond)
	for _, id := range []string{"p1", "p2", "p3"} {
		hm.RegisterPeer(id)
	}
	// p3 goes silent immediately; p1 and p2 send heartbeats after the sleep
	// so they're recent when CheckTimeouts runs.
	time.Sleep(80 * time.Millisecond)
	hm.RecordHeartbeat("p1")
	hm.RecordHeartbeat("p2")
	hm.CheckTimeouts()

	alive := hm.AlivePeers()
	aliveMap := make(map[string]bool)
	for _, id := range alive {
		aliveMap[id] = true
	}

	if !aliveMap["p1"] {
		t.Error("p1 should be alive after fresh heartbeat")
	}
	if !aliveMap["p2"] {
		t.Error("p2 should be alive after fresh heartbeat")
	}
	if aliveMap["p3"] {
		t.Error("p3 should have timed out and not be in alive list")
	}
}

// ── TimeoutManager tests ──────────────────────────────────────────────────────

func TestTimeoutVote_MajorityConfirms(t *testing.T) {
	tm := NewTimeoutManager(1, 4, 5*time.Second)
	// 4 players total, 3 eligible voters (excluding target).
	// Need ceil(3 * 2/3) = 2 votes to confirm.

	tm.StartVote("target", "voter1")
	status, _ := tm.RecordVote("target", "voter2", true)

	if status != VoteConfirmed {
		t.Errorf("expected VoteConfirmed with 2/3 votes, got %v", status)
	}
}

func TestTimeoutVote_InsufficientVotes(t *testing.T) {
	tm := NewTimeoutManager(1, 6, 5*time.Second)
	// 6 players, 5 eligible. Need ceil(5 * 2/3) = 4 votes.

	tm.StartVote("target", "voter1")
	status, _ := tm.RecordVote("target", "voter2", true)

	if status != VotePending {
		t.Errorf("expected VotePending with only 2 votes, got %v", status)
	}
}

func TestTimeoutVote_ExistingVoteNotDuplicated(t *testing.T) {
	tm := NewTimeoutManager(1, 3, 5*time.Second)
	v1 := tm.StartVote("target", "voter1")
	v2 := tm.StartVote("target", "voter1") // duplicate start

	// Both calls should return a vote for the same target.
	if v1.TargetPeerID != v2.TargetPeerID {
		t.Error("duplicate StartVote should return vote for same target")
	}
	// And only one vote should be cast (no double-counting).
	if v1.YesCount() != v2.YesCount() {
		t.Error("duplicate StartVote should not add extra votes")
	}
}

func TestTimeoutVote_VoteExpiry(t *testing.T) {
	tm := NewTimeoutManager(1, 4, 50*time.Millisecond)
	tm.StartVote("target", "voter1")

	time.Sleep(100 * time.Millisecond)
	expired := tm.ExpireStaleVotes()

	found := false
	for _, id := range expired {
		if id == "target" {
			found = true
		}
	}
	if !found {
		t.Error("stale vote for target should have expired")
	}

	v := tm.VoteFor("target")
	if v == nil || v.Status != VoteRejected {
		t.Error("expired vote should have VoteRejected status")
	}
}

func TestTimeoutVote_OnConfirmedCallback(t *testing.T) {
	tm := NewTimeoutManager(1, 3, 5*time.Second)
	// 3 players, 2 eligible. Need ceil(2 * 2/3) = 2 votes.

	var confirmed int32
	tm.OnConfirmed = func(peerID string) {
		atomic.AddInt32(&confirmed, 1)
	}

	tm.StartVote("target", "v1")
	tm.RecordVote("target", "v2", true)
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&confirmed) == 0 {
		t.Error("OnConfirmed should have been called")
	}
}

func TestTimeoutVote_HeadsUp(t *testing.T) {
	// 2 players: one votes to fold the other — should immediately confirm.
	tm := NewTimeoutManager(1, 2, 5*time.Second)
	v := tm.StartVote("target", "voter1")

	if v.Status != VoteConfirmed {
		t.Errorf("heads-up: single vote should confirm immediately, got %v", v.Status)
	}
}

// ── KeyShareStore tests ───────────────────────────────────────────────────────

func TestKeyShares_StoreAndContribute(t *testing.T) {
	p := pokercrypto.SharedPrime()
	store := NewKeyShareStore(p)

	share := pokercrypto.ShamirShare{Index: 1, Value: big.NewInt(42)}
	store.StoreMyShare("alice", share)

	got, ok := store.ContributeShare("alice")
	if !ok {
		t.Fatal("expected share to be found")
	}
	if got.Value.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("wrong share value: %s", got.Value)
	}
}

func TestKeyShares_MissingShare(t *testing.T) {
	p := pokercrypto.SharedPrime()
	store := NewKeyShareStore(p)

	_, ok := store.ContributeShare("nonexistent")
	if ok {
		t.Error("should return false for unknown player")
	}
}

func TestKeyShares_FullRoundTrip(t *testing.T) {
	p := pokercrypto.SharedPrime()

	// Generate a real SRA key.
	key, err := pokercrypto.GenerateSRAKey(p)
	if err != nil {
		t.Fatalf("GenerateSRAKey: %v", err)
	}

	// Split into 4 shares with threshold 2.
	shares, threshold, err := SplitAndDistribute(key, 4)
	if err != nil {
		t.Fatalf("SplitAndDistribute: %v", err)
	}

	store := NewKeyShareStore(p)

	// Store share 0 as "our" share, distribute shares 1-3 to the reconstruction pool.
	store.StoreMyShare("bob", shares[0])
	for _, s := range shares[1:] {
		store.AddReconstructionShare("bob", s)
	}

	if !store.CanReconstruct("bob", threshold) {
		t.Fatal("should be able to reconstruct with all shares")
	}

	reconstructed, err := store.ReconstructSRAKey("bob", threshold)
	if err != nil {
		t.Fatalf("ReconstructSRAKey: %v", err)
	}

	// Verify: encrypt with original key, decrypt with reconstructed — must match.
	m := pokercrypto.CardToField(17, p)
	enc, _ := key.Encrypt(m)
	dec, err := reconstructed.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt with reconstructed key: %v", err)
	}
	if m.Cmp(dec) != 0 {
		t.Error("reconstructed key does not decrypt correctly")
	}
}

func TestKeyShares_InsufficientShares(t *testing.T) {
	p := pokercrypto.SharedPrime()
	store := NewKeyShareStore(p)

	// Add only 1 share with threshold 3.
	store.AddReconstructionShare("carol", pokercrypto.ShamirShare{Index: 1, Value: big.NewInt(99)})

	if store.CanReconstruct("carol", 3) {
		t.Error("should not be able to reconstruct with insufficient shares")
	}
	_, err := store.Reconstruct("carol", 3)
	if err == nil {
		t.Error("expected error for insufficient shares")
	}
}

func TestKeyShares_DeduplicateShares(t *testing.T) {
	p := pokercrypto.SharedPrime()
	store := NewKeyShareStore(p)

	share := pokercrypto.ShamirShare{Index: 1, Value: big.NewInt(100)}
	store.AddReconstructionShare("dave", share)
	store.AddReconstructionShare("dave", share) // duplicate
	store.AddReconstructionShare("dave", share) // duplicate again

	// Should only have 1 unique share stored.
	if len(store.sharesHeld["dave"]) != 1 {
		t.Errorf("expected 1 unique share, got %d", len(store.sharesHeld["dave"]))
	}
}

// ── SlashDetector tests ───────────────────────────────────────────────────────

// mockLog implements EquivocationChecker for tests.
type mockLog struct {
	senderID string
	envA     *LogEntry
	envB     *LogEntry
}

func (m *mockLog) DetectEquivocation() (string, *LogEntry, *LogEntry) {
	return m.senderID, m.envA, m.envB
}

func TestSlash_Equivocation_Clean(t *testing.T) {
	sd := NewSlashDetector(1)
	log := &mockLog{} // no equivocation

	records := sd.CheckEquivocation(log)
	if len(records) > 0 {
		t.Error("clean log should not trigger equivocation")
	}
	if sd.HasViolations() {
		t.Error("no violations should be recorded for a clean log")
	}
}

func TestSlash_Equivocation_Detected(t *testing.T) {
	sd := NewSlashDetector(1)
	log := &mockLog{
		senderID: "mallory",
		envA:     &LogEntry{SenderID: "mallory", Seq: 1, Payload: []byte("fold")},
		envB:     &LogEntry{SenderID: "mallory", Seq: 1, Payload: []byte("raise 100")},
	}

	records := sd.CheckEquivocation(log)
	if len(records) == 0 {
		t.Fatal("expected equivocation to be detected")
	}
	if records[0].Reason != SlashEquivocation {
		t.Errorf("expected SlashEquivocation, got %v", records[0].Reason)
	}
	if records[0].PeerID != "mallory" {
		t.Errorf("expected mallory, got %s", records[0].PeerID)
	}
	if !sd.IsSlashed("mallory") {
		t.Error("mallory should be marked as slashed")
	}
}

func TestSlash_BadZKProof(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"a", "b"}, []byte("n"))
	sd := NewSlashDetector(1)

	ct := pokercrypto.CardToField(5, p)
	realResult, _ := key.Decrypt(ct)
	proof, _ := pokercrypto.ProveDecryption(key, ct, realResult, sid)

	// Create a tampered partial decryption.
	fakeResult := pokercrypto.CardToField(20, p)
	tampered := &pokercrypto.PartialDecryption{
		PlayerID:   "mallory",
		CardIndex:  5,
		Ciphertext: ct,
		Result:     fakeResult, // wrong!
		Proof:      proof,      // proof is for realResult, not fakeResult
	}

	record := sd.CheckPartialDecryption(tampered, p, sid)
	if record == nil {
		t.Error("expected slash record for bad ZK proof")
	}
	if record.Reason != SlashBadZKProof {
		t.Errorf("expected SlashBadZKProof, got %v", record.Reason)
	}
	if record.PeerID != "mallory" {
		t.Errorf("expected mallory, got %s", record.PeerID)
	}
}

func TestSlash_ValidProofNotSlashed(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"a", "b"}, []byte("n"))
	sd := NewSlashDetector(1)

	ct := pokercrypto.CardToField(7, p)
	result, _ := key.Decrypt(ct)
	proof, _ := pokercrypto.ProveDecryption(key, ct, result, sid)

	pd := &pokercrypto.PartialDecryption{
		PlayerID:   "alice",
		CardIndex:  7,
		Ciphertext: ct,
		Result:     result,
		Proof:      proof,
	}

	record := sd.CheckPartialDecryption(pd, p, sid)
	if record != nil {
		t.Error("valid proof should not produce a slash record")
	}
	if sd.IsSlashed("alice") {
		t.Error("honest player should not be slashed")
	}
}

func TestSlash_InvalidAction(t *testing.T) {
	sd := NewSlashDetector(1)
	record := sd.CheckInvalidAction("cheater", "cannot raise below minimum")

	if record == nil {
		t.Fatal("expected slash record")
	}
	if record.Reason != SlashInvalidAction {
		t.Errorf("expected SlashInvalidAction, got %v", record.Reason)
	}
	if !sd.IsSlashed("cheater") {
		t.Error("cheater should be marked as slashed")
	}
}

func TestSlash_KeyWithholding(t *testing.T) {
	sd := NewSlashDetector(1)
	record := sd.CheckKeyWithholding("lazy", 3)

	if record == nil {
		t.Fatal("expected slash record")
	}
	if record.Reason != SlashKeyWithholding {
		t.Errorf("expected SlashKeyWithholding, got %v", record.Reason)
	}
	if record.BadProofCardIdx != 3 {
		t.Errorf("expected card index 3, got %d", record.BadProofCardIdx)
	}
}

func TestSlash_OnSlashCallback(t *testing.T) {
	sd := NewSlashDetector(1)
	var called int32
	sd.OnSlash = func(r *SlashRecord) {
		atomic.AddInt32(&called, 1)
	}

	sd.CheckInvalidAction("bad-actor", "cheated")
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&called) == 0 {
		t.Error("OnSlash callback should have been called")
	}
}

func TestSlash_MultipleRecords(t *testing.T) {
	sd := NewSlashDetector(1)
	sd.CheckInvalidAction("player1", "error 1")
	sd.CheckInvalidAction("player1", "error 2")
	sd.CheckKeyWithholding("player2", 0)

	records := sd.Records()
	if len(records) != 3 {
		t.Errorf("expected 3 records, got %d", len(records))
	}
}

// ── FaultManager integration tests ───────────────────────────────────────────

func TestFaultManager_RegisterAndHeartbeat(t *testing.T) {
	cfg := FaultConfig{
		HeartbeatTimeout: 100 * time.Millisecond,
		Prime:            pokercrypto.SharedPrime(),
	}
	fm := NewFaultManager("local", 1, cfg)
	fm.RegisterPlayers([]string{"local", "alice", "bob"})

	fm.RecordHeartbeat("alice")
	fm.RecordHeartbeat("bob")

	if fm.PeerStatus("alice") != PeerAlive {
		t.Error("alice should be alive after heartbeat")
	}
}

func TestFaultManager_TimeoutVoteFlow(t *testing.T) {
	cfg := FaultConfig{
		HeartbeatTimeout: 50 * time.Millisecond,
		VoteExpiry:       5 * time.Second,
		Prime:            pokercrypto.SharedPrime(),
	}
	fm := NewFaultManager("local", 1, cfg)
	fm.RegisterPlayers([]string{"local", "alice", "bob", "carol"})

	var folded string
	fm.OnPlayerFolded = func(peerID string) {
		folded = peerID
	}

	// alice times out.
	fm.StartTimeoutVote("alice")
	fm.HandleTimeoutVote("alice", "bob", true)
	fm.HandleTimeoutVote("alice", "carol", true)

	time.Sleep(20 * time.Millisecond)
	if folded != "alice" {
		t.Errorf("expected alice to be folded, got %q", folded)
	}
}

func TestFaultManager_ApplyTimeoutFold(t *testing.T) {
	players := []*game.Player{
		game.NewPlayer("A", "Alice", 500),
		game.NewPlayer("B", "Bob", 500),
	}
	gs := game.NewGameState("t1", 1, players, 0, 5, 10)
	gs.Phase = game.PhasePreFlop

	action, err := ApplyTimeoutFold(gs, "A")
	if err != nil {
		t.Fatalf("ApplyTimeoutFold: %v", err)
	}
	if action.Type != game.ActionFold {
		t.Errorf("expected Fold action, got %v", action.Type)
	}
	if action.PlayerID != "A" {
		t.Errorf("expected player A, got %s", action.PlayerID)
	}
}

func TestFaultManager_ApplyTimeoutFold_AlreadyFolded(t *testing.T) {
	players := []*game.Player{
		game.NewPlayer("A", "Alice", 500),
		game.NewPlayer("B", "Bob", 500),
	}
	gs := game.NewGameState("t1", 1, players, 0, 5, 10)
	players[0].Status = game.StatusFolded

	_, err := ApplyTimeoutFold(gs, "A")
	if err == nil {
		t.Error("expected error for already-folded player")
	}
}

func TestFaultManager_KeyShareFlow(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)

	cfg := FaultConfig{
		Prime:           p,
		ShamirThreshold: 2,
	}
	fm := NewFaultManager("local", 1, cfg)
	fm.RegisterPlayers([]string{"local", "alice", "bob", "carol"})

	// Simulate receiving shares from other nodes.
	shares, threshold, _ := SplitAndDistribute(key, 4)

	fm.StoreKeyShare("bob", shares[0])
	fm.AddReconstructionShare("bob", shares[1])
	fm.AddReconstructionShare("bob", shares[2])

	reconstructed, ok := fm.TryReconstructKey("bob")
	if !ok {
		t.Fatal("expected successful key reconstruction")
	}
	if reconstructed == nil {
		t.Fatal("reconstructed key is nil")
	}
	_ = threshold

	// Verify the key works.
	m := pokercrypto.CardToField(33, p)
	enc, _ := key.Encrypt(m)
	dec, err := reconstructed.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt with reconstructed key: %v", err)
	}
	if m.Cmp(dec) != 0 {
		t.Error("reconstructed key decrypts incorrectly")
	}
}

func TestFaultManager_SlashCallbackFires(t *testing.T) {
	cfg := FaultConfig{Prime: pokercrypto.SharedPrime()}
	fm := NewFaultManager("local", 1, cfg)
	fm.RegisterPlayers([]string{"local", "badguy"})

	var mu sync.Mutex
	var slashed []string
	fm.OnSlash = func(r *SlashRecord) {
		mu.Lock()
		slashed = append(slashed, r.PeerID)
		mu.Unlock()
	}

	fm.RecordInvalidAction("badguy", "sent illegal raise")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(slashed) == 0 {
		t.Error("slash callback should have fired")
	}
	if slashed[0] != "badguy" {
		t.Errorf("expected badguy slashed, got %s", slashed[0])
	}
}

// ── HeartbeatSender tests ─────────────────────────────────────────────────────

func TestHeartbeatSender_SendsBeats(t *testing.T) {
	var count int32
	sender := NewHeartbeatSender("me", 20*time.Millisecond, func(seq int64) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	ctx, cancel := newCancelContext(100 * time.Millisecond)
	defer cancel()
	go sender.Run(ctx)

	time.Sleep(120 * time.Millisecond)
	n := atomic.LoadInt32(&count)
	if n < 3 {
		t.Errorf("expected at least 3 heartbeats in 100ms, got %d", n)
	}
}

func TestHeartbeatSender_StopsOnContextCancel(t *testing.T) {
	var count int32
	sender := NewHeartbeatSender("me", 20*time.Millisecond, func(seq int64) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	ctx, cancel := newCancelContext(50 * time.Millisecond)
	cancel() // cancel immediately

	err := sender.Run(ctx)
	if err == nil {
		t.Error("expected context error on cancelled context")
	}
}

// ── SplitAndDistribute tests ──────────────────────────────────────────────────

func TestSplitAndDistribute_ThresholdIsHalfN(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)

	for _, n := range []int{2, 3, 4, 5, 6} {
		_, threshold, err := SplitAndDistribute(key, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		expected := (n + 1) / 2
		if expected < 2 {
			expected = 2
		}
		if threshold != expected {
			t.Errorf("n=%d: expected threshold %d, got %d", n, expected, threshold)
		}
	}
}

func TestSplitAndDistribute_TooFewPlayers(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	_, _, err := SplitAndDistribute(key, 1)
	if err == nil {
		t.Error("expected error for 1 player")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newCancelContext returns a context that auto-cancels after d.
func newCancelContext(d time.Duration) (context.Context, func()) {
	ch := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(ch) }) }
	go func() {
		time.Sleep(d)
		cancel()
	}()
	return &cancelCtx{ch: ch}, cancel
}

type cancelCtx struct {
	ch <-chan struct{}
}

func (c *cancelCtx) Done() <-chan struct{} { return c.ch }
func (c *cancelCtx) Err() error {
	select {
	case <-c.ch:
		return fmt.Errorf("context cancelled")
	default:
		return nil
	}
}
func (c *cancelCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelCtx) Value(key interface{}) interface{} { return nil }
