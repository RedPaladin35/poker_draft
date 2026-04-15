package integration

import (
	"math/big"
	"math/rand"
	"testing"
	"time"

	pokercrypto "github.com/p2p-poker/internal/crypto"
	"github.com/p2p-poker/internal/fault"
	"github.com/p2p-poker/internal/game"
)

// adversarialNode simulates a malicious peer that attempts various attacks.
type adversarialNode struct {
	id  string
	key *pokercrypto.SRAKey
	p   *big.Int
	sid []byte
}

func newAdversarialNode(id string) *adversarialNode {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{id, "honest"}, []byte("test"))
	return &adversarialNode{id: id, key: key, p: p, sid: sid}
}

// badZKProof returns a partial decryption with a tampered result.
func (a *adversarialNode) badZKProof(cardIdx int, correctCiphertext *big.Int) *pokercrypto.PartialDecryption {
	realResult, _ := a.key.Decrypt(correctCiphertext)
	proof, _ := pokercrypto.ProveDecryption(a.key, correctCiphertext, realResult, a.sid)
	// Substitute a fake result — the proof was generated for the real result.
	fakeResult := pokercrypto.CardToField(51-cardIdx, a.p)
	return &pokercrypto.PartialDecryption{
		PlayerID:   a.id,
		CardIndex:  cardIdx,
		Ciphertext: correctCiphertext,
		Result:     fakeResult,
		Proof:      proof,
	}
}

// ── Attack: bad ZK proof ──────────────────────────────────────────────────────

func TestAdversarial_BadZKProof_DetectedAndSlashed(t *testing.T) {
	attacker := newAdversarialNode("mallory")
	sd := fault.NewSlashDetector(1)

	ct := pokercrypto.CardToField(7, attacker.p)
	badPD := attacker.badZKProof(7, ct)

	record := sd.CheckPartialDecryption(badPD, attacker.p, attacker.sid)
	if record == nil {
		t.Fatal("SECURITY: bad ZK proof was not detected")
	}
	if record.Reason != fault.SlashBadZKProof {
		t.Errorf("expected SlashBadZKProof, got %v", record.Reason)
	}
	if !sd.IsSlashed("mallory") {
		t.Error("attacker should be marked as slashed")
	}
	t.Logf("Attack correctly caught: %s", record)
}

// ── Attack: equivocation ──────────────────────────────────────────────────────

func TestAdversarial_Equivocation_DetectedByMockLog(t *testing.T) {
	sd := fault.NewSlashDetector(1)

	// Simulate a log that reports equivocation.
	log := &equivocatingLog{
		senderID: "mallory",
		envA:     &fault.LogEntry{SenderID: "mallory", Seq: 1, Payload: []byte("fold")},
		envB:     &fault.LogEntry{SenderID: "mallory", Seq: 1, Payload: []byte("raise 500")},
	}

	records := sd.CheckEquivocation(log)
	if len(records) == 0 {
		t.Fatal("SECURITY: equivocation not detected")
	}
	if records[0].PeerID != "mallory" {
		t.Errorf("wrong offender: %s", records[0].PeerID)
	}
	if records[0].EnvA == nil || records[0].EnvB == nil {
		t.Error("both conflicting messages should be recorded as evidence")
	}
	t.Logf("Equivocation caught: %s signed both %q and %q at seq 1",
		records[0].PeerID, records[0].EnvA.Payload, records[0].EnvB.Payload)
}

// equivocatingLog implements fault.EquivocationChecker for adversarial tests.
type equivocatingLog struct {
	senderID string
	envA     *fault.LogEntry
	envB     *fault.LogEntry
}

func (l *equivocatingLog) DetectEquivocation() (string, *fault.LogEntry, *fault.LogEntry) {
	return l.senderID, l.envA, l.envB
}

// ── Attack: invalid action ────────────────────────────────────────────────────

func TestAdversarial_InvalidAction_RejectedByEngine(t *testing.T) {
	players := makePlayers(2, 200)
	rng := newSeededRng(77)
	gs := game.NewGameState("t1", 1, players, 0, 5, 10)
	m := game.NewMachine(gs, rng)
	m.StartHand()

	sd := fault.NewSlashDetector(1)

	current := gs.CurrentPlayer()
	// Wrong player tries to act.
	wrongAction := game.Action{PlayerID: "nobody", Type: game.ActionFold}
	err := m.ApplyAction(wrongAction)
	if err == nil {
		t.Fatal("game engine should reject action from wrong player")
	}

	// Record as a slash.
	record := sd.CheckInvalidAction("nobody", err.Error())
	if record == nil {
		t.Fatal("expected slash record for invalid action")
	}
	t.Logf("Invalid action caught: %s tried to act — it was %s's turn", "nobody", current.ID)
}

