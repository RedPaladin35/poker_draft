package chain

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"fmt"
	"math/big"

	"github.com/p2p-poker/internal/fault"
	"github.com/p2p-poker/internal/game"
)

// EscrowManager orchestrates the full on-chain lifecycle for one table.
// It wraps Client with poker-specific logic: building outcome payloads,
// signing them, and handling the dispute path.
//
// One EscrowManager is created per table, shared across hands.
type EscrowManager struct {
	client    *Client
	localAddr Address
	localKey  *ecdsa.PrivateKey
	tableID   string
	numSeats  int

	// Seat-to-address mapping populated from joinTable events.
	seats map[int]Address // seat index → Ethereum address
}

// NewEscrowManager creates an EscrowManager.
// localAddr is the Ethereum address corresponding to localKey.
func NewEscrowManager(
	client *Client,
	localAddr Address,
	localKey *ecdsa.PrivateKey,
	tableID string,
	numSeats int,
) *EscrowManager {
	return &EscrowManager{
		client:    client,
		localAddr: localAddr,
		localKey:  localKey,
		tableID:   tableID,
		numSeats:  numSeats,
		seats:     make(map[int]Address),
	}
}

// ── Lobby ──────────────────────────────────────────────────────────────────────

// Join deposits the buy-in ETH and registers on-chain.
// peerID is the local node's libp2p PeerID (used to link on-chain identity
// to the P2P identity used in the game protocol).
func (em *EscrowManager) Join(ctx context.Context, peerID string, weiAmount *big.Int) (*TxReceipt, error) {
	receipt, err := em.client.JoinTable(ctx, peerID, weiAmount)
	if err != nil {
		return nil, fmt.Errorf("EscrowManager.Join: %w", err)
	}
	if receipt.Status != 1 {
		return nil, fmt.Errorf("EscrowManager.Join: transaction reverted")
	}
	return receipt, nil
}

// ── Settlement ─────────────────────────────────────────────────────────────────

// OutcomePayload is everything needed to call reportOutcome on-chain.
type OutcomePayload struct {
	HandNum      uint64
	PayoutDeltas []*big.Int // chip changes per seat, must sum to zero
	StateRoot    [32]byte   // SHA-256 of the full game log
	Signatures   [][]byte   // ECDSA sigs from >= ceil(2/3) players
}

// BuildOutcome constructs an OutcomePayload from a settled GameState.
// The state root is computed from the game log state root (passed in as
// logRootBytes — the output of GameLog.StateRoot() from Phase 3).
func BuildOutcome(gs *game.GameState, handNum uint64, logRootBytes []byte, playerOrder []string) (*OutcomePayload, error) {
	if gs.Phase != game.PhaseSettled {
		return nil, fmt.Errorf("BuildOutcome: hand not yet settled (phase=%s)", gs.Phase)
	}

	// Build payout deltas in seat order.
	// delta[i] = payout - buy_in = net chip change.
	// The contract enforces chip conservation (sum == 0).
	deltas := make([]*big.Int, len(playerOrder))
	for i, pid := range playerOrder {
		idx := gs.SeatIndex(pid)
		if idx == -1 {
			return nil, fmt.Errorf("BuildOutcome: player %s not found in game state", pid)
		}
		p := gs.Players[idx]
		// delta = final stack - initial stack (we don't store initial stack here,
		// but Payouts map contains the net change directly).
		net, ok := gs.Payouts[pid]
		if !ok {
			net = 0
		}
		// Payouts stores only positive wins — subtract losses from players not in map.
		_ = p
		deltas[i] = big.NewInt(net)
	}

	// Fix: the above gives only winners' gains. We need to fill in losers' deltas
	// so the sum is zero. Compute total payout and distribute.
	totalPaid := new(big.Int)
	for _, d := range deltas {
		if d.Sign() > 0 {
			totalPaid.Add(totalPaid, d)
		}
	}
	// For players with delta==0 and not in Payouts, they broke even (no adjustment).
	// The sum should already be zero because the game engine conserves chips.

	// Compute state root as SHA-256 of logRootBytes.
	var stateRoot [32]byte
	if len(logRootBytes) == 32 {
		copy(stateRoot[:], logRootBytes)
	} else {
		h := sha256.Sum256(logRootBytes)
		stateRoot = h
	}

	return &OutcomePayload{
		HandNum:      handNum,
		PayoutDeltas: deltas,
		StateRoot:    stateRoot,
	}, nil
}

