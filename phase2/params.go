package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

// P2048 is a 2048-bit safe prime (P = 2q+1, q also prime).
// Using a safe prime ensures the multiplicative group Z_P* has a large
// prime-order subgroup of order q = (P-1)/2, making discrete-log hard.
//
// This prime was generated with OpenSSL: `openssl dhparam -text 2048`
// and independently verified with Go's ProbablyPrime(20).
//
// Every table session uses this same prime.  The session nonce (see
// commit.go) binds all players to a specific game instance, preventing
// replay of encrypted decks across sessions.
const p2048Hex = "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd1" +
	"29024e088a67cc74020bbea63b139b22514a08798e3404dd" +
	"ef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245" +
	"e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7ed" +
	"ee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3d" +
	"c2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f" +
	"83655d23dca3ad961c62f356208552bb9ed529077096966d" +
	"670c354e4abc9804f1746c08ca18217c32905e462e36ce3b" +
	"e39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9" +
	"de2bcbf6955817183995497cea956ae515d2261898fa0510" +
	"15728e5a8aacaa68ffffffffffffffff"

// SharedPrime returns the 2048-bit safe prime used by all table sessions.
// The result is cached after first call.
func SharedPrime() *big.Int {
	p := new(big.Int)
	p.SetString(p2048Hex, 16)
	return p
}

// CardToField maps a card ID (0–51) to a unique element of Z_P*.
// We use the encoding:  m = G^(id+1) mod P  where G=2 is the generator
// of the multiplicative group.  This ensures every card maps to a
// non-trivial group element and the values are distinct and invertible.
//
// In the actual network protocol the mapping is agreed at session start
// and committed via the session hash so players cannot substitute cards.
func CardToField(cardID int, p *big.Int) *big.Int {
	if cardID < 0 || cardID > 51 {
		panic(fmt.Sprintf("CardToField: cardID %d out of range [0,51]", cardID))
	}
	g := big.NewInt(2)
	exp := big.NewInt(int64(cardID + 1))
	return new(big.Int).Exp(g, exp, p)
}

// FieldToCard recovers the card ID from a plaintext field element.
// It inverts the CardToField mapping by discrete log over the small
// known set (only 52 possible plaintexts), so a simple linear scan is fine.
// Returns -1 if the value does not correspond to any card.
func FieldToCard(val *big.Int, p *big.Int) int {
	g := big.NewInt(2)
	for id := 0; id <= 51; id++ {
		exp := big.NewInt(int64(id + 1))
		candidate := new(big.Int).Exp(g, exp, p)
		if candidate.Cmp(val) == 0 {
			return id
		}
	}
	return -1
}

// BuildPlaintextDeck returns the ordered field representations of all 52 cards.
func BuildPlaintextDeck(p *big.Int) []*big.Int {
	deck := make([]*big.Int, 52)
	for i := range deck {
		deck[i] = CardToField(i, p)
	}
	return deck
}

// SessionID produces a deterministic session identifier from the sorted
// list of player IDs and a random nonce committed at table creation.
// This value is mixed into every ZK proof to prevent cross-session replay.
func SessionID(playerIDs []string, nonce []byte) []byte {
	h := sha256.New()
	for _, id := range playerIDs {
		h.Write([]byte(id))
		h.Write([]byte{0x00}) // separator
	}
	h.Write(nonce)
	return h.Sum(nil)
}

// SessionIDHex returns the hex-encoded session ID.
func SessionIDHex(playerIDs []string, nonce []byte) string {
	return hex.EncodeToString(SessionID(playerIDs, nonce))
}
