package types

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	bencode "github.com/jackpal/bencode-go"
)

type bencodeTorrent struct {
	Announce     string       `bencode:"announce"`
	AnnounceList [][]string   `bencode:"announce-list"`
	Info         *bencodeInfo `bencode:"info"`
}

type bencodeInfo struct {
	Name        string        `bencode:"name"`
	PieceLength int64         `bencode:"piece length"`
	Pieces      string        `bencode:"pieces"`
	Length      int64         `bencode:"length"`
	Files       []bencodeFile `bencode:"files"`
	Private     int64         `bencode:"private"`
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

	if raw.Info == nil {
		return nil, fmt.Errorf("torrent file missing required 'info' dictionary")
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

	params, err := url.ParseQuery(strings.TrimPrefix(magnetLink, "magnet:?"))
	if err != nil {
		return nil, fmt.Errorf("parse magnet query: %w", err)
	}

	link := &MagnetLink{
		Trackers:  append([]string(nil), params["tr"]...),
		PeerAddrs: append([]string(nil), params["x.pe"]...),
	}
	if dn := params.Get("dn"); dn != "" {
		link.Name = dn
	}

	var hashErr error
	for _, xt := range params["xt"] {
		if !strings.HasPrefix(xt, "urn:btih:") {
			continue
		}

		link.InfoHash, err = decodeInfoHash(strings.TrimPrefix(xt, "urn:btih:"))
		if err == nil {
			return link, nil
		}
		hashErr = err
	}

	if hashErr != nil {
		return nil, fmt.Errorf("invalid magnet info hash: %w", hashErr)
	}
	return nil, fmt.Errorf("magnet link is missing xt=urn:btih:<infohash>")
}

// Base32ToInfoHash converts a base32 encoded string to a 20-byte info hash.
func Base32ToInfoHash(encoded string) [20]byte {
	var hash [20]byte
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(encoded)))
	if err != nil || len(decoded) != len(hash) {
		return hash
	}
	copy(hash[:], decoded)
	return hash
}

func decodeInfoHash(value string) ([20]byte, error) {
	var hash [20]byte

	trimmed := strings.TrimSpace(value)
	switch len(trimmed) {
	case 40:
		decoded, err := hex.DecodeString(trimmed)
		if err != nil {
			return hash, err
		}
		copy(hash[:], decoded)
		return hash, nil
	case 32:
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(trimmed))
		if err != nil {
			return hash, err
		}
		if len(decoded) != len(hash) {
			return hash, fmt.Errorf("decoded base32 info hash has %d bytes", len(decoded))
		}
		copy(hash[:], decoded)
		return hash, nil
	default:
		return hash, fmt.Errorf("unsupported info hash length %d", len(trimmed))
	}
}
