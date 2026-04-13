// Package crypto implements the Mental Poker cryptographic protocol.
//
// Overview of the full protocol (SRA — Shamir, Rivest, Adleman 1981):
//
//  1. All players agree on a large safe prime P and generator G (params.go).
//     These are public, fixed per table, and committed to via a session hash.
//
//  2. Each player i generates a private SRA key pair (e_i, d_i) such that:
//       e_i * d_i ≡ 1  (mod  φ(P))   [Euler's totient of P]
//     Encryption:   E_i(x) = x^e_i  mod P
//     Decryption:   D_i(x) = x^d_i  mod P
//
//  3. Commutativity:  E_A(E_B(x)) = E_B(E_A(x))  (mod P)
//     This is the core property that makes the shuffle protocol work.
//
//  4. Deck shuffle (see shuffle.go):
//     Each player encrypts-and-permutes the full deck in turn.
//     The result is a deck where every "card" is a ciphertext encrypted
//     under every player's key in an unknown order.
//
//  5. Card deal (see deal.go):
//     To reveal card slot i to player j, every OTHER player k publishes
//     D_k(card_i) along with a ZK proof that they decrypted correctly.
//     Player j then applies D_j to the result to recover the plaintext.
//
// Security note:
//   The prime P must be a safe prime (P = 2q+1 where q is also prime)
//   so that the multiplicative group Z_P* is of large prime order q,
//   making discrete-log attacks hard.  We use a 2048-bit safe prime.
package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
)

// SRAKey holds a player's private SRA key pair for one session.
// e and d are chosen so that e*d ≡ 1 (mod φ(P)) = 1 (mod P-1)
// because P is prime → φ(P) = P-1.
type SRAKey struct {
	E *big.Int // encryption exponent (public to self, private to others)
	D *big.Int // decryption exponent (must never leave this node)
	P *big.Int // shared prime modulus (same for every player at the table)
}

// GenerateSRAKey generates a fresh SRA key pair for the given prime P.
// It picks a random e that is coprime with P-1, then computes d = e^-1 mod (P-1).
func GenerateSRAKey(p *big.Int) (*SRAKey, error) {
	if p == nil || !p.ProbablyPrime(20) {
		return nil, errors.New("GenerateSRAKey: p must be a prime")
	}

	// φ(P) = P-1 for a prime P.
	phi := new(big.Int).Sub(p, big.NewInt(1))

	// Pick random e in [2, φ(P)-1] that is coprime with φ(P).
	one := big.NewInt(1)
	var e *big.Int
	for {
		candidate, err := rand.Int(rand.Reader, phi)
		if err != nil {
			return nil, fmt.Errorf("GenerateSRAKey: rand: %w", err)
		}
		// Ensure e >= 2.
		candidate.Add(candidate, big.NewInt(2))
		if candidate.Cmp(phi) >= 0 {
			continue
		}
		gcd := new(big.Int).GCD(nil, nil, candidate, phi)
		if gcd.Cmp(one) == 0 {
			e = candidate
			break
		}
	}

	// d = e^{-1} mod φ(P).
	d := new(big.Int).ModInverse(e, phi)
	if d == nil {
		return nil, errors.New("GenerateSRAKey: modular inverse does not exist")
	}

	return &SRAKey{E: e, D: d, P: p}, nil
}

// Encrypt computes c = m^e mod P.
// m must be in [1, P-1]; returns an error if out of range.
func (k *SRAKey) Encrypt(m *big.Int) (*big.Int, error) {
	if err := k.validateMessage(m); err != nil {
		return nil, fmt.Errorf("Encrypt: %w", err)
	}
	return new(big.Int).Exp(m, k.E, k.P), nil
}

// Decrypt computes m = c^d mod P.
func (k *SRAKey) Decrypt(c *big.Int) (*big.Int, error) {
	if err := k.validateMessage(c); err != nil {
		return nil, fmt.Errorf("Decrypt: %w", err)
	}
	return new(big.Int).Exp(c, k.D, k.P), nil
}

// validateMessage ensures m ∈ [1, P-1].
func (k *SRAKey) validateMessage(m *big.Int) error {
	if m == nil {
		return errors.New("message is nil")
	}
	one := big.NewInt(1)
	pMinus1 := new(big.Int).Sub(k.P, one)
	if m.Cmp(one) < 0 || m.Cmp(pMinus1) > 0 {
		return fmt.Errorf("message %s out of range [1, P-1]", m)
	}
	return nil
}

// EncryptAll encrypts a slice of card values, returning a new slice.
func (k *SRAKey) EncryptAll(cards []*big.Int) ([]*big.Int, error) {
	out := make([]*big.Int, len(cards))
	for i, c := range cards {
		enc, err := k.Encrypt(c)
		if err != nil {
			return nil, fmt.Errorf("EncryptAll[%d]: %w", i, err)
		}
		out[i] = enc
	}
	return out, nil
}

// DecryptAll decrypts a slice of card ciphertexts, returning a new slice.
func (k *SRAKey) DecryptAll(cards []*big.Int) ([]*big.Int, error) {
	out := make([]*big.Int, len(cards))
	for i, c := range cards {
		dec, err := k.Decrypt(c)
		if err != nil {
			return nil, fmt.Errorf("DecryptAll[%d]: %w", i, err)
		}
		out[i] = dec
	}
	return out, nil
}

// PublicKey returns the encryption exponent E as a public value.
// In the actual network protocol the ZK proof (zkp.go) lets peers
// verify correct decryption without seeing D.
func (k *SRAKey) PublicKey() *big.Int {
	return new(big.Int).Set(k.E)
}

// VerifyKeyPair sanity-checks that e*d ≡ 1 (mod P-1).
// Useful in tests and on key import.
func (k *SRAKey) VerifyKeyPair() bool {
	phi := new(big.Int).Sub(k.P, big.NewInt(1))
	product := new(big.Int).Mul(k.E, k.D)
	product.Mod(product, phi)
	return product.Cmp(big.NewInt(1)) == 0
}
