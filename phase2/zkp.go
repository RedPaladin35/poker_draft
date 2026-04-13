package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// ZKProof is a non-interactive Chaum-Pedersen proof of discrete log equality.
//
// Goal: Player P has private key d and wants to prove to others that:
//   result = ciphertext^d mod P
// without revealing d.
//
// This is a proof of knowledge of d such that:
//   result = ciphertext^d mod P      (the actual decryption)
//   h      = G^d mod P               (a published "key commitment")
//
// Both equalities use the same exponent d, which is what we prove.
//
// Protocol (Fiat-Shamir heuristic makes it non-interactive):
//  1. Prover picks random r, computes:
//       A = G^r mod P          (commitment on G)
//       B = ciphertext^r mod P (commitment on ciphertext)
//  2. Challenge c = H(G, h, ciphertext, result, A, B, sessionID)
//  3. Response s = r + c*d mod (P-1)   [exponent arithmetic mod φ(P)]
//  4. Verifier checks:
//       G^s          ≡ A * h^c       mod P
//       ciphertext^s ≡ B * result^c  mod P
//
// The sessionID binding (step 2) prevents replay across game sessions.
type ZKProof struct {
	// A = G^r mod P
	A *big.Int
	// B = ciphertext^r mod P
	B *big.Int
	// S = r + c*d mod (P-1)
	S *big.Int
	// H = G^d mod P  (public "decryption key commitment")
	H *big.Int
}

var g = big.NewInt(2) // generator, same as in params.go

// ProveDecryption generates a ZK proof that:
//
//	result = ciphertext^d mod P
//
// Parameters:
//   - key:        the prover's SRA key (d is the private decryption exponent)
//   - ciphertext: the value being decrypted
//   - result:     the claimed decryption output (= ciphertext^d mod P)
//   - sessionID:  bound to this specific game session (prevents replay)
func ProveDecryption(key *SRAKey, ciphertext, result *big.Int, sessionID []byte) (*ZKProof, error) {
	P := key.P
	phi := new(big.Int).Sub(P, big.NewInt(1)) // φ(P) = P-1

	// h = G^d mod P — the public key commitment.
	h := new(big.Int).Exp(g, key.D, P)

	// Pick random r in [1, φ(P)-1].
	r, err := rand.Int(rand.Reader, phi)
	if err != nil {
		return nil, fmt.Errorf("ProveDecryption: rand: %w", err)
	}
	if r.Sign() == 0 {
		r.SetInt64(1) // avoid r=0
	}

	// A = G^r mod P
	A := new(big.Int).Exp(g, r, P)
	// B = ciphertext^r mod P
	B := new(big.Int).Exp(ciphertext, r, P)

	// Challenge c = H(G, h, ciphertext, result, A, B, sessionID).
	c := computeChallenge(P, h, ciphertext, result, A, B, sessionID)

	// s = (r + c*d) mod φ(P)
	s := new(big.Int).Mul(c, key.D)
	s.Add(s, r)
	s.Mod(s, phi)

	return &ZKProof{A: A, B: B, S: s, H: h}, nil
}

// VerifyDecryption verifies a ZK proof that result = ciphertext^d mod P,
// where d corresponds to the public commitment H = G^d mod P.
//
// Returns nil on success, an error describing the failure otherwise.
func VerifyDecryption(proof *ZKProof, ciphertext, result *big.Int, P *big.Int, sessionID []byte) error {
	if proof == nil {
		return errors.New("VerifyDecryption: proof is nil")
	}
	if proof.A == nil || proof.B == nil || proof.S == nil || proof.H == nil {
		return errors.New("VerifyDecryption: proof has nil fields")
	}

	// Recompute the challenge.
	c := computeChallenge(P, proof.H, ciphertext, result, proof.A, proof.B, sessionID)

	// Check 1: G^s ≡ A * H^c  (mod P)
	lhs1 := new(big.Int).Exp(g, proof.S, P)
	hc := new(big.Int).Exp(proof.H, c, P)
	rhs1 := new(big.Int).Mul(proof.A, hc)
	rhs1.Mod(rhs1, P)
	if lhs1.Cmp(rhs1) != 0 {
		return fmt.Errorf("VerifyDecryption: check 1 failed: G^s=%s ≠ A*H^c=%s", lhs1, rhs1)
	}

	// Check 2: ciphertext^s ≡ B * result^c  (mod P)
	lhs2 := new(big.Int).Exp(ciphertext, proof.S, P)
	resultC := new(big.Int).Exp(result, c, P)
	rhs2 := new(big.Int).Mul(proof.B, resultC)
	rhs2.Mod(rhs2, P)
	if lhs2.Cmp(rhs2) != 0 {
		return fmt.Errorf("VerifyDecryption: check 2 failed: ct^s=%s ≠ B*result^c=%s", lhs2, rhs2)
	}

	return nil
}

// computeChallenge produces the Fiat-Shamir hash challenge c from the
// protocol transcript.  All inputs are big-endian byte-encoded.
// The sessionID is the last input, binding the proof to a specific game.
func computeChallenge(P, h, ciphertext, result, A, B *big.Int, sessionID []byte) *big.Int {
	hash := sha256.New()
	for _, v := range []*big.Int{P, h, ciphertext, result, A, B} {
		b := v.Bytes()
		// Length-prefix each value to prevent concatenation collisions.
		length := make([]byte, 4)
		length[0] = byte(len(b) >> 24)
		length[1] = byte(len(b) >> 16)
		length[2] = byte(len(b) >> 8)
		length[3] = byte(len(b))
		hash.Write(length)
		hash.Write(b)
	}
	hash.Write(sessionID)
	digest := hash.Sum(nil)

	// Reduce the 256-bit hash to a challenge in [0, P-1].
	c := new(big.Int).SetBytes(digest)
	phi := new(big.Int).Sub(P, big.NewInt(1))
	c.Mod(c, phi)
	return c
}

// PartialDecryption bundles a single player's decryption of one card slot
// together with its ZK proof of correctness.
type PartialDecryption struct {
	PlayerID   string
	CardIndex  int      // which slot in the encrypted deck
	Ciphertext *big.Int // the input value this player decrypted
	Result     *big.Int // ciphertext^d mod P
	Proof      *ZKProof
}

// Verify checks that this partial decryption is valid under the given session.
func (pd *PartialDecryption) Verify(P *big.Int, sessionID []byte) error {
	if err := VerifyDecryption(pd.Proof, pd.Ciphertext, pd.Result, P, sessionID); err != nil {
		return fmt.Errorf("player %s card %d: %w", pd.PlayerID, pd.CardIndex, err)
	}
	return nil
}