// ── Attack: key withholding ───────────────────────────────────────────────────

func TestAdversarial_KeyWithholding_SlashRecordCreated(t *testing.T) {
	// Simulate: card slot 3 needs a partial decryption from "mallory"
	// but mallory never provides it within the timeout window.
	sd := fault.NewSlashDetector(1)

	// After waiting for the key, we record a withholding slash.
	record := sd.CheckKeyWithholding("mallory", 3)
	if record == nil {
		t.Fatal("expected slash record for key withholding")
	}
	if record.BadProofCardIdx != 3 {
		t.Errorf("expected card index 3, got %d", record.BadProofCardIdx)
	}
	if !sd.IsSlashed("mallory") {
		t.Error("key-withholder should be slashed")
	}
}

// ── Attack: timeout abuse ─────────────────────────────────────────────────────

func TestAdversarial_TimeoutAbuse_SingleVoteNotEnough(t *testing.T) {
	// A single malicious peer cannot force-fold another player alone.
	// They need 2/3 majority.
	tm := fault.NewTimeoutManager(1, 4, 5*time.Second)
	// 4 players, 3 eligible voters (excluding target). Need ceil(3*2/3)=2 votes.

	// Mallory votes alone.
	v := tm.StartVote("alice", "mallory")
	if v.Status == fault.VoteConfirmed {
		t.Error("SECURITY: single vote should not be enough to confirm timeout")
	}
	if v.YesCount() != 1 {
		t.Errorf("expected 1 vote, got %d", v.YesCount())
	}
	t.Log("Single-peer timeout abuse correctly prevented: need 2/3 majority")
}

// ── Attack: correct behaviour passes through unchanged ───────────────────────

func TestAdversarial_HonestPlayer_NeverSlashed(t *testing.T) {
	p := pokercrypto.SharedPrime()
	key, _ := pokercrypto.GenerateSRAKey(p)
	sid := pokercrypto.SessionID([]string{"alice", "bob"}, []byte("nonce"))
	sd := fault.NewSlashDetector(1)

	// Alice provides 10 correct partial decryptions.
	for cardIdx := 0; cardIdx < 10; cardIdx++ {
		ct := pokercrypto.CardToField(cardIdx, p)
		result, _ := key.Decrypt(ct)
		proof, _ := pokercrypto.ProveDecryption(key, ct, result, sid)
		pd := &pokercrypto.PartialDecryption{
			PlayerID:   "alice",
			CardIndex:  cardIdx,
			Ciphertext: ct,
			Result:     result,
			Proof:      proof,
		}
		record := sd.CheckPartialDecryption(pd, p, sid)
		if record != nil {
			t.Errorf("card %d: honest player should not be slashed: %s", cardIdx, record)
		}
	}

	if sd.IsSlashed("alice") {
		t.Error("honest player should never be slashed")
	}
	if sd.HasViolations() {
		t.Error("no violations should be recorded for honest player")
	}
}

// ── Chip conservation under adversarial fold injection ────────────────────────

func TestAdversarial_ForcedFold_ChipsConserved(t *testing.T) {
	// Simulate a timeout fold mid-hand and verify chip conservation.
	players := makePlayers(4, 300)
	rng := newSeededRng(42)
	gs := game.NewGameState("t1", 1, players, 0, 5, 10)
	m := game.NewMachine(gs, rng)
	m.StartHand()

	// Force player-2 to fold via timeout after the first action.
	current := gs.CurrentPlayer()
	if current != nil {
		// First player acts normally.
		toCall := gs.CurrentBet - current.CurrentBet
		var a game.Action
		if toCall > 0 {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCall}
		} else {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCheck}
		}
		m.ApplyAction(a)
	}

	// Now force player-2 to fold (timeout simulation).
	foldAction, err := fault.ApplyTimeoutFold(gs, "player-2")
	if err == nil {
		m.ApplyAction(foldAction)
	}

	// Continue the hand to completion.
	for limit := 300; gs.Phase != game.PhaseSettled && limit > 0; limit-- {
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		toCall := gs.CurrentBet - current.CurrentBet
		var a game.Action
		if toCall > 0 {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCall}
		} else {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCheck}
		}
		m.ApplyAction(a)
	}

	if gs.Phase != game.PhaseSettled {
		t.Fatalf("hand did not settle after forced fold")
	}

	var total int64
	for _, p := range players {
		total += p.Stack
	}
	if total != 1200 {
		t.Errorf("chip conservation after forced fold: got %d, want 1200", total)
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

func newSeededRng(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
