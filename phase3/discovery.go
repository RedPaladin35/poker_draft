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

// PokerServiceTag is the mDNS service name for local discovery.
// All nodes running a poker table on the same LAN will find each other.
const PokerServiceTag = "p2p-poker-v1"

// PeerFoundFunc is called whenever a new peer is discovered.
type PeerFoundFunc func(peer.AddrInfo)

// MDNSDiscovery implements local-network peer discovery via mDNS.
// When running on a LAN (development, home game), peers advertise
// themselves and connect automatically.
//
// For internet play (Phase 7), swap this for a DHT-based discoverer
// using go-libp2p-kad-dht — the interface is identical.
type MDNSDiscovery struct {
	h       host.Host
	svc     mdns.Service
	mu      sync.Mutex
	found   []peer.AddrInfo
	onFound PeerFoundFunc
}

// mdnsNotifee implements mdns.Notifee — called when a peer is found.
type mdnsNotifee struct {
	disc *MDNSDiscovery
}

func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.disc.mu.Lock()
	n.disc.found = append(n.disc.found, pi)
	cb := n.disc.onFound
	n.disc.mu.Unlock()
	if cb != nil {
		cb(pi)
	}
}

// NewMDNSDiscovery starts an mDNS discovery service on the given host.
// onFound is called (in a separate goroutine) each time a peer is discovered.
func NewMDNSDiscovery(h host.Host, onFound PeerFoundFunc) (*MDNSDiscovery, error) {
	disc := &MDNSDiscovery{h: h, onFound: onFound}
	svc := mdns.NewMdnsService(h, PokerServiceTag, &mdnsNotifee{disc: disc})
	if err := svc.Start(); err != nil {
		return nil, fmt.Errorf("NewMDNSDiscovery: %w", err)
	}
	disc.svc = svc
	return disc, nil
}

// Peers returns all discovered peers so far.
func (d *MDNSDiscovery) Peers() []peer.AddrInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]peer.AddrInfo, len(d.found))
	copy(out, d.found)
	return out
}

// WaitForPeers blocks until at least n peers have been discovered or the
// context is cancelled.  Useful in lobby / test synchronisation.
func (d *MDNSDiscovery) WaitForPeers(ctx context.Context, n int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
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
	return d.svc.Close()
}
