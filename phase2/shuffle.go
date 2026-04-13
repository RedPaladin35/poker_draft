package crypto

import (
	"crypto/rand"
	"fmt"
	"math/big"
	mathrand "math/rand"
)

// ShuffleStep represents one player's contribution to the deck shuffle:
// they receive the deck from the previous player, encrypt every card with
// their SRA key, permute the order, and pass the result on.
//
// The commitment to the output deck is broadcast BEFORE the shuffle step
// is performed, locking in the permutation (see commit.go).  After all
// players have shuffled, they reveal their commitments in order so any
// player can verify no substitution occurred.
type ShuffleStep struct {
	PlayerID    string
	InputDeck   []*big.Int // deck received (nil for the first player)
	OutputDeck  []*big.Int // encrypted + permuted deck to pass on
	Permutation []int      // the permutation applied (indices into InputDeck)
	Commitment  *Commitment // H(OutputDeck || nonce) — broadcast before reveal
}

// ShuffleProtocol coordinates the full N-player shuffle sequence.
// In the real P2P implementation (Phase 3) each step is executed by the
// corresponding player on their own machine and transmitted over the network.
// Here it runs in-process, simulating the protocol with goroutine-per-player.
type ShuffleProtocol struct {
	P         *big.Int
	SessionID []byte
	NumCards  int
}

// NewShuffleProtocol creates a coordinator for the given prime and session.
func NewShuffleProtocol(p *big.Int, sessionID []byte) *ShuffleProtocol {
	return &ShuffleProtocol{P: p, SessionID: sessionID, NumCards: 52}
}

// ExecuteStep performs one player's shuffle step.
// It encrypts each card in deck with key, then applies a cryptographically
// random Fisher-Yates permutation.
// Returns the ShuffleStep (including the commitment) ready to be broadcast.
func (sp *ShuffleProtocol) ExecuteStep(playerID string, deck []*big.Int, key *SRAKey) (*ShuffleStep, error) {
	if len(deck) != sp.NumCards {
		return nil, fmt.Errorf("ExecuteStep %s: expected %d cards, got %d", playerID, sp.NumCards, len(deck))
	}

	// 1. Encrypt every card with this player's key.
	encrypted, err := key.EncryptAll(deck)
	if err != nil {
		return nil, fmt.Errorf("ExecuteStep %s: encrypt: %w", playerID, err)
	}

	// 2. Apply a cryptographically random permutation.
	perm, err := randomPermutation(sp.NumCards)
	if err != nil {
		return nil, fmt.Errorf("ExecuteStep %s: permutation: %w", playerID, err)
	}
	permuted := make([]*big.Int, sp.NumCards)
	for i, srcIdx := range perm {
		permuted[i] = encrypted[srcIdx]
	}

	// 3. Commit to the output deck before broadcasting it.
	commitment, err := NewDeckCommitment(permuted)
	if err != nil {
		return nil, fmt.Errorf("ExecuteStep %s: commit: %w", playerID, err)
	}

	return &ShuffleStep{
		PlayerID:    playerID,
		InputDeck:   deck,
		OutputDeck:  permuted,
		Permutation: perm,
		Commitment:  commitment,
	}, nil
}

// VerifyStep checks that a step's OutputDeck matches its commitment.
// This is called after all players have revealed their commitments.
func (sp *ShuffleProtocol) VerifyStep(step *ShuffleStep) error {
	if err := step.Commitment.VerifyDeck(step.OutputDeck); err != nil {
		return fmt.Errorf("VerifyStep player %s: %w", step.PlayerID, err)
	}
	return nil
}

// RunFullShuffle runs the complete shuffle protocol for a list of (playerID, key) pairs.
// Returns the final encrypted deck (encrypted under every player's key in sequence)
// and the full shuffle log for verification.
//
// This is the in-process simulation used in tests.  In Phase 3 each
// ExecuteStep call becomes a network round-trip.
func (sp *ShuffleProtocol) RunFullShuffle(
	players []string,
	keys []*SRAKey,
	initialDeck []*big.Int,
) ([]*big.Int, []*ShuffleStep, error) {
	if len(players) != len(keys) {
		return nil, nil, fmt.Errorf("RunFullShuffle: %d players but %d keys", len(players), len(keys))
	}

	steps := make([]*ShuffleStep, len(players))
	current := initialDeck

	for i, pid := range players {
		step, err := sp.ExecuteStep(pid, current, keys[i])
		if err != nil {
			return nil, nil, err
		}

		// Simulate the "verify commitment after reveal" check.
		if err := sp.VerifyStep(step); err != nil {
			return nil, nil, err
		}

		steps[i] = step
		current = step.OutputDeck
	}

	return current, steps, nil
}

// randomPermutation generates a cryptographically random permutation of [0, n).
// Uses crypto/rand to seed a Fisher-Yates shuffle.
func randomPermutation(n int) ([]int, error) {
	// Get 8 bytes of randomness for the seed.
	seedBytes := make([]byte, 8)
	if _, err := rand.Read(seedBytes); err != nil {
		return nil, err
	}
	var seed int64
	for _, b := range seedBytes {
		seed = seed<<8 | int64(b)
	}

	rng := mathrand.New(mathrand.NewSource(seed))
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	rng.Shuffle(n, func(i, j int) {
		perm[i], perm[j] = perm[j], perm[i]
	})
	return perm, nil
}

// EncryptedDeck is the final output of the shuffle protocol —
// 52 slots, each a ciphertext encrypted under every player's key.
// The order is unknown to all players because each player applied a
// private permutation.
type EncryptedDeck struct {
	Cards     []*big.Int // 52 ciphertexts
	P         *big.Int   // shared prime (for decryption)
	SessionID []byte     // game session binding
}

// NewEncryptedDeck wraps the final shuffle output.
func NewEncryptedDeck(cards []*big.Int, p *big.Int, sessionID []byte) (*EncryptedDeck, error) {
	if len(cards) != 52 {
		return nil, fmt.Errorf("NewEncryptedDeck: expected 52 cards, got %d", len(cards))
	}
	c := make([]*big.Int, 52)
	for i, v := range cards {
		c[i] = new(big.Int).Set(v)
	}
	sid := make([]byte, len(sessionID))
	copy(sid, sessionID)
	return &EncryptedDeck{Cards: c, P: p, SessionID: sid}, nil
}

// CardAt returns a copy of the ciphertext at the given deck position.
func (ed *EncryptedDeck) CardAt(index int) (*big.Int, error) {
	if index < 0 || index >= len(ed.Cards) {
		return nil, fmt.Errorf("CardAt: index %d out of range", index)
	}
	return new(big.Int).Set(ed.Cards[index]), nil
}
