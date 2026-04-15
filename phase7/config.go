// Package config loads and validates the poker node configuration.
// Settings come from (in priority order):
//  1. CLI flags (highest)
//  2. Environment variables (POKER_*)
//  3. config.yaml in the working directory
//  4. ~/.poker/config.yaml
//  5. Hard-coded defaults (lowest)
package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Config is the complete configuration for one poker node.
type Config struct {
	// ── Identity ───────────────────────────────────────────────────────────────
	PlayerName string `yaml:"player_name"` // display name shown at the table
	DataDir    string `yaml:"data_dir"`    // where keys and state are persisted

	// ── Network ────────────────────────────────────────────────────────────────
	Network NetworkConfig `yaml:"network"`

	// ── Game rules ─────────────────────────────────────────────────────────────
	Game GameConfig `yaml:"game"`

	// ── Fault tolerance ────────────────────────────────────────────────────────
	Fault FaultConfig `yaml:"fault"`

	// ── On-chain settlement ────────────────────────────────────────────────────
	Chain ChainConfig `yaml:"chain"`
}

// NetworkConfig controls the libp2p layer.
type NetworkConfig struct {
	ListenAddr      string   `yaml:"listen_addr"`       // e.g. "/ip4/0.0.0.0/tcp/9000"
	BootstrapPeers  []string `yaml:"bootstrap_peers"`   // full multiaddrs for internet play
	EnableMDNS      bool     `yaml:"enable_mdns"`       // LAN auto-discovery
	MaxPeers        int      `yaml:"max_peers"`
}

// GameConfig controls table rules.
type GameConfig struct {
	TableID    string `yaml:"table_id"`    // unique table identifier
	MaxSeats   int    `yaml:"max_seats"`   // 2–9
	SmallBlind int64  `yaml:"small_blind"` // chips
	BigBlind   int64  `yaml:"big_blind"`   // chips
	BuyIn      int64  `yaml:"buy_in"`      // starting chip stack
	ActionTimeout time.Duration `yaml:"action_timeout"` // per-player decision window
}

// FaultConfig controls fault-tolerance thresholds.
type FaultConfig struct {
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `yaml:"heartbeat_timeout"`
	VoteExpiry        time.Duration `yaml:"vote_expiry"`
}

// ChainConfig controls the on-chain settlement layer.
type ChainConfig struct {
	Enabled         bool   `yaml:"enabled"`          // false = local play, no chain
	RPCURL          string `yaml:"rpc_url"`          // JSON-RPC endpoint
	ContractAddress string `yaml:"contract_address"` // deployed PokerEscrow address
	ChainID         int64  `yaml:"chain_id"`         // 31337=local, 11155111=Sepolia
	BuyInWei        string `yaml:"buy_in_wei"`       // wei amount for joinTable()
	GasLimit        uint64 `yaml:"gas_limit"`
	PrivateKeyHex   string `yaml:"private_key_hex"`  // hex-encoded ECDSA key (no 0x prefix)
}

// Default returns production-ready defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		PlayerName: "Player",
		DataDir:    filepath.Join(home, ".poker"),
		Network: NetworkConfig{
			ListenAddr: "/ip4/0.0.0.0/tcp/9000",
			EnableMDNS: true,
			MaxPeers:   20,
		},
		Game: GameConfig{
			TableID:       "default-table",
			MaxSeats:      6,
			SmallBlind:    5,
			BigBlind:      10,
			BuyIn:         1000,
			ActionTimeout: 45 * time.Second,
		},
		Fault: FaultConfig{
			HeartbeatInterval: 5 * time.Second,
			HeartbeatTimeout:  15 * time.Second,
			VoteExpiry:        30 * time.Second,
		},
		Chain: ChainConfig{
			Enabled:  false,
			RPCURL:   "http://127.0.0.1:8545",
			ChainID:  31337,
			GasLimit: 500_000,
		},
	}
}

