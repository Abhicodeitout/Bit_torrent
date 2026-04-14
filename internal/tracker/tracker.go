package tracker

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/types"
)

// AnnounceRequest carries tracker announce lifecycle data.
type AnnounceRequest struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Event      string // "started", "completed", "stopped", or ""
}

type httpTrackerPeer struct {
	IP   string `bencode:"ip"`
	Port int64  `bencode:"port"`
}

type httpTrackerResponseCompact struct {
	FailureReason string `bencode:"failure reason"`
	Peers         string `bencode:"peers"`
}

type httpTrackerResponseList struct {
	FailureReason string            `bencode:"failure reason"`
	Peers         []httpTrackerPeer `bencode:"peers"`
}

func udpEventCode(event string) uint32 {
	switch event {
	case "completed":
		return 1
	case "started":
		return 2
	case "stopped":
		return 3
	default:
		return 0
	}
}

// AnnounceUDP implements the full UDP tracker protocol (BEP 15).
func AnnounceUDP(trackerURL string, infoHash [20]byte, peerID [20]byte, port uint16) ([]types.Peer, error) {
	return AnnounceUDPWithRequest(trackerURL, AnnounceRequest{
		InfoHash: infoHash,
		PeerID:   peerID,
		Port:     port,
		Event:    "started",
	})
}

// AnnounceUDPWithRequest implements UDP tracker announces with lifecycle events.
func AnnounceUDPWithRequest(trackerURL string, req AnnounceRequest) ([]types.Peer, error) {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return nil, fmt.Errorf("parse UDP tracker URL: %w", err)
	}
	host := u.Host
	if host == "" {
		return nil, fmt.Errorf("empty host in UDP tracker URL: %s", trackerURL)
	}

	conn, err := net.DialTimeout("udp", host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial UDP tracker %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second)) //nolint:errcheck

	// ── Connect request ────────────────────────────────────────────────────────
	var txID1 [4]byte
	rand.Read(txID1[:]) //nolint:errcheck

	connectReq := make([]byte, 16)
	binary.BigEndian.PutUint64(connectReq[0:8], 0x41727101980) // BEP 15 magic
	binary.BigEndian.PutUint32(connectReq[8:12], 0)            // action: connect
	copy(connectReq[12:16], txID1[:])

	if _, err := conn.Write(connectReq); err != nil {
		return nil, fmt.Errorf("send connect request: %w", err)
	}

	connectResp := make([]byte, 16)
	n, err := conn.Read(connectResp)
	if err != nil || n < 16 {
		return nil, fmt.Errorf("read connect response (n=%d): %w", n, err)
	}
	if binary.BigEndian.Uint32(connectResp[0:4]) != 0 {
		return nil, fmt.Errorf("unexpected action in connect response")
	}
	if !bytes.Equal(connectResp[4:8], txID1[:]) {
		return nil, fmt.Errorf("transaction ID mismatch in connect response")
	}
	connectionID := binary.BigEndian.Uint64(connectResp[8:16])

	// ── Announce request ───────────────────────────────────────────────────────
	var txID2 [4]byte
	rand.Read(txID2[:]) //nolint:errcheck

	announceReq := make([]byte, 98)
	binary.BigEndian.PutUint64(announceReq[0:8], connectionID)
	binary.BigEndian.PutUint32(announceReq[8:12], 1) // action: announce
	copy(announceReq[12:16], txID2[:])
	copy(announceReq[16:36], req.InfoHash[:])
	copy(announceReq[36:56], req.PeerID[:])
	binary.BigEndian.PutUint64(announceReq[56:64], uint64(req.Downloaded))
	binary.BigEndian.PutUint64(announceReq[64:72], uint64(req.Left))
	binary.BigEndian.PutUint64(announceReq[72:80], uint64(req.Uploaded))
	binary.BigEndian.PutUint32(announceReq[80:84], udpEventCode(req.Event))
	// bytes 84-87: IP = 0 (use default)
	rand.Read(announceReq[88:92])                              //nolint:errcheck  key (random)
	binary.BigEndian.PutUint32(announceReq[92:96], ^uint32(0)) // num_want: -1
	binary.BigEndian.PutUint16(announceReq[96:98], req.Port)

	if _, err := conn.Write(announceReq); err != nil {
		return nil, fmt.Errorf("send announce request: %w", err)
	}

	announceResp := make([]byte, 65536)
	n, err = conn.Read(announceResp)
	if err != nil {
		return nil, fmt.Errorf("read announce response: %w", err)
	}
	if n < 20 {
		return nil, fmt.Errorf("announce response too short: %d bytes", n)
	}

	action := binary.BigEndian.Uint32(announceResp[0:4])
	if action == 3 { // error
		msg := ""
		if n > 8 {
			msg = string(announceResp[8:n])
		}
		return nil, fmt.Errorf("tracker error: %s", msg)
	}
	if action != 1 {
		return nil, fmt.Errorf("unexpected announce action: %d", action)
	}
	if !bytes.Equal(announceResp[4:8], txID2[:]) {
		return nil, fmt.Errorf("transaction ID mismatch in announce response")
	}
	// bytes 8-11: interval   12-15: leechers   16-19: seeders
	peers := ParsePeersCompact(announceResp[20:n])
	fmt.Printf("UDP tracker %s: %d peers\n", host, len(peers))
	return peers, nil
}

