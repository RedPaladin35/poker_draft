package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

// PokerServiceTag is the mDNS service name used for local peer discovery.
// All nodes on the same LAN advertising this tag will find each other
// automatically without needing a bootstrap address.
const PokerServiceTag = "p2p-poker-v1"

// PeerFoundFunc is called (in its own goroutine) whenever a new peer is found.
type PeerFoundFunc func(peer.AddrInfo)

// MDNSDiscovery wraps libp2p mDNS discovery for LAN play.
//
// For internet play (Phase 7), replace this with a DHT-based discoverer
// using go-libp2p-kad-dht — the PeerFoundFunc interface is identical.
type MDNSDiscovery struct {
	h       host.Host
	svc     mdns.Service
	mu      sync.Mutex
	found   []peer.AddrInfo
	onFound PeerFoundFunc
}

// mdnsNotifee implements mdns.Notifee.
type mdnsNotifee struct {
	disc *MDNSDiscovery
}

func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.disc.mu.Lock()
	n.disc.found = append(n.disc.found, pi)
	cb := n.disc.onFound
	n.disc.mu.Unlock()
	if cb != nil {
		go cb(pi)
	}
}

// NewMDNSDiscovery starts an mDNS discovery service on the given host.
// onFound is called (in a goroutine) each time a new peer is discovered.
func NewMDNSDiscovery(h host.Host, onFound PeerFoundFunc) (*MDNSDiscovery, error) {
	disc := &MDNSDiscovery{h: h, onFound: onFound}
	svc := mdns.NewMdnsService(h, PokerServiceTag, &mdnsNotifee{disc: disc})
	if err := svc.Start(); err != nil {
		return nil, fmt.Errorf("NewMDNSDiscovery: start: %w", err)
	}
	disc.svc = svc
	return disc, nil
}

// Peers returns a snapshot of all peers discovered so far.
func (d *MDNSDiscovery) Peers() []peer.AddrInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]peer.AddrInfo, len(d.found))
	copy(out, d.found)
	return out
}

// WaitForPeers blocks until at least n peers have been discovered,
// or the context is cancelled. Used in lobby and test synchronisation.
func (d *MDNSDiscovery) WaitForPeers(ctx context.Context, n int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("WaitForPeers: %w", ctx.Err())
		case <-ticker.C:
			d.mu.Lock()
			count := len(d.found)
			d.mu.Unlock()
			if count >= n {
				return nil
			}
		}
	}
}

// Close stops the mDNS service.
func (d *MDNSDiscovery) Close() error {
	if d.svc != nil {
		return d.svc.Close()
	}
	return nil
}
