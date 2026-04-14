package types

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"os"
	"net/url"
	"strings"

	bencode "github.com/jackpal/bencode-go"
)

type bencodeTorrent struct {
	Announce     string     `bencode:"announce"`
	AnnounceList [][]string `bencode:"announce-list"`
	Info         bencodeInfo `bencode:"info"`
}

type bencodeInfo struct {
	Name        string       `bencode:"name"`
	PieceLength int64        `bencode:"piece length"`
	Pieces      string       `bencode:"pieces"`
	Length      int64        `bencode:"length"`
	Files       []bencodeFile `bencode:"files"`
	Private     int64        `bencode:"private"`
}

type bencodeFile struct {
	Length int64    `bencode:"length"`
	Path   []string `bencode:"path"`
}

// GeneratePeerID creates a unique 20-byte peer ID for the client.
func GeneratePeerID() [20]byte {
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

	var raw bencodeTorrent
	err = bencode.Unmarshal(file, &raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse torrent file: %v", err)
	}

	torrent := &TorrentFile{}
	torrent.Announce = raw.Announce
	torrent.AnnounceList = raw.AnnounceList

	// Calculate info hash by re-encoding the info dict
	infoEncoded := new(bytes.Buffer)
	err = bencode.Marshal(infoEncoded, raw.Info)
	if err != nil {
		return nil, fmt.Errorf("failed to encode info dict: %v", err)
	}
	torrent.RawInfo = infoEncoded.Bytes()
	hash := sha1.Sum(torrent.RawInfo)
	torrent.InfoHash = hash

	torrent.Info.PieceLength = raw.Info.PieceLength
	torrent.Info.Name = raw.Info.Name
	if raw.Info.Private == 1 {
		torrent.Info.Private = true
	}

	// Parse pieces (concatenated 20-byte hashes)
	for i := 0; i < len(raw.Info.Pieces); i += 20 {
		if i+20 <= len(raw.Info.Pieces) {
			var h [20]byte
			copy(h[:], raw.Info.Pieces[i:i+20])
			torrent.Info.PieceHashes = append(torrent.Info.PieceHashes, h)
		}
	}

	// Parse file information
	if len(raw.Info.Files) == 0 {
		torrent.Info.Length = raw.Info.Length
	} else {
		for _, f := range raw.Info.Files {
			torrent.Info.Length += f.Length
			torrent.Info.Files = append(torrent.Info.Files, FileInfo{
				Length: f.Length,
				Path:   append([]string(nil), f.Path...),
			})
		}
	}

	if len(torrent.Info.PieceHashes) == 0 || torrent.Info.PieceLength <= 0 {
		return nil, fmt.Errorf("invalid torrent metadata: missing pieces or piece length")
	}

	return torrent, nil
}

// ParseMagnetLink parses a magnet link and returns a MagnetLink struct.
func ParseMagnetLink(magnetLink string) (*MagnetLink, error) {
	if !strings.HasPrefix(magnetLink, "magnet:?") {
		return nil, fmt.Errorf("invalid magnet link format")
	}

	link := &MagnetLink{}

	// Parse the URI
	query := strings.TrimPrefix(magnetLink, "magnet:?")
	params := strings.Split(query, "&")

	for _, param := range params {
		parts := strings.Split(param, "=")
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value, err := url.QueryUnescape(parts[1])
		if err != nil {
			continue
		}

		switch key {
		case "xt":
			// Extract info hash from urn:btih:...
			if strings.HasPrefix(value, "urn:btih:") {
				hashStr := strings.TrimPrefix(value, "urn:btih:")
				// Convert hex string to bytes
				if len(hashStr) == 40 {
					// Hex encoded
					hex := make([]byte, 20)
					for i := 0; i < 20; i++ {
						fmt.Sscanf(hashStr[i*2:i*2+2], "%x", &hex[i])
					}
					copy(link.InfoHash[:], hex)
				} else if len(hashStr) == 32 {
					// Base32 encoded (RFC 3548)
					link.InfoHash = Base32ToInfoHash(hashStr)
				}
			}
		case "dn":
			link.Name = value
		case "tr":
			link.Trackers = append(link.Trackers, value)
		case "x.pe":
			link.PeerAddrs = append(link.PeerAddrs, value)
		}
	}

	return link, nil
}

// Base32ToInfoHash converts a base32 encoded string to a 20-byte info hash.
func Base32ToInfoHash(encoded string) [20]byte {
	// Base32 alphabet used in BitTorrent
	const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

	var hash [20]byte
	var bits uint64
	var bitCount int

	for _, char := range encoded {
		idx := strings.IndexRune(base32Alphabet, char)
		if idx < 0 {
			continue
		}

		bits = (bits << 5) | uint64(idx)
		bitCount += 5

		if bitCount >= 8 {
			bitCount -= 8
			byteIndex := (len(encoded)*5/8 - bitCount/8 - 1)
			if byteIndex >= 0 && byteIndex < 20 {
				hash[byteIndex] = byte((bits >> uint(bitCount)) & 0xFF)
			}
		}
	}

	return hash
}
