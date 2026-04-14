package fault

import (
	"fmt"
	"math/big"
	"sync"

	pokercrypto "github.com/p2p-poker/internal/crypto"
)

// KeyShareStore manages the Shamir shares that each player holds for others.
//
// Protocol at hand start (pre-shuffle):
//  1. Each player i splits their SRA decryption key D_i into N shares
//     (one for each other player) with threshold T = ceil(N/2).
//  2. Player i sends share j to player j over the direct /poker/1.0.0 stream
//     (private — only the recipient sees their share).
//  3. If player i disconnects during the deal, any T of the remaining players
//     can contribute their share to reconstruct D_i.
//  4. The reconstructed D_i is used to complete i's partial decryption,
//     allowing the hand to continue without them.
//
// Security note: Each share is worthless alone — T-1 colluding peers cannot
// reconstruct D_i.  T defaults to ceil(N/2), so a majority must collude to
// expose a player's private key.
type KeyShareStore struct {
	mu sync.RWMutex
	// sharesReceived[ownerID][myShareIndex] = share value
	// These are the shares *this node* received from others.
	sharesReceived map[string]pokercrypto.ShamirShare

	// sharesHeld[ownerID] = all shares this node has collected for reconstruction.
	// Populated from sharesReceived when reconstruction is needed.
	sharesHeld map[string][]pokercrypto.ShamirShare

	prime *big.Int
}

// NewKeyShareStore creates a store for the given field prime.
func NewKeyShareStore(prime *big.Int) *KeyShareStore {
	return &KeyShareStore{
		prime:          prime,
		sharesReceived: make(map[string]pokercrypto.ShamirShare),
		sharesHeld:     make(map[string][]pokercrypto.ShamirShare),
	}
}

// StoreMyShare records the share that ownerID gave to this node.
// Called when a private share message arrives over the direct stream.
func (ks *KeyShareStore) StoreMyShare(ownerID string, share pokercrypto.ShamirShare) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.sharesReceived[ownerID] = share
}

// ContributeShare adds this node's share for ownerID to the reconstruction pool.
// Call this when a peer broadcasts "I need to reconstruct ownerID's key".
func (ks *KeyShareStore) ContributeShare(ownerID string) (pokercrypto.ShamirShare, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	share, ok := ks.sharesReceived[ownerID]
	return share, ok
}

// AddReconstructionShare adds a share contributed by another peer for ownerID.
// Accumulates until we have enough to reconstruct.
func (ks *KeyShareStore) AddReconstructionShare(ownerID string, share pokercrypto.ShamirShare) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Deduplicate by share index.
	for _, existing := range ks.sharesHeld[ownerID] {
		if existing.Index == share.Index {
			return
		}
	}
	ks.sharesHeld[ownerID] = append(ks.sharesHeld[ownerID], share)
}

// CanReconstruct returns true if we have at least threshold shares for ownerID.
func (ks *KeyShareStore) CanReconstruct(ownerID string, threshold int) bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return len(ks.sharesHeld[ownerID]) >= threshold
}

// Reconstruct recovers the private key for ownerID using collected shares.
// Returns an error if fewer than threshold shares are available.
func (ks *KeyShareStore) Reconstruct(ownerID string, threshold int) (*big.Int, error) {
	ks.mu.RLock()
	shares := make([]pokercrypto.ShamirShare, len(ks.sharesHeld[ownerID]))
	copy(shares, ks.sharesHeld[ownerID])
	ks.mu.RUnlock()

	if len(shares) < threshold {
		return nil, fmt.Errorf("Reconstruct: need %d shares for %s, have %d",
			threshold, ownerID, len(shares))
	}

	// Use exactly threshold shares (the first T we collected).
	key, err := pokercrypto.ReconstructSecret(shares[:threshold], ks.prime)
	if err != nil {
		return nil, fmt.Errorf("Reconstruct %s: %w", ownerID, err)
	}
	return key, nil
}

// ReconstructSRAKey builds a full SRAKey from the reconstructed private exponent.
func (ks *KeyShareStore) ReconstructSRAKey(ownerID string, threshold int) (*pokercrypto.SRAKey, error) {
	d, err := ks.Reconstruct(ownerID, threshold)
	if err != nil {
		return nil, err
	}
	// Compute e = d^{-1} mod (P-1) to rebuild the full key pair.
	phi := new(big.Int).Sub(ks.prime, big.NewInt(1))
	e := new(big.Int).ModInverse(d, phi)
	if e == nil {
		return nil, fmt.Errorf("ReconstructSRAKey: ModInverse failed for %s", ownerID)
	}
	return &pokercrypto.SRAKey{E: e, D: d, P: ks.prime}, nil
}

// SplitAndDistribute splits ownerKey into N shares and returns them indexed
// by recipient seat index (0-based).  The share for seat i is shares[i].
// Threshold is set to ceil(N/2) which requires a majority to reconstruct.
func SplitAndDistribute(ownerKey *pokercrypto.SRAKey, numPlayers int) ([]pokercrypto.ShamirShare, int, error) {
	if numPlayers < 2 {
		return nil, 0, fmt.Errorf("SplitAndDistribute: need at least 2 players")
	}
	threshold := (numPlayers + 1) / 2 // ceil(N/2)
	if threshold < 2 {
		threshold = 2
	}

	shares, err := pokercrypto.SplitSecret(ownerKey.D, threshold, numPlayers, ownerKey.P)
	if err != nil {
		return nil, 0, fmt.Errorf("SplitAndDistribute: %w", err)
	}
	return shares, threshold, nil
}
