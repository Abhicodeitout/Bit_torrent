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
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/types"
)

// AnnounceUDP implements the full UDP tracker protocol (BEP 15).
func AnnounceUDP(trackerURL string, infoHash [20]byte, peerID [20]byte, port uint16) ([]types.Peer, error) {
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
	copy(announceReq[16:36], infoHash[:])
	copy(announceReq[36:56], peerID[:])
	// bytes 56-79: downloaded(8), left(8), uploaded(8) — all zero
	binary.BigEndian.PutUint32(announceReq[80:84], 2) // event: started
	// bytes 84-87: IP = 0 (use default)
	rand.Read(announceReq[88:92])                              //nolint:errcheck  key (random)
	binary.BigEndian.PutUint32(announceReq[92:96], ^uint32(0)) // num_want: -1
	binary.BigEndian.PutUint16(announceReq[96:98], port)

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
	baseURL, err := url.Parse(torrent.Announce)
	if err != nil {
		return nil, fmt.Errorf("invalid tracker URL: %v", err)
	}

	params := url.Values{
		"info_hash":  {string(torrent.InfoHash[:])},
		"peer_id":    {string(peerID[:])},
		"port":       {"6881"},
		"uploaded":   {"0"},
		"downloaded": {"0"},
		"left":       {fmt.Sprintf("%d", torrent.Info.Length)},
		"compact":    {"1"}, // Request compact peer list
		"event":      {"started"},
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

	// Parse the bencode response
	var response map[string]interface{}
	reader := bytes.NewReader(body)
	err = bencode.Unmarshal(reader, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracker response: %v", err)
	}

	// Check for error message
	if errorMsg, ok := response["failure reason"].(string); ok {
		return nil, fmt.Errorf("tracker error: %s", errorMsg)
	}

	// Parse peers
	var peers []types.Peer

	// Try compact format first
	if peersStr, ok := response["peers"].(string); ok {
		peers = ParsePeersCompact([]byte(peersStr))
	} else if peersList, ok := response["peers"].([]interface{}); ok {
		// Dictionary format
		for _, p := range peersList {
			if peerMap, ok := p.(map[string]interface{}); ok {
				if ipStr, ok := peerMap["ip"].(string); ok {
					if port, ok := peerMap["port"].(int64); ok {
						ip := net.ParseIP(ipStr)
						peers = append(peers, types.Peer{
							IP:   ip,
							Port: uint16(port),
						})
					}
				}
			}
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
