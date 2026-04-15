# P2P Poker Protocol Specification

Version 1.0 — Phase 7

## Overview

This document specifies the wire protocol and cryptographic protocol for the
decentralised P2P Texas Hold'em poker engine. It is precise enough for a
third-party implementation to interoperate.

---

## 1. Identities

Every player is identified by their **libp2p PeerID** — a base58-encoded
SHA-256 hash of their Ed25519 public key. The PeerID is used as:

- The in-game player ID (seat name)
- The signing identity for all network messages
- The key for Shamir share distribution

The Ethereum address used for on-chain settlement is a separate identity,
linked to the PeerID by the `joinTable` contract call.

---

## 2. Message Format

All messages are transmitted as length-prefixed protobuf-encoded `Envelope`
values.

```
Frame := uint32_big_endian(len(proto_bytes)) || proto_bytes
```

The `Envelope` message:

```protobuf
message Envelope {
  MsgType type      = 1;
  string  sender_id = 2;   // libp2p PeerID
  int64   seq       = 3;   // monotonically increasing per sender, starts at 1
  int64   timestamp = 4;   // unix milliseconds
  bytes   payload   = 5;   // inner message, protobuf-encoded
  bytes   signature = 6;   // Ed25519 over sign_bytes(envelope)
}
```

**Signing bytes** (must be reproduced exactly):

```
sign_bytes = byte(type) || sender_id_bytes || 0x00 || seq_big_endian_8 || timestamp_big_endian_8 || payload
```

**Replay protection**: every receiver maintains a `last_seq[senderID]` map.
A message is accepted only if `seq > last_seq[senderID]`.

---

## 3. Transport

- **Public game messages** (actions, shuffle steps, partial decryptions,
  heartbeats) are published on the GossipSub topic `poker/table/<tableID>`.
- **Private messages** (hole card partial decryptions destined for one player)
  are sent over a direct `/poker/1.0.0` libp2p stream.

---

## 4. Lobby Protocol

```
Player A                  Player B                  Player C
   │── JOIN_TABLE ─────────────────────────────────►│
   │── JOIN_TABLE ─────────►│                       │
   │◄─ JOIN_TABLE ──────────│                       │
   │── PLAYER_READY ────────────────────────────────►│
   │── PLAYER_READY ────────►│                       │
   │◄─ PLAYER_READY ─────────│                       │
   │   [all ready — hand begins]
```

`JoinTable.session_nonce` is this player's contribution to the shared
session nonce. The full session nonce is the concatenation of all player
nonces in join order, used to bind ZK proofs to this specific game instance.

---

## 5. Mental Poker Shuffle Protocol

### 5.1 Parameters

All players use the shared 2048-bit safe prime P defined in `params.go`.
The generator G = 2.

Card encoding: card ID `i` (0–51) maps to field element `G^(i+1) mod P`.

### 5.2 Key Generation

Each player generates a fresh SRA key pair `(e, d)` such that `e·d ≡ 1 (mod P−1)`.
Key pairs must be freshly generated per session. Reuse across sessions enables
cross-session replay attacks.

### 5.3 Shuffle

1. Player 1 receives the plaintext deck `[G^1, G^2, ..., G^52] mod P`.
2. Player 1 encrypts all 52 values: `c_i = m_i^e1 mod P`.
3. Player 1 applies a cryptographically random permutation.
4. Player 1 broadcasts the commitment `H(shuffled_deck || nonce)`.
5. Player 2 receives player 1's output, repeats steps 2–4.
6. Continue until all N players have shuffled.
7. All players reveal their commitments in order. Any player can verify
   `H(deck || nonce) == commitment` to detect substitution.

### 5.4 Hole Card Deal

To reveal deck slot `i` to player `j`:

1. Every player `k ≠ j` computes `partial_k = ciphertext_i^dk mod P`
   and broadcasts `(partial_k, ZKProof_k)`.
2. Each ZK proof is verified by all receivers before applying the decryption.
3. Player `j` receives the final intermediate value and decrypts with `d_j`.

### 5.5 Community Card Deal

To reveal a community card (deck slot `i`):

1. **All** N players compute and broadcast a partial decryption with ZK proof.
2. Any player can apply them in sequence to recover the plaintext.

---

## 6. ZK Proof Format (Chaum-Pedersen)

The proof that `result = ciphertext^d mod P` without revealing `d`:

