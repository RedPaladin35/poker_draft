package crypto

import (
	"fmt"
	"math/big"

	"github.com/p2p-poker/internal/game"
)

// DealProtocol manages the secure card reveal process.
//
// After the shuffle, every card slot in the EncryptedDeck is a ciphertext
// encrypted under ALL N players' keys (in the order they shuffled).
//
// To reveal card slot i to player j (the recipient):
//  1. Every OTHER player k (k ≠ j) computes:
//       partial_k = ciphertext_i ^ d_k  mod P
//     and broadcasts (partial_k, ZKProof_k).
//  2. The recipient j verifies every ZKProof.
//  3. The recipient j combines the partial decryptions:
//       combined = ciphertext_i * ∏ partial_k  ... actually the recipient
//       applies the partial decryptions one by one:
//         step = partial_1
//         step = step ^ d_2  (player 2's partial)
//         ...
//       OR more precisely: the final value fed to j is:
//         intermediate = product of all partial decryptions from others
//         BUT in SRA the scheme works by sequential decryption, not product.
//
// Correct SRA reveal sequence for card slot i destined for player j:
//   - Players NOT in the recipient set apply their decryption in any order
//     (commutativity allows this) and pass the result to the next.
//   - The recipient applies their layer last, revealing the plaintext.
//
// For community cards (revealed to everyone):
//   - ALL players contribute a partial decryption.
//   - Anyone can combine them.
//
// This file implements both paths.

// DealProtocol coordinates card reveals for one hand.
type DealProtocol struct {
	Deck      *EncryptedDeck
	Players   []string // player IDs in shuffle order
	Keys      []*SRAKey
	SessionID []byte
}

// NewDealProtocol creates a deal coordinator.
func NewDealProtocol(deck *EncryptedDeck, players []string, keys []*SRAKey) *DealProtocol {
	return &DealProtocol{
		Deck:      deck,
		Players:   players,
		Keys:      keys,
		SessionID: deck.SessionID,
	}
}

// RevealToPlayer reveals card at deckIndex to the player at recipientIdx.
// All OTHER players produce a partial decryption + ZK proof.
// The recipient then decrypts the result with their own key.
//
// Returns the revealed game.Card and the full set of partial decryptions
// (for the game log — any peer can re-verify these later).
func (dp *DealProtocol) RevealToPlayer(deckIndex, recipientIdx int) (game.Card, []PartialDecryption, error) {
	ciphertext, err := dp.Deck.CardAt(deckIndex)
	if err != nil {
		return game.Card{}, nil, err
	}

	// Step 1: Every non-recipient player decrypts their layer.
	current := new(big.Int).Set(ciphertext)
	var partials []PartialDecryption

	for i, pid := range dp.Players {
		if i == recipientIdx {
			continue // recipient goes last
		}
		partial, err := dp.applyPartialDecryption(pid, i, deckIndex, current)
		if err != nil {
			return game.Card{}, nil, err
		}
		// Verify the ZK proof immediately (in production this comes over the network).
		if err := partial.Verify(dp.Deck.P, dp.SessionID); err != nil {
			return game.Card{}, nil, fmt.Errorf("RevealToPlayer: invalid proof from %s: %w", pid, err)
		}
		partials = append(partials, *partial)
		current = partial.Result // pass to next player
	}

	// Step 2: Recipient decrypts the final layer (no ZK proof needed — they
	// decrypt privately and the result is their hole card).
	recipientKey := dp.Keys[recipientIdx]
	plaintext, err := recipientKey.Decrypt(current)
	if err != nil {
		return game.Card{}, nil, fmt.Errorf("RevealToPlayer: recipient decrypt: %w", err)
	}

	cardID := FieldToCard(plaintext, dp.Deck.P)
	if cardID == -1 {
		return game.Card{}, nil, fmt.Errorf("RevealToPlayer: plaintext %s does not map to a card", plaintext)
	}

	return game.CardFromID(cardID), partials, nil
}

