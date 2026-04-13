package crypto

import (
	"fmt"
	"math/big"

	"github.com/p2p-poker/internal/game"
)

// CryptoGame bridges the Mental Poker cryptographic protocol with the
// Phase 1 game engine.  It replaces the local random Deck.Shuffle call
// with the full N-player SRA shuffle + ZK-verified deal.
//
// Lifecycle per hand:
//  1. NewCryptoGame — each player generates their SRA key.
//  2. RunShuffle    — executes the N-player shuffle protocol,
//                    producing a shared EncryptedDeck.
//  3. DealToEngine  — runs the deal protocol and populates the
//                    game.GameState with real hole cards and community cards,
//                    replacing the plaintext deck used in Phase 1.
//
// In Phase 3 (networking) the RunShuffle steps will be replaced by
// network round-trips; the interface here stays identical.

// CryptoGame holds all per-hand cryptographic state.
type CryptoGame struct {
	P         *big.Int
	SessionID []byte
	Players   []string
	Keys      []*SRAKey
	Deck      *EncryptedDeck
	ShuffleLog []*ShuffleStep
	DealLog   []PartialDecryption
}

// NewCryptoGame initialises a CryptoGame for a list of player IDs.
// It generates one SRA key per player and computes the session ID.
//
// In Phase 3, each player generates their own key locally; they share
// only E (the encryption exponent) and the ZK proofs — D never leaves
// the owning node.
func NewCryptoGame(playerIDs []string, nonce []byte) (*CryptoGame, error) {
	if len(playerIDs) < 2 {
		return nil, fmt.Errorf("NewCryptoGame: need at least 2 players")
	}

	p := SharedPrime()
	sid := SessionID(playerIDs, nonce)

	keys := make([]*SRAKey, len(playerIDs))
	for i, pid := range playerIDs {
		k, err := GenerateSRAKey(p)
		if err != nil {
			return nil, fmt.Errorf("NewCryptoGame: key gen for %s: %w", pid, err)
		}
		keys[i] = k
	}

	return &CryptoGame{
		P:         p,
		SessionID: sid,
		Players:   playerIDs,
		Keys:      keys,
	}, nil
}

// RunShuffle executes the full N-player shuffle and stores the resulting
// EncryptedDeck.  Must be called before DealToEngine.
func (cg *CryptoGame) RunShuffle() error {
	sp := NewShuffleProtocol(cg.P, cg.SessionID)
	initialDeck := BuildPlaintextDeck(cg.P)

	finalDeck, steps, err := sp.RunFullShuffle(cg.Players, cg.Keys, initialDeck)
	if err != nil {
		return fmt.Errorf("RunShuffle: %w", err)
	}

	cg.ShuffleLog = steps
	cg.Deck, err = NewEncryptedDeck(finalDeck, cg.P, cg.SessionID)
	if err != nil {
		return fmt.Errorf("RunShuffle: wrap deck: %w", err)
	}
	return nil
}

// DealToEngine runs the deal protocol and writes hole cards and community cards
// directly into the provided GameState, replacing the Phase 1 random deck.
//
// It mirrors the exact dealing order of machine.go:
//   - 2 hole cards to each player (dealt in two rounds, left of dealer first).
//   - Community cards are NOT dealt here — they are dealt on demand when
//     each betting round ends (DealFlop / DealTurn / DealRiver).
//
// After this call, gs.Deck is set to nil because the plaintext deck is no
// longer used — all card reveals go through the DealProtocol.
func (cg *CryptoGame) DealToEngine(gs *game.GameState) error {
	if cg.Deck == nil {
		return fmt.Errorf("DealToEngine: RunShuffle must be called first")
	}

	dp := NewDealProtocol(cg.Deck, cg.Players, cg.Keys)

	// Deal hole cards: positions 0 .. 2N-1.
	holeCards, err := dp.DealHoleCards(gs.DealerIdx)
	if err != nil {
		return fmt.Errorf("DealToEngine: hole cards: %w", err)
	}

	// Write hole cards into the game state.
	// The player order in cg.Players must match gs.Players.
	for _, p := range gs.Players {
		pidIdx := cg.playerIndex(p.ID)
		if pidIdx == -1 {
			return fmt.Errorf("DealToEngine: player %s not found in crypto session", p.ID)
		}
		p.HoleCards = holeCards[pidIdx]
	}

	// Null out the plaintext deck — it is no longer used.
	gs.Deck = nil

	return nil
}

// DealFlop reveals the 3 flop community cards from the encrypted deck.
// Call this after the pre-flop betting round ends (mirrors machine.go dealFlop).
// startPos is the first unused deck position (= 2*numPlayers after hole cards).
func (cg *CryptoGame) DealFlop(startPos int) ([]game.Card, error) {
	return cg.dealBatch(startPos, 3)
}

// DealTurn reveals the single turn card.
func (cg *CryptoGame) DealTurn(startPos int) ([]game.Card, error) {
	return cg.dealBatch(startPos, 1)
}

// DealRiver reveals the single river card.
func (cg *CryptoGame) DealRiver(startPos int) ([]game.Card, error) {
	return cg.dealBatch(startPos, 1)
}

// dealBatch reveals count community cards starting at startPos,
// burning one card before each batch (standard poker dealing).
func (cg *CryptoGame) dealBatch(startPos, count int) ([]game.Card, error) {
	dp := NewDealProtocol(cg.Deck, cg.Players, cg.Keys)
	pos := startPos + 1 // +1 for the burn card

	cards := make([]game.Card, count)
	for i := 0; i < count; i++ {
		card, partials, err := dp.RevealCommunity(pos)
		if err != nil {
			return nil, fmt.Errorf("dealBatch pos %d: %w", pos, err)
		}
		cg.DealLog = append(cg.DealLog, partials...)
		cards[i] = card
		pos++
	}
	return cards, nil
}

// playerIndex returns the index of playerID in cg.Players, or -1.
func (cg *CryptoGame) playerIndex(playerID string) int {
	for i, pid := range cg.Players {
		if pid == playerID {
			return i
		}
	}
	return -1
}

// HolecardStartPos returns the first deck position after all hole cards.
// This is the position community card dealing starts from.
func (cg *CryptoGame) HolecardStartPos() int {
	return len(cg.Players) * 2
}

// VerifyFullLog re-verifies every ZK proof in the deal log.
// Any player can call this after the hand to audit correctness.
func (cg *CryptoGame) VerifyFullLog() error {
	return VerifyAllProofs(cg.DealLog, cg.P, cg.SessionID)
}
