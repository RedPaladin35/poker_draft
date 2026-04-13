package crypto

import (
	"bytes"
	"math/big"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// smallPrime is a small safe prime for fast unit tests (not for production).
// P = 23 (safe prime: (23-1)/2 = 11, also prime).
var smallPrime = big.NewInt(23)

// testPrime returns the 2048-bit shared prime for integration tests.
func testPrime() *big.Int { return SharedPrime() }

// makeSession returns a deterministic session ID for tests.
func makeSession(players []string) []byte {
	return SessionID(players, []byte("test-nonce-12345"))
}

// ─── SRA key tests ────────────────────────────────────────────────────────────

func TestGenerateSRAKey_ValidPair(t *testing.T) {
	key, err := GenerateSRAKey(testPrime())
	if err != nil {
		t.Fatalf("GenerateSRAKey: %v", err)
	}
	if !key.VerifyKeyPair() {
		t.Error("key pair verification failed: e*d ≢ 1 mod (P-1)")
	}
}

func TestGenerateSRAKey_InvalidPrime(t *testing.T) {
	_, err := GenerateSRAKey(big.NewInt(10)) // composite
	if err == nil {
		t.Error("expected error for composite modulus, got nil")
	}
}

func TestSRA_EncryptDecryptRoundTrip(t *testing.T) {
	key, _ := GenerateSRAKey(testPrime())
	p := testPrime()

	// Use a valid field element: m = 2 (small, well within [1, P-1]).
	m := big.NewInt(2)
	c, err := key.Encrypt(m)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	recovered, err := key.Decrypt(c)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if recovered.Cmp(m) != 0 {
		t.Errorf("round-trip failed: got %s, want %s (mod %s)", recovered, m, p)
	}
}

func TestSRA_Commutativity(t *testing.T) {
	// Core property: E_A(E_B(x)) == E_B(E_A(x))
	p := testPrime()
	keyA, _ := GenerateSRAKey(p)
	keyB, _ := GenerateSRAKey(p)

	m := CardToField(7, p) // some card

	// E_A(E_B(m))
	eb, _ := keyB.Encrypt(m)
	eaeb, _ := keyA.Encrypt(eb)

	// E_B(E_A(m))
	ea, _ := keyA.Encrypt(m)
	ebea, _ := keyB.Encrypt(ea)

	if eaeb.Cmp(ebea) != 0 {
		t.Errorf("commutativity violated: E_A(E_B(m))=%s ≠ E_B(E_A(m))=%s", eaeb, ebea)
	}
}

func TestSRA_DecryptInOrder(t *testing.T) {
	// Verify D_A(D_B(E_B(E_A(m)))) == m
	p := testPrime()
	keyA, _ := GenerateSRAKey(p)
	keyB, _ := GenerateSRAKey(p)

	m := CardToField(42, p)

	ea, _ := keyA.Encrypt(m)
	eaeb, _ := keyB.Encrypt(ea)

	// Decrypt B first, then A (any order works due to commutativity).
	decB, _ := keyB.Decrypt(eaeb)
	decA, _ := keyA.Decrypt(decB)

	if decA.Cmp(m) != 0 {
		t.Errorf("multi-key round-trip failed: got %s, want %s", decA, m)
	}
}

func TestSRA_EncryptOutOfRange(t *testing.T) {
	key, _ := GenerateSRAKey(testPrime())
	// m = 0 is invalid.
	_, err := key.Encrypt(big.NewInt(0))
	if err == nil {
		t.Error("expected error for m=0, got nil")
	}
	// m = P is invalid (must be < P).
	_, err = key.Encrypt(testPrime())
	if err == nil {
		t.Error("expected error for m=P, got nil")
	}
}

// ─── Card encoding tests ──────────────────────────────────────────────────────

func TestCardToField_AllCards(t *testing.T) {
	p := testPrime()
	seen := make(map[string]int)
	for id := 0; id <= 51; id++ {
		v := CardToField(id, p)
		key := v.String()
		if prev, ok := seen[key]; ok {
			t.Errorf("CardToField collision: card %d and card %d both map to %s", id, prev, key)
		}
		seen[key] = id
	}
}

func TestFieldToCard_RoundTrip(t *testing.T) {
	p := testPrime()
	for id := 0; id <= 51; id++ {
		v := CardToField(id, p)
		got := FieldToCard(v, p)
		if got != id {
			t.Errorf("FieldToCard(%d): got %d", id, got)
		}
	}
}

func TestFieldToCard_Unknown(t *testing.T) {
	p := testPrime()
	// Use 1 as a value that should not correspond to any card.
	if id := FieldToCard(big.NewInt(1), p); id != -1 {
		t.Errorf("expected -1 for unknown value, got %d", id)
	}
}

// ─── ZK proof tests ───────────────────────────────────────────────────────────

func TestZKProof_ValidProof(t *testing.T) {
	p := testPrime()
	key, _ := GenerateSRAKey(p)
	sid := makeSession([]string{"alice", "bob"})

	ciphertext := CardToField(5, p)
	result, _ := key.Decrypt(ciphertext)

	proof, err := ProveDecryption(key, ciphertext, result, sid)
	if err != nil {
		t.Fatalf("ProveDecryption: %v", err)
	}

	if err := VerifyDecryption(proof, ciphertext, result, p, sid); err != nil {
		t.Errorf("VerifyDecryption: %v", err)
	}
}

func TestZKProof_WrongResult_Detected(t *testing.T) {
	// Critical security test: a malicious player provides a wrong result.
	// The verifier MUST detect this.
	p := testPrime()
	key, _ := GenerateSRAKey(p)
	sid := makeSession([]string{"alice", "bob"})

	ciphertext := CardToField(5, p)
	realResult, _ := key.Decrypt(ciphertext)

	// Generate a valid proof for the real result.
	proof, _ := ProveDecryption(key, ciphertext, realResult, sid)

	// Attacker substitutes a different result.
	fakeResult := CardToField(17, p) // a different card
	err := VerifyDecryption(proof, ciphertext, fakeResult, p, sid)
	if err == nil {
		t.Error("SECURITY: tampered result passed ZK verification — this is a critical bug")
	}
}

func TestZKProof_WrongSessionID_Detected(t *testing.T) {
	// Proof generated for session A must not verify for session B.
	p := testPrime()
	key, _ := GenerateSRAKey(p)
	sidA := makeSession([]string{"session-a"})
	sidB := makeSession([]string{"session-b"})

	ciphertext := CardToField(3, p)
	result, _ := key.Decrypt(ciphertext)

	proof, _ := ProveDecryption(key, ciphertext, result, sidA)

	// Verify against a different session ID.
	err := VerifyDecryption(proof, ciphertext, result, p, sidB)
	if err == nil {
		t.Error("SECURITY: proof from session A verified against session B")
	}
}

func TestZKProof_NilProof_Rejected(t *testing.T) {
	err := VerifyDecryption(nil, big.NewInt(2), big.NewInt(3), testPrime(), []byte("sid"))
	if err == nil {
		t.Error("expected error for nil proof")
	}
}

func TestZKProof_AllCards(t *testing.T) {
	// Every card can be proved and verified.
	p := testPrime()
	key, _ := GenerateSRAKey(p)
	sid := makeSession([]string{"solo"})

	for id := 0; id <= 51; id++ {
		ct := CardToField(id, p)
		// Encrypt then decrypt to simulate the deal flow.
		enc, _ := key.Encrypt(ct)
		result, _ := key.Decrypt(enc)

		proof, err := ProveDecryption(key, enc, result, sid)
		if err != nil {
			t.Fatalf("card %d ProveDecryption: %v", id, err)
		}
		if err := VerifyDecryption(proof, enc, result, p, sid); err != nil {
			t.Errorf("card %d VerifyDecryption: %v", id, err)
		}
	}
}

// ─── Commitment tests ─────────────────────────────────────────────────────────

func TestCommitment_RoundTrip(t *testing.T) {
	data := []byte("hello poker world")
	c, err := NewCommitment(data)
	if err != nil {
		t.Fatalf("NewCommitment: %v", err)
	}
	if err := c.Verify(data); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestCommitment_TamperedData_Detected(t *testing.T) {
	data := []byte("correct deck order")
	c, _ := NewCommitment(data)

	tampered := []byte("different deck order")
	if err := c.Verify(tampered); err == nil {
		t.Error("tampered data passed commitment verification")
	}
}

func TestDeckCommitment(t *testing.T) {
	p := testPrime()
	deck := BuildPlaintextDeck(p)

	c, err := NewDeckCommitment(deck)
	if err != nil {
		t.Fatalf("NewDeckCommitment: %v", err)
	}
	if err := c.VerifyDeck(deck); err != nil {
		t.Errorf("VerifyDeck: %v", err)
	}

	// Swap two cards — verification must fail.
	tampered := make([]*big.Int, len(deck))
	copy(tampered, deck)
	tampered[0], tampered[1] = tampered[1], tampered[0]
	if err := c.VerifyDeck(tampered); err == nil {
		t.Error("swapped deck passed commitment verification")
	}
}

// ─── Shamir Secret Sharing tests ──────────────────────────────────────────────

func TestShamir_RoundTrip(t *testing.T) {
	p := testPrime()
	secret := big.NewInt(42)
	n, threshold := 5, 3

	shares, err := SplitSecret(secret, threshold, n, p)
	if err != nil {
		t.Fatalf("SplitSecret: %v", err)
	}
	if len(shares) != n {
		t.Fatalf("expected %d shares, got %d", n, len(shares))
	}

	// Reconstruct from exactly threshold shares.
	recovered, err := ReconstructSecret(shares[:threshold], p)
	if err != nil {
		t.Fatalf("ReconstructSecret: %v", err)
	}
	if recovered.Cmp(secret) != 0 {
		t.Errorf("reconstructed %s, want %s", recovered, secret)
	}
}

func TestShamir_AnyThresholdShares(t *testing.T) {
	p := testPrime()
	secret := big.NewInt(99)
	shares, _ := SplitSecret(secret, 3, 5, p)

	// Reconstruct using different subsets of 3 shares.
	subsets := [][]ShamirShare{
		{shares[0], shares[1], shares[2]},
		{shares[1], shares[2], shares[3]},
		{shares[0], shares[3], shares[4]},
	}
	for i, subset := range subsets {
		got, err := ReconstructSecret(subset, p)
		if err != nil {
			t.Fatalf("subset %d: %v", i, err)
		}
		if got.Cmp(secret) != 0 {
			t.Errorf("subset %d: got %s, want %s", i, got, secret)
		}
	}
}

func TestShamir_TwoThreshold(t *testing.T) {
	p := testPrime()
	secret := big.NewInt(7)
	shares, _ := SplitSecret(secret, 2, 4, p)

	got, err := ReconstructSecret(shares[:2], p)
	if err != nil {
		t.Fatalf("ReconstructSecret: %v", err)
	}
	if got.Cmp(secret) != 0 {
		t.Errorf("got %s, want %s", got, secret)
	}
}

// ─── Shuffle protocol tests ───────────────────────────────────────────────────

func TestShuffle_FourPlayers(t *testing.T) {
	p := testPrime()
	players := []string{"alice", "bob", "carol", "dave"}
	sid := makeSession(players)

	keys := make([]*SRAKey, len(players))
	for i := range keys {
		var err error
		keys[i], err = GenerateSRAKey(p)
		if err != nil {
			t.Fatalf("key gen %d: %v", i, err)
		}
	}

	sp := NewShuffleProtocol(p, sid)
	initial := BuildPlaintextDeck(p)

	finalDeck, steps, err := sp.RunFullShuffle(players, keys, initial)
	if err != nil {
		t.Fatalf("RunFullShuffle: %v", err)
	}

	// Verify 4 steps produced.
	if len(steps) != 4 {
		t.Errorf("expected 4 steps, got %d", len(steps))
	}

	// Final deck must still have 52 distinct values.
	if len(finalDeck) != 52 {
		t.Fatalf("expected 52 cards, got %d", len(finalDeck))
	}
	unique := make(map[string]bool)
	for _, v := range finalDeck {
		unique[v.String()] = true
	}
	if len(unique) != 52 {
		t.Errorf("final deck has %d unique values (expected 52) — collision or duplicate", len(unique))
	}

	// The final deck must NOT equal the initial deck (it's been shuffled).
	sameOrder := true
	for i, v := range initial {
		if finalDeck[i].Cmp(v) != 0 {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Error("final deck has the same order as initial — shuffle had no effect")
	}
}

func TestShuffle_CommitmentsVerify(t *testing.T) {
	p := testPrime()
	players := []string{"p1", "p2", "p3"}
	sid := makeSession(players)

	keys := make([]*SRAKey, 3)
	for i := range keys {
		keys[i], _ = GenerateSRAKey(p)
	}

	sp := NewShuffleProtocol(p, sid)
	_, steps, err := sp.RunFullShuffle(players, keys, BuildPlaintextDeck(p))
	if err != nil {
		t.Fatalf("shuffle: %v", err)
	}

	for _, step := range steps {
		if err := sp.VerifyStep(step); err != nil {
			t.Errorf("step commitment failed for %s: %v", step.PlayerID, err)
		}
	}
}

// ─── Deal protocol tests ──────────────────────────────────────────────────────

func setup4PlayerDeal(t *testing.T) (*DealProtocol, []string, []*SRAKey) {
	t.Helper()
	p := testPrime()
	players := []string{"alice", "bob", "carol", "dave"}
	sid := makeSession(players)

	keys := make([]*SRAKey, len(players))
	for i := range keys {
		var err error
		keys[i], err = GenerateSRAKey(p)
		if err != nil {
			t.Fatalf("key gen: %v", err)
		}
	}

	sp := NewShuffleProtocol(p, sid)
	finalDeck, _, err := sp.RunFullShuffle(players, keys, BuildPlaintextDeck(p))
	if err != nil {
		t.Fatalf("shuffle: %v", err)
	}

	ed, err := NewEncryptedDeck(finalDeck, p, sid)
	if err != nil {
		t.Fatalf("NewEncryptedDeck: %v", err)
	}

	return NewDealProtocol(ed, players, keys), players, keys
}

func TestDeal_HoleCards_AllValid(t *testing.T) {
	dp, _, _ := setup4PlayerDeal(t)

	holeCards, err := dp.DealHoleCards(0) // dealer = seat 0
	if err != nil {
		t.Fatalf("DealHoleCards: %v", err)
	}

	// Every player must have 2 valid, non-zero cards.
	seen := make(map[int]bool)
	for i, pair := range holeCards {
		for j, card := range pair {
			zero := (card == (struct {
				Rank interface{}
				Suit interface{}
			}{}))
			_ = zero
			id := card.CardID()
			if id < 0 || id > 51 {
				t.Errorf("player %d card %d: invalid card ID %d", i, j, id)
			}
			if seen[id] {
				t.Errorf("player %d card %d: duplicate card %s (ID %d)", i, j, card, id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 8 {
		t.Errorf("expected 8 unique hole cards, got %d", len(seen))
	}
}

func TestDeal_HoleCardsPrivate(t *testing.T) {
	// Critical: player A must NOT be able to decrypt player B's hole card
	// without the partial decryptions from other players.
	p := testPrime()
	players := []string{"alice", "bob"}
	sid := makeSession(players)

	keys := make([]*SRAKey, 2)
	for i := range keys {
		keys[i], _ = GenerateSRAKey(p)
	}

	sp := NewShuffleProtocol(p, sid)
	finalDeck, _, _ := sp.RunFullShuffle(players, keys, BuildPlaintextDeck(p))
	ed, _ := NewEncryptedDeck(finalDeck, p, sid)

	// Card at slot 0 is bob's (first card dealt, to seat left of dealer=0 → seat 1 = bob).
	ciphertext := finalDeck[0]

	// Alice tries to decrypt bob's card using only her key.
	aliceDecrypt, _ := keys[0].Decrypt(ciphertext)

	// aliceDecrypt is still encrypted under Bob's key — it must NOT be a valid card.
	cardID := FieldToCard(aliceDecrypt, p)
	// It may happen to decode as a card by coincidence (extremely rare), but
	// more importantly it will not be the CORRECT card without Bob's decryption.
	// We test this by checking the full two-step decryption produces a different result.
	bobDecryptOfAlice, _ := keys[1].Decrypt(aliceDecrypt)
	aliceDecryptOfBob, _ := keys[0].Decrypt(finalDeck[0])
	bobFull, _ := keys[1].Decrypt(aliceDecryptOfBob)

	// The correct card requires both decryptions.
	if cardID != -1 {
		// Alice's partial decryption happened to look like a card.
		// It must not equal the actual card.
		if aliceDecrypt.Cmp(bobDecryptOfAlice) != 0 {
			// Different from correct result — privacy holds.
		}
	}
	// The actual plaintext must equal what full decryption produces.
	if aliceDecrypt.Cmp(bobFull) == 0 {
		t.Error("PRIVACY: alice's single-key decryption revealed the actual card")
	}
}

func TestDeal_CommunityCards(t *testing.T) {
	dp, _, _ := setup4PlayerDeal(t)

	// Community cards start at position 2*4 = 8 (after 4 players × 2 hole cards).
	// Batches: flop=3, turn=1, river=1.
	startPos := 8
	batches, err := dp.DealCommunityCards(startPos, []int{3, 1, 1})
	if err != nil {
		t.Fatalf("DealCommunityCards: %v", err)
	}

	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Errorf("flop: expected 3 cards, got %d", len(batches[0]))
	}
	if len(batches[1]) != 1 {
		t.Errorf("turn: expected 1 card, got %d", len(batches[1]))
	}
	if len(batches[2]) != 1 {
		t.Errorf("river: expected 1 card, got %d", len(batches[2]))
	}

	// All 5 community cards must be valid.
	for b, batch := range batches {
		for j, card := range batch {
			if id := card.CardID(); id < 0 || id > 51 {
				t.Errorf("batch %d card %d: invalid card ID %d", b, j, id)
			}
		}
	}
}

func TestDeal_MaliciousDecryption_Detected(t *testing.T) {
	// If a player provides a wrong partial decryption, the ZK proof must fail.
	p := testPrime()
	players := []string{"alice", "bob", "carol"}
	sid := makeSession(players)

	keys := make([]*SRAKey, 3)
	for i := range keys {
		keys[i], _ = GenerateSRAKey(p)
	}

	sp := NewShuffleProtocol(p, sid)
	finalDeck, _, _ := sp.RunFullShuffle(players, keys, BuildPlaintextDeck(p))
	ed, _ := NewEncryptedDeck(finalDeck, p, sid)
	dp := NewDealProtocol(ed, players, keys)

	// Get a valid partial decryption.
	ciphertext := finalDeck[0]
	realResult, _ := keys[0].Decrypt(ciphertext)
	validProof, _ := ProveDecryption(keys[0], ciphertext, realResult, sid)

	validPD := PartialDecryption{
		PlayerID:   "alice",
		CardIndex:  0,
		Ciphertext: ciphertext,
		Result:     realResult,
		Proof:      validProof,
	}

	// Tamper with the result.
	wrongResult := CardToField(50, dp.Deck.P)
	tampered := SubstitutePartialDecryption(&validPD, wrongResult)

	err := tampered.Verify(p, sid)
	if err == nil {
		t.Error("SECURITY: malicious partial decryption passed ZK verification")
	}
}

func TestDeal_NoCardDuplicate_FullHand(t *testing.T) {
	// No card should appear more than once across hole cards + community cards.
	dp, _, _ := setup4PlayerDeal(t)

	seen := make(map[int]bool)

	holeCards, err := dp.DealHoleCards(0)
	if err != nil {
		t.Fatalf("DealHoleCards: %v", err)
	}
	for _, pair := range holeCards {
		for _, card := range pair {
			id := card.CardID()
			if seen[id] {
				t.Errorf("duplicate card: %s (id %d)", card, id)
			}
			seen[id] = true
		}
	}

	community, err := dp.DealCommunityCards(8, []int{3, 1, 1})
	if err != nil {
		t.Fatalf("DealCommunityCards: %v", err)
	}
	for _, batch := range community {
		for _, card := range batch {
			id := card.CardID()
			if seen[id] {
				t.Errorf("community card duplicate: %s (id %d)", card, id)
			}
			seen[id] = true
		}
	}
}

// ─── CryptoGame integration test ──────────────────────────────────────────────

func TestCryptoGame_FullProtocol(t *testing.T) {
	players := []string{"alice", "bob", "carol", "dave"}
	nonce := []byte("integration-test-nonce")

	cg, err := NewCryptoGame(players, nonce)
	if err != nil {
		t.Fatalf("NewCryptoGame: %v", err)
	}

	if err := cg.RunShuffle(); err != nil {
		t.Fatalf("RunShuffle: %v", err)
	}

	if cg.Deck == nil {
		t.Fatal("Deck is nil after RunShuffle")
	}
	if len(cg.ShuffleLog) != len(players) {
		t.Errorf("expected %d shuffle steps, got %d", len(players), len(cg.ShuffleLog))
	}

	// Verify all shuffle commitments.
	sp := NewShuffleProtocol(cg.P, cg.SessionID)
	for _, step := range cg.ShuffleLog {
		if err := sp.VerifyStep(step); err != nil {
			t.Errorf("shuffle step %s: %v", step.PlayerID, err)
		}
	}
}

func TestCryptoGame_SessionID_Binding(t *testing.T) {
	players := []string{"alice", "bob"}
	sid1 := SessionID(players, []byte("nonce-1"))
	sid2 := SessionID(players, []byte("nonce-2"))

	if bytes.Equal(sid1, sid2) {
		t.Error("different nonces produced the same session ID")
	}

	playersB := []string{"carol", "dave"}
	sid3 := SessionID(playersB, []byte("nonce-1"))
	if bytes.Equal(sid1, sid3) {
		t.Error("different player sets produced the same session ID")
	}
}
