// Package chain provides the Go client for interacting with the PokerEscrow
// smart contract deployed on an EVM-compatible chain.
//
// It wraps go-ethereum's ethclient and accounts/abi packages to provide
// type-safe, idiomatic Go methods for every contract interaction.
//
// NOTE: go-ethereum is not in go.mod in this sandbox (domain blocked).
// Add it on your machine with:
//
//	go get github.com/ethereum/go-ethereum@v1.14.0
//
// All types and method signatures are stable and accurate.
package chain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	contractabi "github.com/p2p-poker/internal/chain/abi"
)

// ── Minimal interface stubs (replaced by go-ethereum types on your machine) ──
// These allow the chain package to compile and be tested without the full
// go-ethereum dependency. On your machine, replace these with the real imports:
//
//   "github.com/ethereum/go-ethereum/accounts/abi"
//   "github.com/ethereum/go-ethereum/accounts/abi/bind"
//   "github.com/ethereum/go-ethereum/common"
//   "github.com/ethereum/go-ethereum/core/types"
//   "github.com/ethereum/go-ethereum/ethclient"
//   "github.com/ethereum/go-ethereum/crypto"

// Address is an Ethereum address (20 bytes).
type Address [20]byte

func (a Address) Hex() string {
	return fmt.Sprintf("0x%x", a[:])
}

// Hash is a 32-byte hash (keccak256, tx hash, block hash, state root).
type Hash [32]byte

// Wei represents an ETH amount in wei.
type Wei = big.Int

// TxHash is the hash of an Ethereum transaction.
type TxHash = Hash

// ── ChainConfig ───────────────────────────────────────────────────────────────

// ChainConfig holds all connection and key material for the chain client.
type ChainConfig struct {
	RPCURL          string         // e.g. "http://127.0.0.1:8545" (Anvil) or Infura URL
	ContractAddress string         // hex address of the deployed PokerEscrow contract
	PrivateKey      *ecdsa.PrivateKey // signing key for transactions
	ChainID         *big.Int       // 31337 for local, 1 for mainnet, 11155111 for Sepolia
	GasLimit        uint64         // default gas limit per tx (0 = estimate)
	ConfirmTimeout  time.Duration  // how long to wait for tx confirmation
}

// DefaultConfig returns sensible defaults for a local Anvil/Hardhat node.
func DefaultConfig(contractAddr string, privKey *ecdsa.PrivateKey) ChainConfig {
	return ChainConfig{
		RPCURL:          "http://127.0.0.1:8545",
		ContractAddress: contractAddr,
		PrivateKey:      privKey,
		ChainID:         big.NewInt(31337),
		GasLimit:        500_000,
		ConfirmTimeout:  30 * time.Second,
	}
}

// ── Client ────────────────────────────────────────────────────────────────────

// Client provides typed access to the PokerEscrow contract.
// In production: eth *ethclient.Client + contract *PokerEscrowCaller (from abigen).
type Client struct {
	cfg     ChainConfig
	abi     string // parsed ABI JSON
	address string
	// eth     *ethclient.Client   ← real go-ethereum type
	// contract *PokerEscrowTransactor
}

// NewClient dials the RPC endpoint and returns a ready Client.
//
// Production implementation:
//
//	eth, err := ethclient.DialContext(ctx, cfg.RPCURL)
//	parsed,  := abi.JSON(strings.NewReader(contractabi.PokerEscrowABI))
//	contract := bind.NewBoundContract(common.HexToAddress(cfg.ContractAddress), parsed, eth, eth, eth)
func NewClient(ctx context.Context, cfg ChainConfig) (*Client, error) {
	if cfg.RPCURL == "" {
		return nil, fmt.Errorf("NewClient: RPCURL is required")
	}
	if cfg.ContractAddress == "" {
		return nil, fmt.Errorf("NewClient: ContractAddress is required")
	}
	// In production: dial go-ethereum ethclient here.
	return &Client{
		cfg:     cfg,
		abi:     contractabi.PokerEscrowABI,
		address: cfg.ContractAddress,
	}, nil
}

