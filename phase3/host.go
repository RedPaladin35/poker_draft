package network

import (
	"context"
	"fmt"

	libp2p "github.com/libp2p/go-libp2p"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"

	"crypto/ed25519"
)

// PokerHost wraps a libp2p host with poker-specific identity helpers.
// One PokerHost exists per player per table session.
type PokerHost struct {
	Host      host.Host
	Ed25519PK ed25519.PrivateKey // raw Ed25519 private key used to sign Envelopes
	PeerID    string             // base58-encoded libp2p PeerID
}

// NewPokerHost creates a libp2p host bound to the given listen address.
//
// listenAddr examples:
//   - "/ip4/0.0.0.0/tcp/0"       — random available port on all interfaces
//   - "/ip4/127.0.0.1/tcp/9000"  — fixed port, loopback only (for tests)
//
// seed, if non-nil and exactly 64 bytes, is used as a deterministic Ed25519
// private key (useful for persistent player identity and tests).
// Pass nil to generate a fresh random key each time.
//
// Transport stack:
//   - TCP transport
//   - Noise protocol for encrypted, authenticated transport
//   - No relay (direct connections only — relay is a Phase 7 concern)
func NewPokerHost(ctx context.Context, listenAddr string, seed []byte) (*PokerHost, error) {
	var libPrivKey libp2pcrypto.PrivKey
	var rawEd ed25519.PrivateKey
	var err error

	if len(seed) == 64 {
		// Deterministic identity from a 64-byte Ed25519 seed (private key).
		rawEd = ed25519.PrivateKey(seed)
		libPrivKey, _, err = libp2pcrypto.KeyPairFromStdKey(rawEd)
		if err != nil {
			return nil, fmt.Errorf("NewPokerHost: key from seed: %w", err)
		}
	} else {
		// Fresh random Ed25519 key.
		libPrivKey, _, err = libp2pcrypto.GenerateEd25519Key(nil)
		if err != nil {
			return nil, fmt.Errorf("NewPokerHost: keygen: %w", err)
		}
		raw, err := libPrivKey.Raw()
		if err != nil {
			return nil, fmt.Errorf("NewPokerHost: raw key: %w", err)
		}
		rawEd = ed25519.PrivateKey(raw)
	}

	maddr, err := ma.NewMultiaddr(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("NewPokerHost: parse multiaddr %q: %w", listenAddr, err)
	}

	h, err := libp2p.New(
		libp2p.Identity(libPrivKey),
		libp2p.ListenAddrs(maddr),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.DisableRelay(),
		libp2p.NATPortMap(), // attempt UPnP port mapping for internet play
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

// Connect establishes a connection to a peer given their full multiaddr string.
// The addr must include the /p2p/<peerID> suffix.
//
// Example: "/ip4/192.168.1.10/tcp/9001/p2p/12D3KooWAbc..."
func (ph *PokerHost) Connect(ctx context.Context, addrStr string) error {
	maddr, err := ma.NewMultiaddr(addrStr)
	if err != nil {
		return fmt.Errorf("Connect: parse addr %q: %w", addrStr, err)
	}
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("Connect: extract peer info: %w", err)
	}
	ph.Host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, peerstore.PermanentAddrTTL)
	if err := ph.Host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("Connect to %s: %w", peerInfo.ID, err)
	}
	return nil
}

// Addrs returns the full multiaddrs of this host, each including the
// /p2p/<peerID> suffix so peers can connect directly.
func (ph *PokerHost) Addrs() []string {
	suffix := fmt.Sprintf("/p2p/%s", ph.Host.ID())
	out := make([]string, 0, len(ph.Host.Addrs()))
	for _, addr := range ph.Host.Addrs() {
		out = append(out, addr.String()+suffix)
	}
	return out
}

// ConnectedPeers returns the IDs of all currently connected peers.
func (ph *PokerHost) ConnectedPeers() []peer.ID {
	return ph.Host.Network().Peers()
}

// Close shuts down the libp2p host gracefully.
func (ph *PokerHost) Close() error {
	return ph.Host.Close()
}
