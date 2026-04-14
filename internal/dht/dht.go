// Package dht implements the BitTorrent DHT protocol (BEP 5) for peer discovery
// on magnets that carry no tracker URLs.
package dht

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/types"
)

const (
	// alpha is the Kademlia concurrency parameter: queries in flight per round.
	alpha = 3
	// maxCandidates caps the working set to avoid unbounded memory growth.
	maxCandidates = 200
)

// bootstrapAddrs are the well-known DHT bootstrap nodes operated by BitTorrent Inc
// and the Transmission project. They are always reachable and return good node lists.
var bootstrapAddrs = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.aelitis.com:6881",
}

// nodeID is a 160-bit Kademlia node identifier.
type nodeID [20]byte

func newNodeID() nodeID {
	var id nodeID
	rand.Read(id[:]) //nolint:errcheck
	return id
}

// xorDist computes the XOR distance between two node IDs.
func xorDist(a, b nodeID) [20]byte {
	var d [20]byte
	for i := range d {
		d[i] = a[i] ^ b[i]
	}
	return d
}

// candidate is a DHT node that may be queried during an iterative lookup.
type candidate struct {
	id      nodeID
	addr    string // "ip:port"
	dist    [20]byte
	queried bool
}

// candidateList implements sort.Interface, ordering by XOR distance.
type candidateList []*candidate

func (l candidateList) Len() int      { return len(l) }
func (l candidateList) Swap(i, j int) { l[i], l[j] = l[j], l[i] }
func (l candidateList) Less(i, j int) bool {
	return bytes.Compare(l[i].dist[:], l[j].dist[:]) < 0
}

// GetPeers performs an iterative DHT get_peers lookup (BEP 5). It contacts
// the public bootstrap nodes, follows the closest-node chain towards infoHash,
// and returns whatever peers it discovers within the given timeout.
func GetPeers(infoHash [20]byte, maxPeers int, timeout time.Duration) ([]types.Peer, error) {
	localID := newNodeID()
	target := nodeID(infoHash)
	deadline := time.Now().Add(timeout)

	var mu sync.Mutex
	seenAddrs := make(map[string]bool)
	seenPeers := make(map[string]bool)
	var peers []types.Peer
	var candidates candidateList

	// addNodes inserts new DHT nodes into the candidate list, sorted by distance.
	addNodes := func(nodes []candidate) {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range nodes {
			if seenAddrs[n.addr] {
				continue
			}
			seenAddrs[n.addr] = true
			c := &candidate{
				id:   n.id,
				addr: n.addr,
				dist: xorDist(n.id, target),
			}
			candidates = append(candidates, c)
		}
		sort.Sort(candidates)
		if len(candidates) > maxCandidates {
			candidates = candidates[:maxCandidates]
		}
	}

	// addPeers deduplicates and appends newly discovered download peers.
	addPeers := func(pp []types.Peer) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range pp {
			key := net.JoinHostPort(p.IP.String(), fmt.Sprintf("%d", p.Port))
			if !seenPeers[key] {
				seenPeers[key] = true
				peers = append(peers, p)
			}
		}
	}

	// Seed with bootstrap nodes (IDs unknown — assign random so they get sorted).
	{
		var seeds []candidate
		for _, addr := range bootstrapAddrs {
			var id nodeID
			rand.Read(id[:]) //nolint:errcheck
			seeds = append(seeds, candidate{id: id, addr: addr})
		}
		addNodes(seeds)
	}

	// Iterative lookup: each round queries alpha unqueried candidates in parallel.
	for time.Now().Before(deadline) {
		mu.Lock()
		numPeers := len(peers)
		var batch []*candidate
		for _, c := range candidates {
			if !c.queried {
				c.queried = true
				batch = append(batch, c)
				if len(batch) >= alpha {
					break
				}
			}
		}
		mu.Unlock()

		if len(batch) == 0 || numPeers >= maxPeers {
			break
		}

		var wg sync.WaitGroup
		for _, c := range batch {
			wg.Add(1)
			go func(c *candidate) {
				defer wg.Done()
				pp, nn, err := krpcGetPeers(c.addr, localID, infoHash, 8*time.Second)
				if err != nil {
					return
				}
				addPeers(pp)
				addNodes(nn)
			}(c)
		}
		wg.Wait()
	}

	mu.Lock()
	result := make([]types.Peer, len(peers))
	copy(result, peers)
	mu.Unlock()

	if len(result) == 0 {
		return nil, fmt.Errorf("DHT: no peers found for %x", infoHash)
	}
	fmt.Printf("DHT: discovered %d peers\n", len(result))
	return result, nil
}

// krpcGetPeers sends a single BEP 5 get_peers query over UDP and returns any
// peers (values) and/or closer nodes (nodes) from the response.
func krpcGetPeers(addr string, localID nodeID, infoHash [20]byte, timeout time.Duration) ([]types.Peer, []candidate, error) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", addr, err)
	}

	conn, err := net.DialUDP("udp4", nil, udpAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	// Build a 2-byte transaction ID so we can detect mismatched responses.
	var txID [2]byte
	rand.Read(txID[:]) //nolint:errcheck

	query := map[string]interface{}{
		"t": string(txID[:]),
		"y": "q",
		"q": "get_peers",
		"a": map[string]interface{}{
			"id":        string(localID[:]),
			"info_hash": string(infoHash[:]),
		},
	}

	var buf bytes.Buffer
	if err := bencode.Marshal(&buf, query); err != nil {
		return nil, nil, fmt.Errorf("marshal KRPC query: %w", err)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return nil, nil, fmt.Errorf("write KRPC query to %s: %w", addr, err)
	}

	// Read one UDP datagram (max DHT message fits in 65535 bytes).
	resp := make([]byte, 65535)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("read KRPC response from %s: %w", addr, err)
	}

	var msg map[string]interface{}
	if err := bencode.Unmarshal(bytes.NewReader(resp[:n]), &msg); err != nil {
		return nil, nil, fmt.Errorf("unmarshal KRPC response: %w", err)
	}

	// Detect error responses.
	if y, _ := msg["y"].(string); y == "e" {
		return nil, nil, fmt.Errorf("KRPC error response from %s", addr)
	}

	r, ok := msg["r"].(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("KRPC response from %s missing r field", addr)
	}

	// ── Parse peers (values) ──────────────────────────────────────────────────
	var peers []types.Peer
	if values, ok := r["values"].([]interface{}); ok {
		for _, v := range values {
			if s, ok := v.(string); ok && len(s) == 6 {
				b := []byte(s)
				ip := net.IPv4(b[0], b[1], b[2], b[3])
				port := binary.BigEndian.Uint16(b[4:6])
				peers = append(peers, types.Peer{IP: ip, Port: port})
			}
		}
	}

	// ── Parse closer nodes ────────────────────────────────────────────────────
	var nodes []candidate
	if nodesStr, ok := r["nodes"].(string); ok {
		data := []byte(nodesStr)
		for i := 0; i+26 <= len(data); i += 26 {
			var id nodeID
			copy(id[:], data[i:i+20])
			ip := net.IPv4(data[i+20], data[i+21], data[i+22], data[i+23])
			port := binary.BigEndian.Uint16(data[i+24 : i+26])
			if port == 0 {
				continue
			}
			nodeAddr := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
			nodes = append(nodes, candidate{id: id, addr: nodeAddr})
		}
	}

	return peers, nodes, nil
}