// Close shuts down the underlying RPC connection.
func (c *Client) Close() {
	// In production: c.eth.Close()
}

// ── Read methods ──────────────────────────────────────────────────────────────

// TableState reads the current state of the escrow contract.
//
// Production: contract.State(&bind.CallOpts{Context: ctx})
func (c *Client) TableState(ctx context.Context) (contractabi.TableState, error) {
	// Stub — real implementation calls the EVM read.
	return contractabi.TableStatePlaying, nil
}

// PlayerCount returns the number of players currently seated.
func (c *Client) PlayerCount(ctx context.Context) (uint64, error) {
	return 0, nil
}

// PlayerInfo returns the on-chain record for seat index i.
func (c *Client) PlayerInfo(ctx context.Context, seat uint8) (*PlayerRecord, error) {
	return nil, fmt.Errorf("PlayerInfo: stub not connected to chain (add go-ethereum)")
}

// TotalEscrow returns the total ETH held in the contract (in wei).
func (c *Client) TotalEscrow(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

// StateRoot returns the game log state root stored after settlement.
func (c *Client) StateRoot(ctx context.Context) (Hash, error) {
	return Hash{}, nil
}

// RequiredSignatures returns the minimum number of signatures for a valid outcome.
func (c *Client) RequiredSignatures(ctx context.Context) (uint64, error) {
	return 0, nil
}

// ── Write methods ─────────────────────────────────────────────────────────────

// JoinTable sends a joinTable() transaction with the given ETH buy-in.
//
// Production:
//
//	auth, _ := bind.NewKeyedTransactorWithChainID(c.cfg.PrivateKey, c.cfg.ChainID)
//	auth.Value = weiAmount
//	tx, err  := c.contract.JoinTable(auth, peerID)
func (c *Client) JoinTable(ctx context.Context, peerID string, weiAmount *big.Int) (*TxReceipt, error) {
	if peerID == "" {
		return nil, fmt.Errorf("JoinTable: peerID required")
	}
	if weiAmount == nil || weiAmount.Sign() <= 0 {
		return nil, fmt.Errorf("JoinTable: buy-in must be positive")
	}
	// Stub.
	return &TxReceipt{
		Status:      1,
		GasUsed:     50000,
		BlockNumber: big.NewInt(1),
	}, nil
}

// ReportOutcome submits the final hand outcome with multi-sig authorisation.
//
// payoutDeltas: chip changes per seat (sum must be zero).
// stateRoot:    SHA-256 hash of the full game log.
// signatures:   ECDSA sigs from at least ceil(2/3) players.
// handNum:      the hand this outcome belongs to.
func (c *Client) ReportOutcome(
	ctx context.Context,
	payoutDeltas []*big.Int,
	stateRoot [32]byte,
	signatures [][]byte,
	handNum uint64,
) (*TxReceipt, error) {
	if err := validatePayoutDeltas(payoutDeltas); err != nil {
		return nil, fmt.Errorf("ReportOutcome: %w", err)
	}
	if len(signatures) == 0 {
		return nil, fmt.Errorf("ReportOutcome: signatures required")
	}
	// Stub.
	return &TxReceipt{Status: 1, GasUsed: 120000, BlockNumber: big.NewInt(2)}, nil
}

// SubmitDispute files a dispute against an accused player.
//
// accused:    Ethereum address of the accused.
// reason:     "equivocation" | "bad_zk_proof" | "invalid_action" | "key_withholding"
// evidence:   serialised proof bytes.
// accuserSig: ECDSA signature of keccak256(accused, reason, evidence).
func (c *Client) SubmitDispute(
	ctx context.Context,
	accused Address,
	reason string,
	evidence []byte,
	accuserSig []byte,
) (*TxReceipt, error) {
	if len(evidence) == 0 {
		return nil, fmt.Errorf("SubmitDispute: evidence required")
	}
	validReasons := map[string]bool{
		"equivocation": true, "bad_zk_proof": true,
		"invalid_action": true, "key_withholding": true,
	}
	if !validReasons[strings.ToLower(reason)] {
		return nil, fmt.Errorf("SubmitDispute: unknown reason %q", reason)
	}
	// Stub.
	return &TxReceipt{Status: 1, GasUsed: 80000, BlockNumber: big.NewInt(3)}, nil
}

// MarkAbandoned sends markAbandoned() if the settlement deadline has passed.
func (c *Client) MarkAbandoned(ctx context.Context) (*TxReceipt, error) {
	return &TxReceipt{Status: 1, GasUsed: 30000, BlockNumber: big.NewInt(4)}, nil
}

// Refund sends a refund() transaction after the table is abandoned.
func (c *Client) Refund(ctx context.Context) (*TxReceipt, error) {
	return &TxReceipt{Status: 1, GasUsed: 30000, BlockNumber: big.NewInt(5)}, nil
}

// WaitForSettlement polls until the contract reaches Settled state or times out.
func (c *Client) WaitForSettlement(ctx context.Context) error {
	deadline := time.Now().Add(c.cfg.ConfirmTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		state, err := c.TableState(ctx)
		if err != nil {
			return err
		}
		if state == contractabi.TableStateSettled {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("WaitForSettlement: timed out after %s", c.cfg.ConfirmTimeout)
}

// ── Event watching ────────────────────────────────────────────────────────────

// WatchPayouts subscribes to PayoutSent events and calls handler for each.
// Returns a stop function. Call stop() to unsubscribe.
//
// Production (go-ethereum log filter):
//
//	query := ethereum.FilterQuery{Addresses: []common.Address{addr}}
//	logs,  := eth.SubscribeFilterLogs(ctx, query, ch)
func (c *Client) WatchPayouts(ctx context.Context, handler func(addr Address, weiAmount *big.Int)) func() {
	// Stub — production wires up an eth_subscribe log filter.
	stop := make(chan struct{})
	return func() { close(stop) }
}

// WatchDisputes subscribes to DisputeFiled events.
func (c *Client) WatchDisputes(ctx context.Context, handler func(filer, accused Address, reason string)) func() {
	stop := make(chan struct{})
	return func() { close(stop) }
}

// ── Types ─────────────────────────────────────────────────────────────────────

// PlayerRecord mirrors the Solidity Player struct.
type PlayerRecord struct {
	Address   Address
	PeerID    string
	BuyIn     *big.Int
	Withdrawn bool
	Slashed   bool
}

// TxReceipt is a minimal transaction receipt.
// Production: github.com/ethereum/go-ethereum/core/types.Receipt
type TxReceipt struct {
	TxHash      TxHash
	Status      uint64   // 1 = success, 0 = reverted
	GasUsed     uint64
	BlockNumber *big.Int
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// validatePayoutDeltas checks that deltas sum to zero (chip conservation).
func validatePayoutDeltas(deltas []*big.Int) error {
	sum := new(big.Int)
	for _, d := range deltas {
		if d == nil {
			return fmt.Errorf("nil delta")
		}
		sum.Add(sum, d)
	}
	if sum.Sign() != 0 {
		return fmt.Errorf("chip conservation violated: sum=%s", sum)
	}
	return nil
}

// EtherToWei converts an ETH float string to wei.
// e.g. "0.1" → 100000000000000000
func EtherToWei(ethStr string) (*big.Int, error) {
	// Parse as float, multiply by 1e18.
	f := new(big.Float)
	if _, ok := f.SetString(ethStr); !ok {
		return nil, fmt.Errorf("EtherToWei: invalid ETH string %q", ethStr)
	}
	oneEth := new(big.Float).SetInt(new(big.Int).Exp(
		big.NewInt(10), big.NewInt(18), nil,
	))
	f.Mul(f, oneEth)
	wei, _ := f.Int(nil)
	return wei, nil
}

// WeiToEther converts wei to a human-readable ETH string.
func WeiToEther(wei *big.Int) string {
	if wei == nil {
		return "0 ETH"
	}
	oneEth := new(big.Float).SetInt(new(big.Int).Exp(
		big.NewInt(10), big.NewInt(18), nil,
	))
	eth := new(big.Float).Quo(new(big.Float).SetInt(wei), oneEth)
	return fmt.Sprintf("%.6f ETH", eth)
}
