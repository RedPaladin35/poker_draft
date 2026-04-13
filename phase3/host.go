package network

import (
	"context"
	"crypto/ed25519"
	"fmt"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
)

// PokerHost wraps a libp2p host with poker-specific identity helpers.
type PokerHost struct {
	Host      host.Host
	Ed25519PK ed25519.PrivateKey // raw Ed25519 key for envelope signing
	PeerID    string             // base58-encoded PeerID
}

// NewPokerHost creates a libp2p host bound to the given listen address.
// listenAddr examples: "/ip4/0.0.0.0/tcp/0" (random port), "/ip4/127.0.0.1/tcp/9000"
//
// The host uses:
//   - Ed25519 identity (deterministic from a seed, or fresh if seed is nil)
//   - Noise protocol for encrypted transport
//   - TCP transport
//   - No mDNS yet — discovery is added separately in discovery.go
func NewPokerHost(ctx context.Context, listenAddr string, seed []byte) (*PokerHost, error) {
	// Generate or derive an Ed25519 identity.
	var privKey crypto.PrivKey
	var rawEd ed25519.PrivateKey
	var err error

	if len(seed) == 64 {
		// Deterministic key from seed (useful for tests and persistent identity).
		rawEd = ed25519.PrivateKey(seed)
		privKey, _, err = crypto.KeyPairFromStdKey(rawEd)
		if err != nil {
			return nil, fmt.Errorf("NewPokerHost: key from seed: %w", err)
		}
	} else {
		// Fresh random key.
		privKey, _, err = crypto.GenerateEd25519Key(nil)
		if err != nil {
			return nil, fmt.Errorf("NewPokerHost: keygen: %w", err)
		}
		raw, _ := privKey.Raw()
		rawEd = ed25519.PrivateKey(raw)
	}

	// Parse listen address.
	maddr, err := ma.NewMultiaddr(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("NewPokerHost: multiaddr: %w", err)
	}

	h, err := libp2p.New(
		libp2p.Identity(privKey),
		libp2p.ListenAddrs(maddr),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.DisableRelay(),
	)
	if err != nil {
		return nil, fmt.Errorf("NewPokerHost: libp2p.New: %w", err)
	}

	return &PokerHost{
		Host:      h,
		Ed25519PK: rawEd,
		PeerID:    h.ID().String(),
	}, nil
}

// Connect establishes a connection to the given peer multiaddr string.
// Example: "/ip4/127.0.0.1/tcp/9001/p2p/12D3KooW..."
func (ph *PokerHost) Connect(ctx context.Context, addrStr string) error {
	maddr, err := ma.NewMultiaddr(addrStr)
	if err != nil {
		return fmt.Errorf("Connect: parse addr: %w", err)
	}
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("Connect: peer info: %w", err)
	}
	ph.Host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, peerstore.PermanentAddrTTL)
	if err := ph.Host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("Connect to %s: %w", peerInfo.ID, err)
	}
	return nil
}

// Addrs returns the full multiaddrs of this host (including PeerID suffix).
func (ph *PokerHost) Addrs() []string {
	hostAddr := fmt.Sprintf("/p2p/%s", ph.Host.ID())
	var out []string
	for _, addr := range ph.Host.Addrs() {
		out = append(out, addr.String()+hostAddr)
	}
	return out
}

// Close shuts down the libp2p host.
func (ph *PokerHost) Close() error {
	return ph.Host.Close()
}