// Validate checks that required fields are set and values are in range.
func (c *Config) Validate() error {
	if c.PlayerName == "" {
		return fmt.Errorf("player_name is required")
	}
	if c.Game.MaxSeats < 2 || c.Game.MaxSeats > 9 {
		return fmt.Errorf("max_seats must be 2–9, got %d", c.Game.MaxSeats)
	}
	if c.Game.SmallBlind <= 0 {
		return fmt.Errorf("small_blind must be positive")
	}
	if c.Game.BigBlind <= 0 || c.Game.BigBlind <= c.Game.SmallBlind {
		return fmt.Errorf("big_blind must be > small_blind")
	}
	if c.Game.BuyIn < c.Game.BigBlind*10 {
		return fmt.Errorf("buy_in must be at least 10x big_blind")
	}
	if c.Chain.Enabled {
		if c.Chain.RPCURL == "" {
			return fmt.Errorf("chain.rpc_url required when chain is enabled")
		}
		if c.Chain.ContractAddress == "" {
			return fmt.Errorf("chain.contract_address required when chain is enabled")
		}
	}
	return nil
}

// ECDSAPrivateKey decodes the hex private key from ChainConfig.
// Returns nil if PrivateKeyHex is empty (chain disabled or key not set).
func (c *ChainConfig) ECDSAPrivateKey() (*ecdsa.PrivateKey, error) {
	if c.PrivateKeyHex == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(c.PrivateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("ECDSAPrivateKey: decode hex: %w", err)
	}
	priv := new(ecdsa.PrivateKey)
	priv.Curve = elliptic.P256()
	priv.D = new(big.Int).SetBytes(b)
	priv.PublicKey.X, priv.PublicKey.Y = priv.Curve.ScalarBaseMult(b)
	return priv, nil
}

// GenerateECDSAKey creates a new random ECDSA key and returns it along with
// its hex encoding (suitable for storing in config).
func GenerateECDSAKey() (*ecdsa.PrivateKey, string, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("GenerateECDSAKey: %w", err)
	}
	hexKey := hex.EncodeToString(priv.D.Bytes())
	return priv, hexKey, nil
}

// EnsureDataDir creates the data directory if it doesn't exist.
func (c *Config) EnsureDataDir() error {
	return os.MkdirAll(c.DataDir, 0o700)
}

// IdentityKeyPath returns the path where the libp2p identity key is stored.
func (c *Config) IdentityKeyPath() string {
	return filepath.Join(c.DataDir, "identity.key")
}

// LoadIdentityKey reads a 64-byte Ed25519 private key seed from disk,
// or generates and saves a new one if the file doesn't exist.
func (c *Config) LoadIdentityKey() ([]byte, error) {
	path := c.IdentityKeyPath()
	b, err := os.ReadFile(path)
	if err == nil && len(b) == 64 {
		return b, nil
	}
	// Generate a new identity.
	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("LoadIdentityKey: generate: %w", err)
	}
	if err := c.EnsureDataDir(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		return nil, fmt.Errorf("LoadIdentityKey: write: %w", err)
	}
	return seed, nil
}

// DefaultYAML returns the default config.yaml content as a string.
// Write this to disk with: poker init
func DefaultYAML() string {
	return `# P2P Poker Node Configuration
# Generated by: poker init

player_name: "Player"
data_dir: "~/.poker"

network:
  listen_addr: "/ip4/0.0.0.0/tcp/9000"
  enable_mdns: true
  max_peers: 20
  # For internet play, add bootstrap peers:
  # bootstrap_peers:
  #   - "/ip4/1.2.3.4/tcp/9000/p2p/12D3KooW..."

game:
  table_id: "my-table"
  max_seats: 6
  small_blind: 5
  big_blind: 10
  buy_in: 1000
  action_timeout: 45s

fault:
  heartbeat_interval: 5s
  heartbeat_timeout: 15s
  vote_expiry: 30s

chain:
  enabled: false
  rpc_url: "http://127.0.0.1:8545"
  contract_address: ""
  chain_id: 31337
  gas_limit: 500000
  buy_in_wei: "10000000000000000"   # 0.01 ETH
  # private_key_hex: ""             # generate with: poker keygen
`
}
