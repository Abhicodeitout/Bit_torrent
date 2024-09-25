package main

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
)

// generatePeerID creates a unique 20-byte peer ID for the client.
func generatePeerID() [20]byte {
	var peerID [20]byte
	rand.Read(peerID[:])
	copy(peerID[:8], []byte("-GO0001-")) // Custom client identifier
	return peerID
}

// OpenTorrentFile opens and parses the .torrent file.
func OpenTorrentFile(filePath string) (*TorrentFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open torrent file: %v", err)
	}
	defer file.Close()

	var torrent TorrentFile

	// Basic parsing logic (this needs to be expanded for actual .torrent structure)
	torrent.Announce = "http://example.com/announce"                // Placeholder
	copy(torrent.InfoHash[:], []byte("samplehash1234567890abcdef")) // Placeholder
	torrent.Info.Length = 1024                                      // Placeholder length

	return &torrent, nil
}

// ConnectToPeer establishes a connection to the specified peer.
func ConnectToPeer(peer Peer) error {
	address := fmt.Sprintf("%s:%d", peer.IP.String(), peer.Port)
	fmt.Printf("Attempting to connect to peer: %s\n", address)

	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Printf("Failed to connect to peer %s: %v\n", address, err)
		return err
	}
	defer conn.Close()

	fmt.Printf("Connected to peer: %s\n", address)

	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: torrent-client <path-to-torrent-file>")
		return
	}

	torrentPath := os.Args[1]

	// Open and parse the torrent file
	torrent, err := OpenTorrentFile(torrentPath)
	if err != nil {
		fmt.Println("Failed to open torrent file:", err)
		return
	}

	// Generate a peer ID
	peerID := generatePeerID()

	// Announce to the tracker and get the list of peers
	peers, err := announceToHTTPTracker(torrent, peerID)
	if err != nil {
		fmt.Println("Failed to get peers from tracker:", err)
		return
	}

	fmt.Println("Starting download from peers...")
	for _, peer := range peers {
		if err := ConnectToPeer(peer); err != nil {
			fmt.Println(err)
			continue
		}
		// You can add logic to request pieces from the peer here.
	}
}
