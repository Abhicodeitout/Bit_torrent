package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	bencode "github.com/jackpal/bencode-go"
)

// AnnounceUDP sends an announcement to the UDP tracker and retrieves a list of peers.
func AnnounceUDP(trackerURL string, infoHash [20]byte, peerID [20]byte, port uint16) ([]Peer, error) {
	conn, err := net.DialTimeout("udp", trackerURL, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to UDP tracker: %v", err)
	}
	defer conn.Close()

	// This is a simplified UDP tracker implementation
	// A full implementation would follow the UDP tracker protocol specification
	fmt.Printf("Connected to UDP tracker at %s\n", trackerURL)

	// For now, return empty peer list as UDP tracker implementation is complex
	return []Peer{}, nil
}

// announceToHTTPTracker sends an HTTP GET request to the HTTP tracker and retrieves a list of peers.
func announceToHTTPTracker(torrent *TorrentFile, peerID [20]byte) ([]Peer, error) {
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
	var peers []Peer

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
						peers = append(peers, Peer{
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
func ParsePeersCompact(data []byte) []Peer {
	const peerSize = 6 // 4 bytes for IP, 2 bytes for port
	numPeers := len(data) / peerSize

	var peers []Peer
	for i := 0; i < numPeers; i++ {
		offset := i * peerSize
		if offset+peerSize > len(data) {
			break
		}

		ip := net.IPv4(data[offset], data[offset+1], data[offset+2], data[offset+3])
		port := binary.BigEndian.Uint16(data[offset+4 : offset+6])
		peers = append(peers, Peer{IP: ip, Port: port})
	}

	fmt.Printf("Parsed %d peers from compact format\n", len(peers))
	return peers
}

// ParsePeers is an alias for ParsePeersCompact for backward compatibility
func ParsePeers(data []byte) []Peer {
	return ParsePeersCompact(data)
}