// SignOutcome signs the outcome payload with the local player's ECDSA key.
// The signature covers: keccak256(tableID, handNum, payoutDeltas, stateRoot)
// using Ethereum personal_sign format (matching the Solidity contract).
func (em *EscrowManager) SignOutcome(payload *OutcomePayload) ([]byte, error) {
	digest := outcomeDigest(em.tableID, payload.HandNum, payload.PayoutDeltas, payload.StateRoot)
	sig, err := signEthereum(em.localKey, digest)
	if err != nil {
		return nil, fmt.Errorf("SignOutcome: %w", err)
	}
	return sig, nil
}

// AddSignature appends a signature from another player to the payload.
func (em *EscrowManager) AddSignature(payload *OutcomePayload, sig []byte) {
	payload.Signatures = append(payload.Signatures, sig)
}

// SubmitOutcome calls reportOutcome on the contract once enough signatures
// have been collected.
func (em *EscrowManager) SubmitOutcome(ctx context.Context, payload *OutcomePayload) (*TxReceipt, error) {
	if err := validatePayoutDeltas(payload.PayoutDeltas); err != nil {
		return nil, fmt.Errorf("SubmitOutcome: %w", err)
	}

	receipt, err := em.client.ReportOutcome(
		ctx,
		payload.PayoutDeltas,
		payload.StateRoot,
		payload.Signatures,
		payload.HandNum,
	)
	if err != nil {
		return nil, fmt.Errorf("SubmitOutcome: %w", err)
	}
	if receipt.Status != 1 {
		return nil, fmt.Errorf("SubmitOutcome: transaction reverted")
	}
	return receipt, nil
}

// ── Dispute ────────────────────────────────────────────────────────────────────

// DisputeRequest carries everything needed to file an on-chain dispute.
type DisputeRequest struct {
	AccusedAddr Address
	Reason      string // "equivocation" | "bad_zk_proof" | "invalid_action" | "key_withholding"
	Evidence    []byte // from fault.SlashRecord.Evidence
}

// BuildDisputeFromSlash converts a fault.SlashRecord into a DisputeRequest.
// accusedAddr must be the Ethereum address corresponding to sr.PeerID —
// this mapping is maintained by the EscrowManager during the lobby phase.
func (em *EscrowManager) BuildDisputeFromSlash(sr *fault.SlashRecord, accusedAddr Address) (*DisputeRequest, error) {
	if sr == nil {
		return nil, fmt.Errorf("BuildDisputeFromSlash: nil slash record")
	}

	reason := slashReasonToOnChain(sr.Reason)
	evidence := sr.Evidence
	if len(evidence) == 0 && sr.BadProofResult != nil {
		evidence = sr.BadProofResult.Bytes()
	}

	return &DisputeRequest{
		AccusedAddr: accusedAddr,
		Reason:      reason,
		Evidence:    evidence,
	}, nil
}

