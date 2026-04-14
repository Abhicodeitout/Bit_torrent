package choke

import (
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"torrent-client/internal/types"
)

// PeerStats tracks upload/download statistics for a specific peer.
type PeerStats struct {
	Peer            types.Peer
	BytesUploaded   int64
	BytesDownloaded int64
	LastActive      time.Time
	TotalUploaded   int64
	Choked          bool
}

// UnchokeRound manages one round of unchoking decisions.
type UnchokeRound struct {
	MaxUnchoked        int // typically 4
	OptimisticInterval int // rounds between optimistic unchokes (typically 3)
	Mu                 sync.RWMutex
	peers              map[string]*PeerStats
	roundNumber        int
	lastOptimistic     string // peer ID of last optimistically unchoked peer
}

// NewUnchokeRound creates a new unchoke round manager.
func NewUnchokeRound(maxUnchoked int) *UnchokeRound {
	return &UnchokeRound{
		MaxUnchoked:        maxUnchoked,
		OptimisticInterval: 3,
		peers:              make(map[string]*PeerStats),
	}
}

// RegisterPeer registers a peer for tracking.
func (ur *UnchokeRound) RegisterPeer(peer types.Peer) {
	ur.Mu.Lock()
	defer ur.Mu.Unlock()

	key := peerKey(peer)
	if _, exists := ur.peers[key]; !exists {
		ur.peers[key] = &PeerStats{
			Peer:       peer,
			LastActive: time.Now(),
			Choked:     true,
		}
	}
}

// RemovePeer unregisters a peer from tracking.
func (ur *UnchokeRound) RemovePeer(peer types.Peer) {
	ur.Mu.Lock()
	defer ur.Mu.Unlock()

	key := peerKey(peer)
	delete(ur.peers, key)
}

// RecordUpload records bytes uploaded to a peer.
func (ur *UnchokeRound) RecordUpload(peer types.Peer, bytes int64) {
	ur.Mu.Lock()
	defer ur.Mu.Unlock()

	key := peerKey(peer)
	if stats, exists := ur.peers[key]; exists {
		stats.BytesUploaded += bytes
		stats.TotalUploaded += bytes
		stats.LastActive = time.Now()
	}
}

// RecordDownload records bytes downloaded from a peer.
func (ur *UnchokeRound) RecordDownload(peer types.Peer, bytes int64) {
	ur.Mu.Lock()
	defer ur.Mu.Unlock()

	key := peerKey(peer)
	if stats, exists := ur.peers[key]; exists {
		stats.BytesDownloaded += bytes
		stats.LastActive = time.Now()
	}
}

// DecideUnchokes runs a round of unchoke decisions.
// Returns slices of peers to unchoke and choke.
func (ur *UnchokeRound) DecideUnchokes() (toUnchoke []types.Peer, toChoke []types.Peer) {
	ur.Mu.Lock()
	defer ur.Mu.Unlock()

	ur.roundNumber++

	// Collect all peers with data
	var activePeers []*PeerStats

	for _, stats := range ur.peers {
		activePeers = append(activePeers, stats)
	}

	// If no active peers, nothing to do
	if len(activePeers) == 0 {
		return nil, nil
	}
	if ur.MaxUnchoked < 0 {
		ur.MaxUnchoked = 0
	}

	// Sort by download volume (tit-for-tat: prefer peers that upload more to us).
	sortPeersByDownloadRate(activePeers)

	// Select peers to unchoke (strictly capped at MaxUnchoked).
	unchoked := make(map[string]bool)
	limit := ur.MaxUnchoked
	if limit > len(activePeers) {
		limit = len(activePeers)
	}

	// First, choose the top performers.
	for i := 0; i < limit; i++ {
		stats := activePeers[i]
		key := peerKey(stats.Peer)
		unchoked[key] = true
	}

	// Optimistic unchoking: occasionally rotate one slot to an unexplored peer.
	if limit > 0 && ur.OptimisticInterval > 0 && ur.roundNumber%ur.OptimisticInterval == 0 && len(activePeers) > limit {
		optimisticIdx := -1
		for i := limit; i < len(activePeers); i++ {
			stats := activePeers[i]
			key := peerKey(stats.Peer)
			if key == ur.lastOptimistic {
				continue
			}
			optimisticIdx = i
			break
		}
		if optimisticIdx == -1 {
			optimisticIdx = limit
		}

		// Replace the current worst selected peer to keep unchoked count <= limit.
		worstSelected := activePeers[limit-1]
		delete(unchoked, peerKey(worstSelected.Peer))

		optimisticPeer := activePeers[optimisticIdx]
		okey := peerKey(optimisticPeer.Peer)
		unchoked[okey] = true
		ur.lastOptimistic = okey
	} else if limit == 0 {
		ur.lastOptimistic = ""
	}

	// Determine which peers to unchoke/choke
	for _, stats := range ur.peers {
		key := peerKey(stats.Peer)
		shouldBeUnchoked := unchoked[key]

		if shouldBeUnchoked && stats.Choked {
			// Currently choked but should be unchoked
			toUnchoke = append(toUnchoke, stats.Peer)
			stats.Choked = false
		} else if !shouldBeUnchoked && !stats.Choked {
			// Currently unchoked but should be choked
			toChoke = append(toChoke, stats.Peer)
			stats.Choked = true
		}
	}

	// Reset byte counters for next round
	for _, stats := range ur.peers {
		stats.BytesUploaded = 0
		stats.BytesDownloaded = 0
	}

	return toUnchoke, toChoke
}

// GetPeerStats returns statistics for a specific peer.
func (ur *UnchokeRound) GetPeerStats(peer types.Peer) *PeerStats {
	ur.Mu.RLock()
	defer ur.Mu.RUnlock()

	key := peerKey(peer)
	return ur.peers[key]
}

func peerKey(peer types.Peer) string {
	return net.JoinHostPort(peer.IP.String(), strconv.Itoa(int(peer.Port)))
}

// GetAllStats returns statistics for all peers.
func (ur *UnchokeRound) GetAllStats() []*PeerStats {
	ur.Mu.RLock()
	defer ur.Mu.RUnlock()

	var stats []*PeerStats
	for _, s := range ur.peers {
		stats = append(stats, s)
	}
	return stats
}

// sortPeersByDownloadRate sorts peers by how much they've downloaded (uploaded to us)
// in descending order. Higher download = higher priority to unchoke.
func sortPeersByDownloadRate(peers []*PeerStats) {
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].BytesDownloaded != peers[j].BytesDownloaded {
			return peers[i].BytesDownloaded > peers[j].BytesDownloaded
		}
		if peers[i].TotalUploaded != peers[j].TotalUploaded {
			return peers[i].TotalUploaded > peers[j].TotalUploaded
		}
		return peers[i].LastActive.After(peers[j].LastActive)
	})
}