```
Prover:
  h = G^d mod P              (key commitment, broadcast once)
  r = random in [1, P-2]
  A = G^r mod P
  B = ciphertext^r mod P
  c = H(P, h, ciphertext, result, A, B, sessionID)
  s = (r + c·d) mod (P-1)
  proof = (A, B, s, h)

Verifier:
  c = H(P, h, ciphertext, result, A, B, sessionID)
  check: G^s ≡ A · h^c (mod P)
  check: ciphertext^s ≡ B · result^c (mod P)
```

The `sessionID` is `SHA-256(player_ids_in_join_order || combined_nonce)`.
It MUST be included in the challenge hash to prevent cross-session replay.

Hash function: SHA-256 with length-prefixed inputs.
All `big.Int` values are encoded big-endian with a 4-byte length prefix.

---

## 7. Fault Tolerance

### 7.1 Heartbeat

Each node broadcasts a `Heartbeat` message every 5 seconds on the
`poker/heartbeat/<tableID>` GossipSub topic. Missing 3 consecutive intervals
(15 seconds) marks a peer as `PeerTimedOut`.

### 7.2 Timeout Vote

When a peer times out AND it is their turn to act:

1. Any node broadcasts a `TimeoutVote` message.
2. Votes are collected for up to 30 seconds.
3. If `⌈(N−1) × 2/3⌉` votes are received: the player is force-folded.
4. The folded player's hand is treated as a fold in the game engine.
5. The fold action is broadcast as a `PLAYER_ACTION` message so all nodes
   apply the same state transition.

### 7.3 Key Recovery (Shamir)

At hand start, each player `i` splits their decryption key `d_i` into N
shares with threshold `T = ⌈N/2⌉` using Shamir Secret Sharing over Z_P.
Share `j` is sent privately to player `j` via the `/poker/1.0.0` stream.

If player `i` disconnects during the deal:
1. Any remaining player broadcasts a "need key for player i" message.
2. All other players broadcast their share.
3. Once T shares are collected, any player can reconstruct `d_i`.
4. Partial decryptions for player `i` are computed on their behalf.

### 7.4 Slash Evidence

Protocol violations generate `SlashRecord` objects with:
- `PeerID`: the offending player
- `Reason`: `equivocation | bad_zk_proof | invalid_action | key_withholding`
- `Evidence`: serialised proof bytes
- `HandNum`: the hand where the violation occurred

Slash records are included in the `HandResult` broadcast and submitted
with the `reportOutcome` on-chain transaction. The smart contract verifies
the evidence and executes the slash.

---

## 8. On-Chain Settlement

### 8.1 Outcome Digest

The outcome digest (signed by each player) is:

```
digest = SHA-256(tableID || handNum_big_endian_8 || delta_0_big_endian_32 || ... || stateRoot)
```

Where each `delta_i` is a 32-byte two's-complement encoding of the payout
delta for player `i` (positive = won chips, negative = lost chips).
The deltas must sum to zero.

In production, use `keccak256(abi.encode(...))` to match Solidity exactly.

### 8.2 Multi-sig Threshold

A valid `reportOutcome` call requires signatures from at least
`⌈N × 2/3⌉` players, verified using `ecrecover` in the Solidity contract.

### 8.3 State Root

The state root is `SHA-256` over the game log in insertion order:

```
for each entry:
  hash.Write(byte(type) || senderID || 0x00 || seq_big_endian_8 || payload || signature)
```

This root is stored on-chain after settlement and can be used by any party
to verify that the submitted outcome matches the observed game log.

---

## 9. Game Rules

Standard Texas Hold'em:
- Pre-flop, Flop, Turn, River betting rounds
- Dealer button rotates left each hand
- Heads-up: dealer posts small blind
- Minimum raise = previous raise size or big blind (whichever is larger)
- All-in creates side pots (standard Texas Hold'em rules)
- Odd chip in split pots goes to the player closest left of the dealer

---

## 10. Interoperability Checklist

A compatible implementation must:

- [ ] Use the shared 2048-bit safe prime P from `params.go`
- [ ] Encode cards as `G^(id+1) mod P` with G=2
- [ ] Implement the Chaum-Pedersen ZK proof with sessionID binding
- [ ] Use length-prefixed SHA-256 for the challenge hash
- [ ] Implement the Shamir share protocol with threshold `⌈N/2⌉`
- [ ] Sign envelopes with Ed25519 using the exact `sign_bytes` layout
- [ ] Maintain per-sender monotonic sequence numbers
- [ ] Implement the 2/3 majority timeout vote protocol
- [ ] Compute the outcome digest as specified in §8.1
- [ ] Publish on GossipSub topics `poker/table/<id>` and `poker/heartbeat/<id>`
- [ ] Handle the `/poker/1.0.0` direct stream protocol
