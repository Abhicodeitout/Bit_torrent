package types

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"os"
	"strings"
	"net/url"

	bencode "github.com/jackpal/bencode-go"
)

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

	var raw map[string]interface{}
	err = bencode.Unmarshal(file, &raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse torrent file: %v", err)
	}

	torrent := &TorrentFile{}

	// Parse announce
	if announce, ok := raw["announce"].(string); ok {
		torrent.Announce = announce
	}

	// Parse announce-list (tier list)
	if announceList, ok := raw["announce-list"].([]interface{}); ok {
		for _, tier := range announceList {
			if tierList, ok := tier.([]interface{}); ok {
				var tierAnnounces []string
				for _, announce := range tierList {
					if announceStr, ok := announce.(string); ok {
						tierAnnounces = append(tierAnnounces, announceStr)
					}
				}
				if len(tierAnnounces) > 0 {
					torrent.AnnounceList = append(torrent.AnnounceList, tierAnnounces)
				}
			}
		}
	}

	// Parse info dictionary
	infoRaw, ok := raw["info"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'info' field in torrent")
	}

	// Calculate info hash by re-encoding the info dict
	infoEncoded := new(bytes.Buffer)
	err = bencode.Marshal(infoEncoded, infoRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to encode info dict: %v", err)
	}
	torrent.RawInfo = infoEncoded.Bytes()
	hash := sha1.Sum(torrent.RawInfo)
	torrent.InfoHash = hash

	// Parse piece length
	if pieceLength, ok := infoRaw["piece length"].(int64); ok {
		torrent.Info.PieceLength = pieceLength
	}

	if name, ok := infoRaw["name"].(string); ok {
		torrent.Info.Name = name
	}

	if privateFlag, ok := infoRaw["private"].(int64); ok && privateFlag == 1 {
		torrent.Info.Private = true
	}

	// Parse pieces (concatenated 20-byte hashes)
	if pieces, ok := infoRaw["pieces"].(string); ok {
		for i := 0; i < len(pieces); i += 20 {
			if i+20 <= len(pieces) {
				var hash [20]byte
				copy(hash[:], pieces[i:i+20])
				torrent.Info.PieceHashes = append(torrent.Info.PieceHashes, hash)
			}
		}
	}

	// Parse file information
	if length, ok := infoRaw["length"].(int64); ok {
		// Single file torrent
		torrent.Info.Length = length
	} else if files, ok := infoRaw["files"].([]interface{}); ok {
		// Multi-file torrent
		for _, f := range files {
			if fileMap, ok := f.(map[string]interface{}); ok {
				if length, ok := fileMap["length"].(int64); ok {
					torrent.Info.Length += length
					if pathList, ok := fileMap["path"].([]interface{}); ok {
						var pathStr []string
						for _, p := range pathList {
							if pStr, ok := p.(string); ok {
								pathStr = append(pathStr, pStr)
							}
						}
						torrent.Info.Files = append(torrent.Info.Files, FileInfo{
							Length: length,
							Path:   pathStr,
						})
					}
				}
			}
		}
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
