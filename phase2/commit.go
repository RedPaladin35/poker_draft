package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// Commitment implements a standard hash-based commitment scheme.
//
// A commitment allows a player to "lock in" a value without revealing it,
// then prove later that they are revealing the same value they committed to.
//
// Usage in the Mental Poker shuffle:
//   1. Before the shuffle begins, each player commits to their permutation
//      and encryption key via: commit = H(value || nonce).
//   2. The commitment is broadcast to all peers.
//   3. After the shuffle is complete, each player reveals (value, nonce).
//   4. All peers verify: H(value || nonce) == commit.
//
// This prevents a player from changing their shuffle after seeing others'.
// The nonce must be secret until reveal time to hide the committed value.

// Commitment holds the hash commitment and the nonce used to create it.
type Commitment struct {
	Hash  []byte // sha256(data || nonce)
	Nonce []byte // 32 random bytes, kept secret until reveal
}

// NewCommitment creates a commitment to arbitrary data.
// The nonce is generated internally using crypto/rand.
func NewCommitment(data []byte) (*Commitment, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("NewCommitment: %w", err)
	}
	hash := computeCommitmentHash(data, nonce)
	return &Commitment{Hash: hash, Nonce: nonce}, nil
}

// Verify checks that the committed hash matches H(data || nonce).
func (c *Commitment) Verify(data []byte) error {
	expected := computeCommitmentHash(data, c.Nonce)
	if len(expected) != len(c.Hash) {
		return errors.New("Verify: hash length mismatch")
	}
	// Constant-time comparison.
	var diff byte
	for i := range expected {
		diff |= expected[i] ^ c.Hash[i]
	}
	if diff != 0 {
		return errors.New("Verify: commitment mismatch — data does not match hash")
	}
	return nil
}

// HashHex returns the hex-encoded commitment hash (safe to broadcast).
func (c *Commitment) HashHex() string {
	return hex.EncodeToString(c.Hash)
}

func computeCommitmentHash(data, nonce []byte) []byte {
	h := sha256.New()
	// Length-prefix both fields to prevent extension attacks.
	lenData := make([]byte, 4)
	lenData[0] = byte(len(data) >> 24)
	lenData[1] = byte(len(data) >> 16)
	lenData[2] = byte(len(data) >> 8)
	lenData[3] = byte(len(data))
	h.Write(lenData)
	h.Write(data)
	h.Write(nonce)
	return h.Sum(nil)
}

// DeckCommitment commits to an encrypted deck (a slice of big.Ints).
// The deck is serialised as the concatenation of 256-byte big-endian values.
func NewDeckCommitment(deck []*big.Int) (*Commitment, error) {
	return NewCommitment(serialiseDeck(deck))
}

// VerifyDeck checks that the committed deck matches the revealed deck.
func (c *Commitment) VerifyDeck(deck []*big.Int) error {
	return c.Verify(serialiseDeck(deck))
}

// serialiseDeck encodes a deck of big.Ints as fixed-width 256-byte values
// concatenated together, giving a deterministic byte representation.
func serialiseDeck(deck []*big.Int) []byte {
	const fieldWidth = 256 // 2048-bit prime → 256 bytes per element
	out := make([]byte, len(deck)*fieldWidth)
	for i, v := range deck {
		b := v.Bytes()
		offset := i * fieldWidth
		// Right-align (big-endian, zero-padded to fieldWidth).
		copy(out[offset+fieldWidth-len(b):offset+fieldWidth], b)
	}
	return out
}

// ShamirShare holds one player's share of another player's secret (used in
// fault.go for key recovery when a player disconnects mid-hand).
// Defined here because it references crypto primitives.
type ShamirShare struct {
	Index int      // share index (1-based)
	Value *big.Int // the share value
}

// SplitSecret splits secret into n shares with threshold t using Shamir's
// Secret Sharing over Z_P*.  Any t shares can reconstruct the secret;
// fewer than t reveal nothing.
//
// This is used during hand setup: each player splits their SRA decryption
// key D and distributes one share to each other player.  If they disconnect
// during the deal, the remaining players can reconstruct D and complete the
// partial decryption on their behalf.
func SplitSecret(secret *big.Int, t, n int, p *big.Int) ([]ShamirShare, error) {
	if t < 2 {
		return nil, errors.New("SplitSecret: threshold must be >= 2")
	}
	if n < t {
		return nil, fmt.Errorf("SplitSecret: n=%d must be >= t=%d", n, t)
	}

	// Build a random degree-(t-1) polynomial f over Z_P with f(0) = secret.
	// Coefficients a[0]=secret, a[1..t-1] are random in [0, P-1].
	coeffs := make([]*big.Int, t)
	coeffs[0] = new(big.Int).Set(secret)
	for i := 1; i < t; i++ {
		r, err := rand.Int(rand.Reader, p)
		if err != nil {
			return nil, fmt.Errorf("SplitSecret: rand coeff[%d]: %w", i, err)
		}
		coeffs[i] = r
	}

	// Evaluate f at x = 1, 2, ..., n.
	shares := make([]ShamirShare, n)
	for x := 1; x <= n; x++ {
		xBig := big.NewInt(int64(x))
		y := new(big.Int).Set(coeffs[0])
		xPow := new(big.Int).Set(xBig)
		for i := 1; i < t; i++ {
			term := new(big.Int).Mul(coeffs[i], xPow)
			term.Mod(term, p)
			y.Add(y, term)
			y.Mod(y, p)
			xPow.Mul(xPow, xBig)
			xPow.Mod(xPow, p)
		}
		shares[x-1] = ShamirShare{Index: x, Value: y}
	}
	return shares, nil
}

// ReconstructSecret recovers the secret from exactly t (or more) shares using
// Lagrange interpolation over Z_P*.
func ReconstructSecret(shares []ShamirShare, p *big.Int) (*big.Int, error) {
	if len(shares) == 0 {
		return nil, errors.New("ReconstructSecret: no shares provided")
	}

	secret := big.NewInt(0)
	for i, si := range shares {
		xi := big.NewInt(int64(si.Index))
		// Lagrange basis polynomial evaluated at 0:
		// L_i(0) = ∏_{j≠i} (-x_j) / (x_i - x_j)  mod P
		num := big.NewInt(1)
		den := big.NewInt(1)
		for j, sj := range shares {
			if i == j {
				continue
			}
			xj := big.NewInt(int64(sj.Index))
			// num *= -xj  =  P - xj  (in Z_P)
			negXj := new(big.Int).Sub(p, xj)
			num.Mul(num, negXj)
			num.Mod(num, p)
			// den *= (xi - xj)  mod P
			diff := new(big.Int).Sub(xi, xj)
			diff.Mod(diff, p)
			den.Mul(den, diff)
			den.Mod(den, p)
		}
		// lagrange = si.Value * num * den^{-1} mod P
		denInv := new(big.Int).ModInverse(den, p)
		if denInv == nil {
			return nil, fmt.Errorf("ReconstructSecret: modular inverse failed for share %d", i)
		}
		lagrange := new(big.Int).Mul(si.Value, num)
		lagrange.Mod(lagrange, p)
		lagrange.Mul(lagrange, denInv)
		lagrange.Mod(lagrange, p)

		secret.Add(secret, lagrange)
		secret.Mod(secret, p)
	}
	return secret, nil
}
