package main

import (
	"net"
)

// Peer represents a peer in the swarm.
type Peer struct {
	IP   net.IP // Changed from [4]byte to net.IP
	Port uint16
}

// TorrentFile represents the structure of a .torrent file.
type TorrentFile struct {
	Announce string
	InfoHash [20]byte
	Info     TorrentInfo
}

// TorrentInfo represents information about the files contained in the .torrent file.
type TorrentInfo struct {
	Length      int64
	PieceLength int64
	PieceHashes [][20]byte // List of 20-byte SHA-1 hashes for each piece
}