// RevealCommunity reveals a community card (visible to all players).
// All N players contribute a partial decryption.
// Any player can call this — the result is deterministic.
func (dp *DealProtocol) RevealCommunity(deckIndex int) (game.Card, []PartialDecryption, error) {
	ciphertext, err := dp.Deck.CardAt(deckIndex)
	if err != nil {
		return game.Card{}, nil, err
	}

	current := new(big.Int).Set(ciphertext)
	var partials []PartialDecryption

	for i, pid := range dp.Players {
		partial, err := dp.applyPartialDecryption(pid, i, deckIndex, current)
		if err != nil {
			return game.Card{}, nil, err
		}
		if err := partial.Verify(dp.Deck.P, dp.SessionID); err != nil {
			return game.Card{}, nil, fmt.Errorf("RevealCommunity: invalid proof from %s: %w", pid, err)
		}
		partials = append(partials, *partial)
		current = partial.Result
	}

	cardID := FieldToCard(current, dp.Deck.P)
	if cardID == -1 {
		return game.Card{}, nil, fmt.Errorf("RevealCommunity: plaintext %s does not map to a card", current)
	}

	return game.CardFromID(cardID), partials, nil
}

// applyPartialDecryption generates one player's partial decryption with proof.
func (dp *DealProtocol) applyPartialDecryption(playerID string, keyIdx, cardIdx int, input *big.Int) (*PartialDecryption, error) {
	key := dp.Keys[keyIdx]
	result, err := key.Decrypt(input)
	if err != nil {
		return nil, fmt.Errorf("applyPartialDecryption player %s: %w", playerID, err)
	}

	proof, err := ProveDecryption(key, input, result, dp.SessionID)
	if err != nil {
		return nil, fmt.Errorf("applyPartialDecryption player %s: prove: %w", playerID, err)
	}

	return &PartialDecryption{
		PlayerID:   playerID,
		CardIndex:  cardIdx,
		Ciphertext: input,
		Result:     result,
		Proof:      proof,
	}, nil
}

// DealHoleCards deals 2 hole cards to each player using the encrypted deck.
// Deck positions 0..(2N-1) are used for hole cards (dealt in two rounds,
// matching standard poker dealing order: one card each, then a second).
// Returns hole cards indexed by player index.
func (dp *DealProtocol) DealHoleCards(dealerIdx int) ([][2]game.Card, error) {
	n := len(dp.Players)
	result := make([][2]game.Card, n)

	deckPos := 0
	for round := 0; round < 2; round++ {
		for i := 0; i < n; i++ {
			playerIdx := (dealerIdx + 1 + i) % n
			card, _, err := dp.RevealToPlayer(deckPos, playerIdx)
			if err != nil {
				return nil, fmt.Errorf("DealHoleCards round %d player %d: %w", round, playerIdx, err)
			}
			result[playerIdx][round] = card
			deckPos++
		}
	}
	return result, nil
}

// DealCommunityCards reveals community cards from the deck.
// burnAndReveal is a slice describing the deal pattern:
//   - Each entry is the number of cards to reveal in that batch
//     (e.g., [3, 1, 1] for flop/turn/river).
//
// startPos is the deck index to begin from (after hole cards).
// Returns batches of community cards and their partial decryption logs.
func (dp *DealProtocol) DealCommunityCards(startPos int, batches []int) ([][]game.Card, error) {
	pos := startPos
	result := make([][]game.Card, len(batches))

	for batch, count := range batches {
		// Burn one card per batch (standard poker protocol).
		pos++ // burn
		cards := make([]game.Card, count)
		for j := 0; j < count; j++ {
			card, _, err := dp.RevealCommunity(pos)
			if err != nil {
				return nil, fmt.Errorf("DealCommunityCards batch %d card %d: %w", batch, j, err)
			}
			cards[j] = card
			pos++
		}
		result[batch] = cards
	}
	return result, nil
}

// VerifyAllProofs re-verifies every partial decryption in a set of records.
// Used by any player to audit the full game log after the hand completes.
func VerifyAllProofs(partials []PartialDecryption, P *big.Int, sessionID []byte) error {
	for i, pd := range partials {
		if err := pd.Verify(P, sessionID); err != nil {
			return fmt.Errorf("VerifyAllProofs[%d]: %w", i, err)
		}
	}
	return nil
}

// SubstitutePartialDecryption simulates a malicious player providing a wrong
// decryption result.  Used ONLY in tests to verify that proof verification
// catches the attack.
func SubstitutePartialDecryption(pd *PartialDecryption, wrongResult *big.Int) *PartialDecryption {
	tampered := *pd
	tampered.Result = wrongResult
	return &tampered
}