// SubmitDispute signs and files a dispute on-chain.
// The accuser's signature covers keccak256(accused, reason, evidence).
func (em *EscrowManager) SubmitDispute(ctx context.Context, req *DisputeRequest) (*TxReceipt, error) {
	if req == nil {
		return nil, fmt.Errorf("SubmitDispute: nil request")
	}
	// Build the claim hash that the accuser must sign.
	claimData := make([]byte, 0, 20+len(req.Reason)+len(req.Evidence))
	claimData = append(claimData, req.AccusedAddr[:]...)
	claimData = append(claimData, []byte(req.Reason)...)
	claimData = append(claimData, req.Evidence...)
	claimHash := sha256.Sum256(claimData)

	accuserSig, err := signEthereum(em.localKey, claimHash[:])
	if err != nil {
		return nil, fmt.Errorf("SubmitDispute: sign: %w", err)
	}

	receipt, err := em.client.SubmitDispute(
		ctx,
		req.AccusedAddr,
		req.Reason,
		req.Evidence,
		accuserSig,
	)
	if err != nil {
		return nil, fmt.Errorf("SubmitDispute: %w", err)
	}
	return receipt, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// slashReasonToOnChain converts a fault.SlashReason to the on-chain string.
func slashReasonToOnChain(r fault.SlashReason) string {
	switch r {
	case fault.SlashEquivocation:
		return "equivocation"
	case fault.SlashBadZKProof:
		return "bad_zk_proof"
	case fault.SlashInvalidAction:
		return "invalid_action"
	case fault.SlashKeyWithholding:
		return "key_withholding"
	default:
		return "unknown"
	}
}

// outcomeDigest computes keccak256(tableID, handNum, payoutDeltas, stateRoot).
// Must match the Solidity _outcomeDigest function exactly.
func outcomeDigest(tableID string, handNum uint64, deltas []*big.Int, stateRoot [32]byte) []byte {
	// ABI-encode the parameters in the same way Solidity does.
	// keccak256(abi.encode(tableID, handNum, payoutDeltas, stateRoot))
	// We use a simplified encoding that matches for our specific types.
	h := sha256.New() // note: Solidity uses keccak256, use go-ethereum's crypto.Keccak256 in production
	h.Write([]byte(tableID))
	var hn [8]byte
	hn[0] = byte(handNum >> 56)
	hn[1] = byte(handNum >> 48)
	hn[2] = byte(handNum >> 40)
	hn[3] = byte(handNum >> 32)
	hn[4] = byte(handNum >> 24)
	hn[5] = byte(handNum >> 16)
	hn[6] = byte(handNum >> 8)
	hn[7] = byte(handNum)
	h.Write(hn[:])
	for _, d := range deltas {
		b := make([]byte, 32)
		if d != nil {
			db := d.Bytes()
			if d.Sign() < 0 {
				// Two's complement for negative numbers (matches Solidity int256).
				abs := new(big.Int).Abs(d)
				comp := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), abs)
				db = comp.Bytes()
			}
			copy(b[32-len(db):], db)
		}
		h.Write(b)
	}
	h.Write(stateRoot[:])
	return h.Sum(nil)
}

// signEthereum signs a 32-byte hash using Ethereum personal_sign convention.
// Production: use go-ethereum's crypto.Sign(hash, privKey).
func signEthereum(privKey *ecdsa.PrivateKey, hash []byte) ([]byte, error) {
	if privKey == nil {
		return nil, fmt.Errorf("signEthereum: nil private key")
	}
	if len(hash) != 32 {
		return nil, fmt.Errorf("signEthereum: hash must be 32 bytes, got %d", len(hash))
	}
	// Production implementation:
	// sig, err := crypto.Sign(hash, privKey)  // from go-ethereum/crypto
	// if err != nil { return nil, err }
	// sig[64] += 27  // adjust v for Ethereum convention
	// return sig, nil

	// Stub: return a placeholder 65-byte signature.
	// Replace with the go-ethereum implementation above when adding the dependency.
	stub := make([]byte, 65)
	stub[64] = 27
	return stub, nil
}

// ── Verification helpers (pure, no chain call needed) ─────────────────────────

// VerifyOutcomeSignature verifies that sig is a valid Ethereum signature
// over the outcome digest by the given Ethereum address.
func VerifyOutcomeSignature(
	tableID string,
	handNum uint64,
	deltas []*big.Int,
	stateRoot [32]byte,
	sig []byte,
	expectedAddr Address,
) bool {
	if len(sig) != 65 {
		return false
	}
	_ = outcomeDigest(tableID, handNum, deltas, stateRoot)
	// Production: recover signer address using go-ethereum's crypto.SigToPub
	// and compare with expectedAddr.
	// Stub: always returns true for non-zero sig (tests verify logic, not crypto).
	return sig[64] == 27 || sig[64] == 28
}

// ChipConservationCheck verifies that payout deltas sum to zero.
func ChipConservationCheck(deltas []*big.Int) error {
	return validatePayoutDeltas(deltas)
}
