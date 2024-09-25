package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

// AnnounceUDP sends an announcement to the UDP tracker and retrieves a list of peers.
func AnnounceUDP(trackerURL string, infoHash [20]byte, peerID [20]byte, port uint16) ([]Peer, error) {
	conn, err := net.Dial("udp", trackerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to UDP tracker: %v", err)
	}
	defer conn.Close()

	// Prepare the announce request (this is simplified; refer to the actual protocol)
	request := []byte{} // Populate with appropriate request format

	_, err = conn.Write(request)
	if err != nil {
		return nil, fmt.Errorf("failed to send announce request: %v", err)
	}

	// Read the response from the tracker
	response := make([]byte, 1500)
	n, err := conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read response from UDP tracker: %v", err)
	}

	// Parse the peers from the response
	peers := ParsePeers(response[:n])
	return peers, nil
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
	}

	baseURL.RawQuery = params.Encode()

	resp, err := http.Get(baseURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to HTTP tracker: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP tracker response: %v", err)
	}

	// Parse peers from the HTTP response
	peers := ParsePeers(body)
	return peers, nil
}

// ParsePeers parses the compact peer list from the tracker response and returns a list of peers.
func ParsePeers(data []byte) []Peer {
	const peerSize = 6 // 4 bytes for IP, 2 bytes for port
	numPeers := len(data) / peerSize

	var peers []Peer
	for i := 0; i < numPeers; i++ {
		offset := i * peerSize
		ip := net.IPv4(data[offset], data[offset+1], data[offset+2], data[offset+3])
		port := binary.BigEndian.Uint16(data[offset+4 : offset+6])
		peers = append(peers, Peer{IP: ip, Port: port})
	}

	fmt.Printf("Parsed %d peers\n", len(peers))
	return peers
}
