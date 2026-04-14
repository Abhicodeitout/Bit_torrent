package protocol

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"net"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/types"
)

// MsgExtended is the BEP 10 extension protocol message ID.
const MsgExtended = 20

// handshakeWithExtBit sends a BitTorrent handshake with the BEP 10 extension bit
// set in the reserved bytes, and returns the remote peer-ID and whether the peer
// also advertises extension support.
//
// Handshake wire layout (68 bytes):
//
//	[0]      pstrlen (19)
//	[1..19]  "BitTorrent protocol"
//	[20..27] reserved  ← bit 20 from right = byte[5] bit-4 = 0x10
//	[28..47] info_hash
//	[48..67] peer_id
func handshakeWithExtBit(conn net.Conn, infoHash [20]byte, peerID [20]byte) ([20]byte, bool, error) {
	pstr := "BitTorrent protocol"
	buf := make([]byte, 0, 68)
	buf = append(buf, byte(len(pstr)))
	buf = append(buf, pstr...)

	var reserved [8]byte
	reserved[5] = 0x10 // extension protocol bit (BEP 10)
	buf = append(buf, reserved[:]...)
	buf = append(buf, infoHash[:]...)
	buf = append(buf, peerID[:]...)

	conn.SetWriteDeadline(time.Now().Add(connTimeout))
	if _, err := conn.Write(buf); err != nil {
		return [20]byte{}, false, fmt.Errorf("handshake write: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	resp := make([]byte, 68)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return [20]byte{}, false, fmt.Errorf("handshake read: %w", err)
	}

	if resp[0] != byte(len(pstr)) || string(resp[1:20]) != pstr {
		return [20]byte{}, false, fmt.Errorf("invalid handshake protocol string")
	}

	// reserved bytes start at offset 20; extension bit is reserved[5] = resp[25]
	supportsExt := resp[25]&0x10 != 0

	var remoteID [20]byte
	copy(remoteID[:], resp[48:68])
	return remoteID, supportsExt, nil
}

// FetchMetadata retrieves the torrent info-dictionary from a single peer using
// the Extension Protocol (BEP 10) and the ut_metadata extension (BEP 9).
// The caller is responsible for closing conn after this returns.
func FetchMetadata(conn net.Conn, infoHash [20]byte, peerID [20]byte) (*types.TorrentInfo, error) {
	_, supportsExt, err := handshakeWithExtBit(conn, infoHash, peerID)
	if err != nil {
		return nil, err
	}
	if !supportsExt {
		return nil, fmt.Errorf("peer does not support BEP 10 extension protocol")
	}

	// ── Send our extension handshake (ext-ID 0, handshake) ────────────────────
	ourHS := map[string]interface{}{
		"m": map[string]interface{}{
			"ut_metadata": int64(1), // local ID we assign to ut_metadata
		},
	}
	var hsBuf bytes.Buffer
	if err := bencode.Marshal(&hsBuf, ourHS); err != nil {
		return nil, fmt.Errorf("marshal ext handshake: %w", err)
	}
	if err := SendMessage(conn, Message{
		ID:      MsgExtended,
		Payload: append([]byte{0}, hsBuf.Bytes()...), // ext-ID 0 = handshake
	}); err != nil {
		return nil, fmt.Errorf("send ext handshake: %w", err)
	}

	// ── Read peer's extension handshake ───────────────────────────────────────
	var peerMetaID int64
	var metadataSize int64

	const maxSkip = 200
	for skip := 0; skip < maxSkip; skip++ {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		msg, err := ReadMessage(conn)
		if err != nil {
			return nil, fmt.Errorf("read ext handshake: %w", err)
		}
		// Extension handshake: ID=20, first payload byte = 0
		if msg.ID != MsgExtended || len(msg.Payload) == 0 || msg.Payload[0] != 0 {
			continue
		}
		var peerHS map[string]interface{}
		if err := bencode.Unmarshal(bytes.NewReader(msg.Payload[1:]), &peerHS); err != nil {
			continue
		}
		if m, ok := peerHS["m"].(map[string]interface{}); ok {
			if id, ok := m["ut_metadata"].(int64); ok {
				peerMetaID = id
			}
		}
		if sz, ok := peerHS["metadata_size"].(int64); ok {
			metadataSize = sz
		}
		break
	}

	if peerMetaID == 0 {
		return nil, fmt.Errorf("peer does not support ut_metadata extension")
	}
	if metadataSize <= 0 || metadataSize > 10*1024*1024 {
		return nil, fmt.Errorf("invalid metadata_size from peer: %d", metadataSize)
	}

	// ── Fetch metadata pieces ─────────────────────────────────────────────────
	const metaPieceSize = 16384
	numPieces := int((metadataSize + metaPieceSize - 1) / metaPieceSize)
	assembled := make([]byte, metadataSize)

	for i := 0; i < numPieces; i++ {
		req := map[string]interface{}{
			"msg_type": int64(0), // request
			"piece":    int64(i),
		}
		var reqBuf bytes.Buffer
		if err := bencode.Marshal(&reqBuf, req); err != nil {
			return nil, fmt.Errorf("marshal metadata request piece %d: %w", i, err)
		}
		if err := SendMessage(conn, Message{
			ID:      MsgExtended,
			Payload: append([]byte{byte(peerMetaID)}, reqBuf.Bytes()...),
		}); err != nil {
			return nil, fmt.Errorf("send metadata request piece %d: %w", i, err)
		}

		for skip := 0; skip < maxSkip; skip++ {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			msg, err := ReadMessage(conn)
			if err != nil {
				return nil, fmt.Errorf("read metadata piece %d: %w", i, err)
			}
			if msg.ID != MsgExtended || len(msg.Payload) < 2 {
				continue
			}

			dictLen, err := bencodeLen(msg.Payload[1:])
			if err != nil {
				continue
			}

			var resp map[string]interface{}
			if err := bencode.Unmarshal(bytes.NewReader(msg.Payload[1:1+dictLen]), &resp); err != nil {
				continue
			}

			msgType, _ := resp["msg_type"].(int64)
			switch msgType {
			case 1: // data
				raw := msg.Payload[1+dictLen:]
				offset := i * metaPieceSize
				copy(assembled[offset:], raw)
				goto nextPiece
			case 2: // reject
				return nil, fmt.Errorf("peer rejected metadata piece %d", i)
			}
		}
		return nil, fmt.Errorf("did not receive metadata piece %d after %d messages", i, maxSkip)
	nextPiece:
	}

	// ── Verify SHA1 against info hash ─────────────────────────────────────────
	if sha1.Sum(assembled) != infoHash {
		return nil, fmt.Errorf("metadata SHA1 mismatch — corrupted data from peer")
	}

	return decodeInfoDict(assembled)
}

// FetchMetadataFromPeers tries each peer in order, returning the TorrentInfo
// from the first peer that provides valid metadata.
func FetchMetadataFromPeers(peers []types.Peer, infoHash [20]byte, peerID [20]byte) (*types.TorrentInfo, error) {
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers to fetch metadata from")
	}
	for _, peer := range peers {
		addr := net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))
		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			continue
		}
		info, err := FetchMetadata(conn, infoHash, peerID)
		conn.Close()
		if err == nil {
			fmt.Printf("Fetched metadata from %s\n", addr)
			return info, nil
		}
		fmt.Printf("Metadata from %s: %v\n", addr, err)
	}
	return nil, fmt.Errorf("failed to fetch metadata from any of %d peers", len(peers))
}

