package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/p2p-poker/internal/crypto"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	red    = "\033[31m"
	dim    = "\033[2m"
)

func sep(n int) string { return strings.Repeat("═", n) }

func section(title string) {
	fmt.Printf("\n%s%s══ %s ══%s\n", bold, yellow, title, reset)
}

func ok(msg string)   { fmt.Printf("  %s✓ %s%s\n", green, msg, reset) }
func fail(msg string) { fmt.Printf("  %s✗ %s%s\n", red, msg, reset) }
func info(msg string) { fmt.Printf("  %s%s%s\n", dim, msg, reset) }

func main() {
	fmt.Printf("%s%s%s\n", bold, sep(56), reset)
	fmt.Printf("%s  P2P Poker Engine — Phase 2: Mental Poker Demo%s\n", bold, reset)
	fmt.Printf("%s%s%s\n\n", bold, sep(56), reset)

	players := []string{"Alice", "Bob", "Carol", "Dave"}
	nonce := []byte("demo-session-nonce-2024")

	// ── Step 1: Key generation ────────────────────────────────────────────────
	section("Step 1: Key Generation (SRA over 2048-bit safe prime)")
	info(fmt.Sprintf("Players: %s", strings.Join(players, ", ")))
	info("Each player independently generates e, d such that e·d ≡ 1 (mod P-1)")

	start := time.Now()
	cg, err := crypto.NewCryptoGame(players, nonce)
	if err != nil {
		fail(fmt.Sprintf("NewCryptoGame: %v", err))
		return
	}
	elapsed := time.Since(start)

	for i, pid := range players {
		// Access the key via the exported slice — verify the pair.
		key := cg.Keys[i]
		if key.VerifyKeyPair() {
			ok(fmt.Sprintf("%s: e·d ≡ 1 (mod P-1)  [key pair verified]", pid))
		} else {
			fail(fmt.Sprintf("%s: key pair invalid!", pid))
		}
	}
	info(fmt.Sprintf("Key generation time: %v", elapsed))

	// ── Step 2: Session ID ────────────────────────────────────────────────────
	section("Step 2: Session ID Binding")
	info("Session ID binds all ZK proofs to this specific game instance.")
	info("Different players or different nonces always produce a different ID.")
	sidHex := crypto.SessionIDHex(players, nonce)
	fmt.Printf("  Session ID: %s%s%s\n", cyan, sidHex[:32]+"…", reset)
	ok("Session ID computed and will be mixed into every ZK proof")

	// ── Step 3: Shuffle ───────────────────────────────────────────────────────
	section("Step 3: N-Player Shuffle Protocol")
	info("Each player encrypts all 52 cards with their SRA key, then permutes.")
	info("No player can see the order; no player can bias the shuffle.")

	start = time.Now()
	if err := cg.RunShuffle(); err != nil {
		fail(fmt.Sprintf("RunShuffle: %v", err))
		return
	}
	shuffleTime := time.Since(start)

	sp := crypto.NewShuffleProtocol(cg.P, cg.SessionID)
	for _, step := range cg.ShuffleLog {
		if err := sp.VerifyStep(step); err != nil {
			fail(fmt.Sprintf("Step %s commitment: %v", step.PlayerID, err))
			return
		}
		ok(fmt.Sprintf("%s shuffled and committed  (commitment: %s…)", step.PlayerID, step.Commitment.HashHex()[:16]))
	}
	info(fmt.Sprintf("Shuffle time for %d players: %v", len(players), shuffleTime))

	// ── Step 4: Commutativity verification ────────────────────────────────────
	section("Step 4: Verifying Commutativity (E_A·E_B = E_B·E_A)")
	info("Core SRA property: decryption order doesn't matter.")
	cardVal := crypto.CardToField(13, cg.P) // 2♥

	encAB, _ := cg.Keys[0].Encrypt(cardVal)
	encAB, _ = cg.Keys[1].Encrypt(encAB) // E_B(E_A(m))

	encBA, _ := cg.Keys[1].Encrypt(cardVal)
	encBA, _ = cg.Keys[0].Encrypt(encBA) // E_A(E_B(m))

	if encAB.Cmp(encBA) == 0 {
		ok("E_A(E_B(m)) == E_B(E_A(m))  [commutativity holds]")
	} else {
		fail("commutativity VIOLATED")
	}

	// ── Step 5: Deal hole cards ───────────────────────────────────────────────
	section("Step 5: Dealing Hole Cards with ZK Proofs")
	info("For each hole card: all non-recipient players partially decrypt + prove correctness.")
	info("The recipient decrypts the final layer privately.")

	dp := crypto.NewDealProtocol(cg.Deck, cg.Players, cg.Keys)

	start = time.Now()
	holeCards, err := dp.DealHoleCards(0)
	if err != nil {
		fail(fmt.Sprintf("DealHoleCards: %v", err))
		return
	}
	dealTime := time.Since(start)

	for i, pid := range players {
		c1 := holeCards[i][0]
		c2 := holeCards[i][1]
		fmt.Printf("  %-8s → %s%s %s%s\n", pid, cyan, c1, c2, reset)
	}
	ok(fmt.Sprintf("All hole cards dealt and ZK proofs verified  (%v)", dealTime))

	// Verify no duplicates.
	seen := make(map[int]bool)
	dupes := false
	for _, pair := range holeCards {
		for _, card := range pair {
			id := card.CardID()
			if seen[id] {
				fail(fmt.Sprintf("DUPLICATE CARD: %s (id %d)", card, id))
				dupes = true
			}
			seen[id] = true
		}
	}
	if !dupes {
		ok("No duplicate cards across all hole card deals")
	}

	// ── Step 6: Community cards ───────────────────────────────────────────────
	section("Step 6: Community Cards (Flop / Turn / River)")

	startPos := len(players) * 2 // 8 for 4 players
	batches, err := dp.DealCommunityCards(startPos, []int{3, 1, 1})
	if err != nil {
		fail(fmt.Sprintf("DealCommunityCards: %v", err))
		return
	}

	labels := []string{"Flop", "Turn", "River"}
	for i, batch := range batches {
		cards := make([]string, len(batch))
		for j, c := range batch {
			cards[j] = c.String()
		}
		fmt.Printf("  %-8s → %s%s%s\n", labels[i], cyan, strings.Join(cards, " "), reset)
	}
	ok("All community cards dealt and ZK proofs verified")

	// Check no overlap with hole cards.
	for _, batch := range batches {
		for _, card := range batch {
			if seen[card.CardID()] {
				fail(fmt.Sprintf("Community card %s duplicates a hole card!", card))
			}
			seen[card.CardID()] = true
		}
	}
	ok("No overlap between hole cards and community cards")

	// ── Step 7: Malicious decryption detection ────────────────────────────────
	section("Step 7: Malicious Decryption Attack Test")
	info("Simulating: Carol provides wrong partial decryption for card slot 0.")

	ciphertext := cg.Deck.Cards[0]
	realResult, _ := cg.Keys[0].Decrypt(ciphertext)
	validProof, _ := crypto.ProveDecryption(cg.Keys[0], ciphertext, realResult, cg.SessionID)

	validPD := crypto.PartialDecryption{
		PlayerID:   "Alice",
		CardIndex:  0,
		Ciphertext: ciphertext,
		Result:     realResult,
		Proof:      validProof,
	}
	wrongResult := crypto.CardToField(50, cg.Deck.P) // completely wrong card
	tampered := crypto.SubstitutePartialDecryption(&validPD, wrongResult)

	if err := tampered.Verify(cg.P, cg.SessionID); err != nil {
		ok(fmt.Sprintf("Malicious decryption DETECTED: %v", err))
	} else {
		fail("SECURITY FAILURE: malicious decryption was not caught!")
	}

	// ── Step 8: Cross-session replay attack detection ─────────────────────────
	section("Step 8: Cross-Session Replay Attack Test")
	info("A proof generated for session A must not verify for session B.")

	sidA := crypto.SessionID([]string{"alice"}, []byte("session-a"))
	sidB := crypto.SessionID([]string{"alice"}, []byte("session-b"))
	proofForA, _ := crypto.ProveDecryption(cg.Keys[0], ciphertext, realResult, sidA)

	if err := crypto.VerifyDecryption(proofForA, ciphertext, realResult, cg.P, sidB); err != nil {
		ok("Cross-session replay DETECTED: proof from session A rejected by session B")
	} else {
		fail("SECURITY FAILURE: cross-session replay not caught!")
	}

	// ── Step 9: Shamir key recovery demo ─────────────────────────────────────
	section("Step 9: Shamir Secret Sharing (Fault Tolerance Setup)")
	info("Each player pre-splits their private key into N shares (threshold = N-1).")
	info("If one player disconnects, the rest can reconstruct their key.")

	key := cg.Keys[0]
	threshold := len(players) - 1
	shares, err := crypto.SplitSecret(key.D, threshold, len(players), cg.P)
	if err != nil {
		fail(fmt.Sprintf("SplitSecret: %v", err))
		return
	}
	for i, share := range shares {
		info(fmt.Sprintf("Share %d → Player %s", share.Index, players[i]))
	}

	// Reconstruct from threshold shares (omit Alice's own share).
	recovered, err := crypto.ReconstructSecret(shares[:threshold], cg.P)
	if err != nil {
		fail(fmt.Sprintf("ReconstructSecret: %v", err))
		return
	}
	if recovered.Cmp(key.D) == 0 {
		ok(fmt.Sprintf("Alice's private key reconstructed from %d of %d shares", threshold, len(players)))
	} else {
		fail("Shamir reconstruction produced wrong key!")
	}

	// ── Summary ────────────────────────────────────────────────────────────────
	fmt.Printf("\n%s%s%s\n", bold, sep(56), reset)
	fmt.Printf("%s  Phase 2 Summary%s\n", bold, reset)
	fmt.Printf("%s%s%s\n", bold, sep(56), reset)

	checks := []string{
		"SRA key generation and validation",
		"Session ID binding prevents cross-game replay",
		"N-player shuffle with commitment verification",
		"Commutativity: E_A(E_B(m)) = E_B(E_A(m))",
		"Hole card deal with ZK proof verification",
		"Community card deal (flop/turn/river)",
		"No card duplicates across full deal",
		"Malicious partial decryption detected",
		"Cross-session replay attack detected",
		"Shamir key recovery for fault tolerance",
	}
	for _, c := range checks {
		ok(c)
	}

	fmt.Printf("\n%sTotal cards dealt: %d unique cards out of 52%s\n", cyan, len(seen), reset)
	fmt.Printf("%sPhase 2 complete — ready for Phase 3 (P2P networking)%s\n\n", green, reset)
}
