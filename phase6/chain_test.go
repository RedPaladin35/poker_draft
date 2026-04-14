package chain

import (
	"context"
	"math/big"
	"testing"

	"github.com/p2p-poker/internal/fault"
	"github.com/p2p-poker/internal/game"
)

// ── Client tests ──────────────────────────────────────────────────────────────

func TestNewClient_RequiresRPCURL(t *testing.T) {
	_, err := NewClient(context.Background(), ChainConfig{
		ContractAddress: "0xabc",
	})
	if err == nil {
		t.Error("expected error for empty RPCURL")
	}
}

func TestNewClient_RequiresContractAddress(t *testing.T) {
	_, err := NewClient(context.Background(), ChainConfig{
		RPCURL: "http://localhost:8545",
	})
	if err == nil {
		t.Error("expected error for empty ContractAddress")
	}
}

func TestNewClient_ValidConfig(t *testing.T) {
	client, err := NewClient(context.Background(), ChainConfig{
		RPCURL:          "http://localhost:8545",
		ContractAddress: "0x1234567890123456789012345678901234567890",
		ChainID:         big.NewInt(31337),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	client.Close()
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("0xabc", nil)
	if cfg.RPCURL != "http://127.0.0.1:8545" {
		t.Errorf("unexpected RPCURL: %s", cfg.RPCURL)
	}
	if cfg.ChainID.Int64() != 31337 {
		t.Errorf("unexpected ChainID: %d", cfg.ChainID.Int64())
	}
	if cfg.GasLimit != 500_000 {
		t.Errorf("unexpected GasLimit: %d", cfg.GasLimit)
	}
}

func TestJoinTable_ZeroBuyIn(t *testing.T) {
	client := testClient(t)
	_, err := client.JoinTable(context.Background(), "QmPeer", big.NewInt(0))
	if err == nil {
		t.Error("expected error for zero buy-in")
	}
}

func TestJoinTable_EmptyPeerID(t *testing.T) {
	client := testClient(t)
	_, err := client.JoinTable(context.Background(), "", big.NewInt(1000))
	if err == nil {
		t.Error("expected error for empty peerID")
	}
}

func TestJoinTable_Valid(t *testing.T) {
	client := testClient(t)
	receipt, err := client.JoinTable(context.Background(), "QmAlicePeerID", big.NewInt(1_000_000_000_000_000_000))
	if err != nil {
		t.Fatalf("JoinTable: %v", err)
	}
	if receipt.Status != 1 {
		t.Errorf("expected status 1, got %d", receipt.Status)
	}
}

func TestReportOutcome_Valid(t *testing.T) {
	client := testClient(t)
	deltas := []*big.Int{
		big.NewInt(500_000),
		big.NewInt(-500_000),
	}
	stateRoot := [32]byte{0x01, 0x02, 0x03}
	sigs := [][]byte{make([]byte, 65), make([]byte, 65)}
	sigs[0][64] = 27
	sigs[1][64] = 27

	receipt, err := client.ReportOutcome(context.Background(), deltas, stateRoot, sigs, 1)
	if err != nil {
		t.Fatalf("ReportOutcome: %v", err)
	}
	if receipt.Status != 1 {
		t.Errorf("expected status 1, got %d", receipt.Status)
	}
}

func TestReportOutcome_ChipConservationViolation(t *testing.T) {
	client := testClient(t)
	deltas := []*big.Int{
		big.NewInt(1000), // doesn't sum to zero
		big.NewInt(1000),
	}
	stateRoot := [32]byte{}
	_, err := client.ReportOutcome(context.Background(), deltas, stateRoot, [][]byte{{}}, 1)
	if err == nil {
		t.Error("expected chip conservation error")
	}
}

func TestReportOutcome_NoSignatures(t *testing.T) {
	client := testClient(t)
	deltas := []*big.Int{big.NewInt(0), big.NewInt(0)}
	_, err := client.ReportOutcome(context.Background(), deltas, [32]byte{}, nil, 1)
	if err == nil {
		t.Error("expected error for no signatures")
	}
}

func TestSubmitDispute_Valid(t *testing.T) {
	client := testClient(t)
	accused := Address{0x01}
	receipt, err := client.SubmitDispute(
		context.Background(),
		accused,
		"equivocation",
		[]byte("evidence"),
		make([]byte, 65),
	)
	if err != nil {
		t.Fatalf("SubmitDispute: %v", err)
	}
	if receipt.Status != 1 {
		t.Errorf("expected status 1, got %d", receipt.Status)
	}
}

func TestSubmitDispute_NoEvidence(t *testing.T) {
	client := testClient(t)
	_, err := client.SubmitDispute(
		context.Background(),
		Address{},
		"equivocation",
		nil, // no evidence
		make([]byte, 65),
	)
	if err == nil {
		t.Error("expected error for missing evidence")
	}
}

func TestSubmitDispute_InvalidReason(t *testing.T) {
	client := testClient(t)
	_, err := client.SubmitDispute(
		context.Background(),
		Address{},
		"made_up_reason",
		[]byte("evidence"),
		make([]byte, 65),
	)
	if err == nil {
		t.Error("expected error for invalid reason")
	}
}

// ── EtherToWei / WeiToEther tests ─────────────────────────────────────────────

func TestEtherToWei(t *testing.T) {
	tests := []struct {
		input    string
		expected *big.Int
	}{
		{"1", new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)},
		{"0.1", new(big.Int).Div(
			new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
			big.NewInt(10),
		)},
	}
	for _, tt := range tests {
		got, err := EtherToWei(tt.input)
		if err != nil {
			t.Fatalf("EtherToWei(%q): %v", tt.input, err)
		}
		if got.Cmp(tt.expected) != 0 {
			t.Errorf("EtherToWei(%q): got %s, want %s", tt.input, got, tt.expected)
		}
	}
}

func TestEtherToWei_Invalid(t *testing.T) {
	_, err := EtherToWei("not-a-number")
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

func TestWeiToEther(t *testing.T) {
	oneEth := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	s := WeiToEther(oneEth)
	if s == "" {
		t.Error("WeiToEther returned empty string")
	}
}

func TestWeiToEther_Nil(t *testing.T) {
	s := WeiToEther(nil)
	if s != "0 ETH" {
		t.Errorf("WeiToEther(nil) = %q, want '0 ETH'", s)
	}
}

// ── ChipConservationCheck tests ───────────────────────────────────────────────

func TestChipConservation_Valid(t *testing.T) {
	deltas := []*big.Int{
		big.NewInt(1000),
		big.NewInt(-600),
		big.NewInt(-400),
	}
	if err := ChipConservationCheck(deltas); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestChipConservation_Violated(t *testing.T) {
	deltas := []*big.Int{
		big.NewInt(1000),
		big.NewInt(200), // both positive — violation
	}
	if err := ChipConservationCheck(deltas); err == nil {
		t.Error("expected conservation error")
	}
}

func TestChipConservation_NilDelta(t *testing.T) {
	deltas := []*big.Int{big.NewInt(0), nil}
	if err := ChipConservationCheck(deltas); err == nil {
		t.Error("expected error for nil delta")
	}
}

func TestChipConservation_Empty(t *testing.T) {
	if err := ChipConservationCheck(nil); err != nil {
		t.Errorf("empty delta slice should pass: %v", err)
	}
}

// ── BuildOutcome tests ────────────────────────────────────────────────────────

func TestBuildOutcome_NotSettled(t *testing.T) {
	gs := makeTestGameState()
	gs.Phase = game.PhasePreFlop // not settled

	_, err := BuildOutcome(gs, 1, []byte("root"), []string{"A", "B"})
	if err == nil {
		t.Error("expected error for unsettled game state")
	}
}

func TestBuildOutcome_Settled(t *testing.T) {
	gs := makeTestGameState()
	gs.Phase = game.PhaseSettled
	gs.Payouts["A"] = 100
	gs.Payouts["B"] = -100 // not actually stored, but we test delta building

	playerOrder := []string{"A", "B"}
	payload, err := BuildOutcome(gs, 1, make([]byte, 32), playerOrder)
	if err != nil {
		t.Fatalf("BuildOutcome: %v", err)
	}
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if payload.HandNum != 1 {
		t.Errorf("expected HandNum=1, got %d", payload.HandNum)
	}
	if len(payload.PayoutDeltas) != 2 {
		t.Errorf("expected 2 deltas, got %d", len(payload.PayoutDeltas))
	}
}

func TestBuildOutcome_StateRoot(t *testing.T) {
	gs := makeTestGameState()
	gs.Phase = game.PhaseSettled

	// Pass exactly 32 bytes — should be used as-is.
	root32 := make([]byte, 32)
	root32[0] = 0xAB
	payload, err := BuildOutcome(gs, 1, root32, []string{"A", "B"})
	if err != nil {
		t.Fatalf("BuildOutcome: %v", err)
	}
	if payload.StateRoot[0] != 0xAB {
		t.Error("state root not copied correctly")
	}
}

func TestBuildOutcome_MissingPlayer(t *testing.T) {
	gs := makeTestGameState()
	gs.Phase = game.PhaseSettled

	_, err := BuildOutcome(gs, 1, make([]byte, 32), []string{"A", "NONEXISTENT"})
	if err == nil {
		t.Error("expected error for missing player")
	}
}

// ── EscrowManager tests ───────────────────────────────────────────────────────

func TestEscrowManager_Join_Valid(t *testing.T) {
	client := testClient(t)
	em := NewEscrowManager(client, Address{}, nil, "table-1", 3)

	wei, _ := EtherToWei("1")
	receipt, err := em.Join(context.Background(), "QmPeer", wei)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if receipt.Status != 1 {
		t.Errorf("expected status 1, got %d", receipt.Status)
	}
}

func TestEscrowManager_BuildDispute_FromSlashRecord(t *testing.T) {
	client := testClient(t)
	em := NewEscrowManager(client, Address{}, nil, "table-1", 3)

	sr := &fault.SlashRecord{
		PeerID:   "mallory",
		Reason:   fault.SlashEquivocation,
		HandNum:  1,
		Evidence: []byte("conflicting-messages"),
	}

	req, err := em.BuildDisputeFromSlash(sr, Address{0x42})
	if err != nil {
		t.Fatalf("BuildDisputeFromSlash: %v", err)
	}
	if req.Reason != "equivocation" {
		t.Errorf("expected 'equivocation', got %q", req.Reason)
	}
	if string(req.Evidence) != "conflicting-messages" {
		t.Errorf("evidence not preserved")
	}
}

func TestEscrowManager_BuildDispute_NilRecord(t *testing.T) {
	client := testClient(t)
	em := NewEscrowManager(client, Address{}, nil, "table-1", 3)
	_, err := em.BuildDisputeFromSlash(nil, Address{})
	if err == nil {
		t.Error("expected error for nil slash record")
	}
}

func TestEscrowManager_SlashReasonMapping(t *testing.T) {
	tests := []struct {
		reason   fault.SlashReason
		expected string
	}{
		{fault.SlashEquivocation, "equivocation"},
		{fault.SlashBadZKProof, "bad_zk_proof"},
		{fault.SlashInvalidAction, "invalid_action"},
		{fault.SlashKeyWithholding, "key_withholding"},
	}
	for _, tt := range tests {
		got := slashReasonToOnChain(tt.reason)
		if got != tt.expected {
			t.Errorf("reason %v: expected %q, got %q", tt.reason, tt.expected, got)
		}
	}
}

// ── VerifyOutcomeSignature tests ──────────────────────────────────────────────

func TestVerifyOutcomeSignature_ValidStub(t *testing.T) {
	sig := make([]byte, 65)
	sig[64] = 27

	ok := VerifyOutcomeSignature(
		"table-1", 1,
		[]*big.Int{big.NewInt(0)},
		[32]byte{},
		sig,
		Address{},
	)
	if !ok {
		t.Error("expected valid signature (stub)")
	}
}

func TestVerifyOutcomeSignature_WrongLength(t *testing.T) {
	ok := VerifyOutcomeSignature("t", 1, nil, [32]byte{}, make([]byte, 32), Address{})
	if ok {
		t.Error("expected false for wrong-length signature")
	}
}

// ── outcomeDigest determinism test ────────────────────────────────────────────

func TestOutcomeDigest_Deterministic(t *testing.T) {
	deltas := []*big.Int{big.NewInt(500), big.NewInt(-500)}
	root := [32]byte{1, 2, 3}

	d1 := outcomeDigest("table-1", 1, deltas, root)
	d2 := outcomeDigest("table-1", 1, deltas, root)

	for i, b := range d1 {
		if d2[i] != b {
			t.Error("digest is not deterministic")
			break
		}
	}
}

func TestOutcomeDigest_DifferentInputsDifferentOutputs(t *testing.T) {
	deltas := []*big.Int{big.NewInt(500), big.NewInt(-500)}
	root := [32]byte{1, 2, 3}

	d1 := outcomeDigest("table-1", 1, deltas, root)
	d2 := outcomeDigest("table-2", 1, deltas, root) // different tableID

	same := true
	for i, b := range d1 {
		if d2[i] != b {
			same = false
			break
		}
	}
	if same {
		t.Error("different tableIDs should produce different digests")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func testClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), ChainConfig{
		RPCURL:          "http://localhost:8545",
		ContractAddress: "0x1234567890123456789012345678901234567890",
		ChainID:         big.NewInt(31337),
	})
	if err != nil {
		t.Fatalf("testClient: %v", err)
	}
	return client
}

func makeTestGameState() *game.GameState {
	players := []*game.Player{
		game.NewPlayer("A", "Alice", 1000),
		game.NewPlayer("B", "Bob", 1000),
	}
	gs := game.NewGameState("t1", 1, players, 0, 5, 10)
	return gs
}