// AnnounceToHTTPTracker sends an HTTP GET request to the HTTP tracker and retrieves a list of peers.
func AnnounceToHTTPTracker(torrent *types.TorrentFile, peerID [20]byte) ([]types.Peer, error) {
	return AnnounceToHTTPTrackerWithRequest(torrent, AnnounceRequest{
		InfoHash: torrent.InfoHash,
		PeerID:   peerID,
		Port:     6881,
		Left:     torrent.Info.Length,
		Event:    "started",
	})
}

// AnnounceToHTTPTrackerWithRequest sends an HTTP tracker announce with lifecycle fields.
func AnnounceToHTTPTrackerWithRequest(torrent *types.TorrentFile, req AnnounceRequest) ([]types.Peer, error) {
	baseURL, err := url.Parse(torrent.Announce)
	if err != nil {
		return nil, fmt.Errorf("invalid tracker URL: %v", err)
	}

	params := url.Values{
		"info_hash":  {string(req.InfoHash[:])},
		"peer_id":    {string(req.PeerID[:])},
		"port":       {fmt.Sprintf("%d", req.Port)},
		"uploaded":   {fmt.Sprintf("%d", req.Uploaded)},
		"downloaded": {fmt.Sprintf("%d", req.Downloaded)},
		"left":       {fmt.Sprintf("%d", req.Left)},
		"compact":    {"1"}, // Request compact peer list
	}
	if req.Event != "" {
		params.Set("event", req.Event)
	}

	baseURL.RawQuery = params.Encode()

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(baseURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to HTTP tracker: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tracker returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP tracker response: %v", err)
	}

	var peers []types.Peer

	// Try compact format first (most trackers when compact=1).
	var compactResp httpTrackerResponseCompact
	if err := bencode.Unmarshal(bytes.NewReader(body), &compactResp); err == nil {
		if compactResp.FailureReason != "" {
			return nil, fmt.Errorf("tracker error: %s", compactResp.FailureReason)
		}
		if compactResp.Peers != "" {
			peers = ParsePeersCompact([]byte(compactResp.Peers))
		}
	}

	if len(peers) == 0 {
		var listResp httpTrackerResponseList
		if err := bencode.Unmarshal(bytes.NewReader(body), &listResp); err != nil {
			return nil, fmt.Errorf("failed to parse tracker response: %v", err)
		}
		if listResp.FailureReason != "" {
			return nil, fmt.Errorf("tracker error: %s", listResp.FailureReason)
		}
		for _, p := range listResp.Peers {
			ip := net.ParseIP(p.IP)
			if ip == nil || p.Port <= 0 || p.Port > 65535 {
				continue
			}
			peers = append(peers, types.Peer{IP: ip, Port: uint16(p.Port)})
		}
	}

	fmt.Printf("Tracker returned %d peers\n", len(peers))
	return peers, nil
}

// ParsePeersCompact parses the compact peer list from the tracker response and returns a list of peers.
func ParsePeersCompact(data []byte) []types.Peer {
	const peerSize = 6 // 4 bytes for IP, 2 bytes for port
	numPeers := len(data) / peerSize

	var peers []types.Peer
	for i := 0; i < numPeers; i++ {
		offset := i * peerSize
		if offset+peerSize > len(data) {
			break
		}

		ip := net.IPv4(data[offset], data[offset+1], data[offset+2], data[offset+3])
		port := binary.BigEndian.Uint16(data[offset+4 : offset+6])
		peers = append(peers, types.Peer{IP: ip, Port: port})
	}

	fmt.Printf("Parsed %d peers from compact format\n", len(peers))
	return peers
}

// ParsePeers is an alias for ParsePeersCompact for backward compatibility
func ParsePeers(data []byte) []types.Peer {
	return ParsePeersCompact(data)
}

// GetPeers resolves peers from a single tracker URL using the appropriate
// protocol implementation. This keeps a stable API for components that only
// need "tracker URL in, peers out" behavior.
func GetPeers(trackerURL string, infoHash [20]byte, peerID [20]byte, torrent *types.TorrentFile) ([]types.Peer, error) {
	switch {
	case trackerURL == "":
		return nil, fmt.Errorf("empty tracker URL")
	case len(infoHash) == 0:
		return nil, fmt.Errorf("invalid info hash")
	case torrent == nil:
		return nil, fmt.Errorf("nil torrent file")
	case strings.HasPrefix(trackerURL, "udp://"):
		return AnnounceUDPWithRequest(trackerURL, AnnounceRequest{
			InfoHash: infoHash,
			PeerID:   peerID,
			Port:     6881,
			Left:     torrent.Info.Length,
			Event:    "started",
		})
	case strings.HasPrefix(trackerURL, "http://"):
		tfCopy := *torrent
		tfCopy.Announce = trackerURL
		return AnnounceToHTTPTrackerWithRequest(&tfCopy, AnnounceRequest{
			InfoHash: infoHash,
			PeerID:   peerID,
			Port:     6881,
			Left:     torrent.Info.Length,
			Event:    "started",
		})
	case strings.HasPrefix(trackerURL, "https://"):
		tfCopy := *torrent
		tfCopy.Announce = trackerURL
		return AnnounceToHTTPTrackerWithRequest(&tfCopy, AnnounceRequest{
			InfoHash: infoHash,
			PeerID:   peerID,
			Port:     6881,
			Left:     torrent.Info.Length,
			Event:    "started",
		})
	default:
		return nil, fmt.Errorf("unsupported tracker scheme in %q", trackerURL)
	}
}
