package peer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"torrent-client/internal/dht"
	"torrent-client/internal/tracker"
	"torrent-client/internal/types"
)

// Manager handles continuous peer discovery and peer pool management.
type Manager struct {
	infoHash      [20]byte
	trackerURLs   []string
	peerID        [20]byte
	torrentFile   *types.TorrentFile
	
	peers         map[string]types.Peer // key: "IP:Port"
	mu            sync.RWMutex
	
	lastTrackerCheck time.Time
	trackerInterval  time.Duration
}

// NewManager creates a new peer manager.
func NewManager(infoHash [20]byte, trackerURLs []string, peerID [20]byte, torrentFile *types.TorrentFile) *Manager {
	return &Manager{
		infoHash:       infoHash,
		trackerURLs:    trackerURLs,
		peerID:         peerID,
		torrentFile:    torrentFile,
		peers:          make(map[string]types.Peer),
		trackerInterval: 30 * time.Minute, // Default interval
	}
}

// Start begins continuous peer discovery.
func (pm *Manager) Start(ctx context.Context) {
	go pm.backgroundDiscovery(ctx)
}

// backgroundDiscovery runs in the background to discover new peers.
func (pm *Manager) backgroundDiscovery(ctx context.Context) {
	// Initial discovery
	pm.discoverFromTrackers()
	pm.discoverFromDHT()
	
	// Periodic discovery
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.discoverFromTrackers()
			// DHT discovery less frequently
			if time.Since(pm.lastTrackerCheck) > 30*time.Minute {
				pm.discoverFromDHT()
			}
		}
	}
}

// discoverFromTrackers queries tracker(s) for peers.
func (pm *Manager) discoverFromTrackers() {
	if len(pm.trackerURLs) == 0 {
		return
	}
	
	// Query the first tracker (or implement tier based discovery)
	trackerURL := pm.trackerURLs[0]
	peers, err := tracker.GetPeers(trackerURL, pm.infoHash, pm.peerID, pm.torrentFile)
	if err != nil {
		fmt.Printf("Tracker discovery failed: %v\n", err)
		return
	}
	
	pm.mu.Lock()
	for _, peer := range peers {
		key := fmt.Sprintf("%s:%d", peer.IP, peer.Port)
		pm.peers[key] = peer
	}
	pm.mu.Unlock()
	
	pm.lastTrackerCheck = time.Now()
	fmt.Printf("Discovered %d peers from tracker\n", len(peers))
}

// discoverFromDHT queries DHT for peers.
func (pm *Manager) discoverFromDHT() {
	dhtPeers, err := dht.GetPeers(pm.infoHash, 50, 45*time.Second)
	if err != nil {
		fmt.Printf("DHT discovery failed: %v\n", err)
		return
	}
	
	pm.mu.Lock()
	for _, peer := range dhtPeers {
		key := fmt.Sprintf("%s:%d", peer.IP, peer.Port)
		pm.peers[key] = peer
	}
	pm.mu.Unlock()
	
	fmt.Printf("Discovered %d peers from DHT\n", len(dhtPeers))
}

// GetPeers returns a copy of available peers.
func (pm *Manager) GetPeers() []types.Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	peers := make([]types.Peer, 0, len(pm.peers))
	for _, peer := range pm.peers {
		peers = append(peers, peer)
	}
	return peers
}

// AddPeer adds a single peer to the pool.
func (pm *Manager) AddPeer(peer types.Peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	key := fmt.Sprintf("%s:%d", peer.IP, peer.Port)
	pm.peers[key] = peer
}

// GetPeerCount returns the number of known peers.
func (pm *Manager) GetPeerCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.peers)
}
