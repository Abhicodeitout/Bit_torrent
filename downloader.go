package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
)

// DownloadTorrent downloads pieces from the specified peers.
func DownloadTorrent(torrent *TorrentFile, peers []Peer) error {
	pieces := make([][]byte, (torrent.Info.Length+16383)/16384) // Assuming 16KB pieces
	downloaded := make([]bool, len(pieces))

	for _, peer := range peers {
		fmt.Printf("Attempting to connect to peer: %s:%d\n", peer.IP.String(), peer.Port)
		if err := ConnectToPeer(peer); err != nil {
			fmt.Println(err)
			continue
		}

		for i := range pieces {
			if downloaded[i] {
				continue // Skip already downloaded pieces
			}

			// Request piece from peer
			data, err := RequestPiece(peer, i)
			if err != nil {
				fmt.Println(err)
				continue
			}

			pieces[i] = data
			downloaded[i] = true
			fmt.Printf("Downloaded piece %d from %s\n", i, peer.IP.String())
		}
	}

	// Assemble pieces into the final file
	return AssembleFile(pieces, torrent)
}

// RequestPiece requests a specific piece from the peer.
func RequestPiece(peer Peer, index int) ([]byte, error) {
	address := fmt.Sprintf("%s:%d", peer.IP.String(), peer.Port)
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer %s: %v", address, err)
	}
	defer conn.Close()

	// Prepare the request for the piece (this is a simplified version)
	request := make([]byte, 17)                             // 1 byte for the message length, 1 byte for the message ID, 4 bytes for index
	binary.BigEndian.PutUint32(request[1:5], uint32(index)) // Piece index
	request[0] = byte(len(request) - 1)                     // Message length
	request[5] = 6                                          // Message ID for "request"

	_, err = conn.Write(request)
	if err != nil {
		return nil, fmt.Errorf("failed to send request for piece %d: %v", index, err)
	}

	// Read the response (this is a placeholder)
	response := make([]byte, 16384) // Assuming piece size is 16KB
	n, err := conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read piece %d: %v", index, err)
	}

	return response[:n], nil // Return the downloaded piece
}

// AssembleFile assembles downloaded pieces into the final file.
func AssembleFile(pieces [][]byte, torrent *TorrentFile) error {
	fmt.Println("Creating the downloadable file")
	outputFilePath := fmt.Sprintf("%s/downloaded_%s.dat", os.Getenv("HOME"), torrent.InfoHash) // Change to desired output filename
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outputFile.Close()

	for _, piece := range pieces {
		if _, err := outputFile.Write(piece); err != nil {
			return fmt.Errorf("failed to write to output file: %v", err)
		}
	}

	fmt.Println("File assembled successfully.")
	return nil
}
