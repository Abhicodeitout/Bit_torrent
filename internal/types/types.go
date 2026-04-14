package types

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
	Announce     string
	AnnounceList [][]string // List of announce URLs (tier list)
	InfoHash     [20]byte
	Info         TorrentInfo
	RawInfo      []byte // Raw encoded info for hash calculation
}

// TorrentInfo represents information about the files contained in the .torrent file.
type TorrentInfo struct {
	Length      int64
	PieceLength int64
	PieceHashes [][20]byte // List of 20-byte SHA-1 hashes for each piece
	Files       []FileInfo
}

// FileInfo represents information about a file in a multi-file torrent.
type FileInfo struct {
	Length int64
	Path   []string
}

// MagnetLink represents a parsed magnet link.
type MagnetLink struct {
	InfoHash  [20]byte
	Name      string
	Trackers  []string
	PeerAddrs []string
}

// PieceState tracks the state of a piece.
type PieceState struct {
	Index    int
	Data     []byte
	Hash     [20]byte
	Complete bool
}