// decodeInfoDict parses a raw bencoded info-dictionary into a TorrentInfo.
func decodeInfoDict(data []byte) (*types.TorrentInfo, error) {
	var raw map[string]interface{}
	if err := bencode.Unmarshal(bytes.NewReader(data), &raw); err != nil {
		return nil, fmt.Errorf("decode info dict: %w", err)
	}

	info := &types.TorrentInfo{}

	if pl, ok := raw["piece length"].(int64); ok {
		info.PieceLength = pl
	}
	if pieces, ok := raw["pieces"].(string); ok {
		for i := 0; i+20 <= len(pieces); i += 20 {
			var h [20]byte
			copy(h[:], pieces[i:i+20])
			info.PieceHashes = append(info.PieceHashes, h)
		}
	}
	if l, ok := raw["length"].(int64); ok {
		info.Length = l
	} else if files, ok := raw["files"].([]interface{}); ok {
		for _, f := range files {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			fLen, _ := fm["length"].(int64)
			info.Length += fLen
			if pathRaw, ok := fm["path"].([]interface{}); ok {
				var path []string
				for _, p := range pathRaw {
					if ps, ok := p.(string); ok {
						path = append(path, ps)
					}
				}
				info.Files = append(info.Files, types.FileInfo{Length: fLen, Path: path})
			}
		}
	}

	if len(info.PieceHashes) == 0 {
		return nil, fmt.Errorf("info dict contains no piece hashes")
	}
	return info, nil
}

// bencodeLen returns the number of bytes occupied by the first bencode value in data.
func bencodeLen(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty bencode data")
	}
	switch data[0] {
	case 'i':
		end := bytes.IndexByte(data[1:], 'e')
		if end < 0 {
			return 0, fmt.Errorf("unterminated integer")
		}
		return end + 2, nil

	case 'l':
		pos := 1
		for pos < len(data) {
			if data[pos] == 'e' {
				return pos + 1, nil
			}
			n, err := bencodeLen(data[pos:])
			if err != nil {
				return 0, err
			}
			pos += n
		}
		return 0, fmt.Errorf("unterminated list")

	case 'd':
		pos := 1
		for pos < len(data) {
			if data[pos] == 'e' {
				return pos + 1, nil
			}
			kn, err := bencodeLen(data[pos:]) // key
			if err != nil {
				return 0, err
			}
			pos += kn
			if pos >= len(data) {
				return 0, fmt.Errorf("dict key without value")
			}
			vn, err := bencodeLen(data[pos:]) // value
			if err != nil {
				return 0, err
			}
			pos += vn
		}
		return 0, fmt.Errorf("unterminated dict")

	default:
		// string: <length>:<data>
		colon := bytes.IndexByte(data, ':')
		if colon < 0 {
			return 0, fmt.Errorf("bencode string missing colon")
		}
		var sLen int
		for _, b := range data[:colon] {
			if b < '0' || b > '9' {
				return 0, fmt.Errorf("bencode string: non-digit in length")
			}
			sLen = sLen*10 + int(b-'0')
		}
		total := colon + 1 + sLen
		if total > len(data) {
			return 0, fmt.Errorf("bencode string data truncated")
		}
		return total, nil
	}
}
