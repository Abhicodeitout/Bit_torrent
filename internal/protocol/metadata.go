package protocol

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/types"
)

// MsgExtended is the BEP 10 extension protocol message ID.
const MsgExtended = 20

type MetadataProgressFunc func(string)

type extensionHandshake struct {
	MetadataSize int64                   `bencode:"metadata_size"`
	Extensions   *extensionHandshakeMap `bencode:"m"`
}

type extensionHandshakeMap struct {
	UTMetadata int64 `bencode:"ut_metadata"`
}

type metadataMessage struct {
	MsgType   int64 `bencode:"msg_type"`
	Piece     int64 `bencode:"piece"`
	TotalSize int64 `bencode:"total_size"`
}

type infoDict struct {
	Name        string         `bencode:"name"`
	Length      int64          `bencode:"length"`
	PieceLength int64          `bencode:"piece length"`
	Pieces      string         `bencode:"pieces"`
	Private     int64          `bencode:"private"`
	Files       []infoDictFile `bencode:"files"`
}

type infoDictFile struct {
	Length int64    `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencodeValue = interface{}

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
	return fetchMetadata(conn, infoHash, peerID, "", nil)
}

func fetchMetadata(conn net.Conn, infoHash [20]byte, peerID [20]byte, peerLabel string, progress MetadataProgressFunc) (*types.TorrentInfo, error) {
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
		value, _, err := decodeBencodeValue(msg.Payload[1:])
		if err != nil {
			continue
		}
		peerHS, ok := value.(map[string]bencodeValue)
		if !ok {
			continue
		}
		if extMap, ok := peerHS["m"].(map[string]bencodeValue); ok {
			if id, ok := extMap["ut_metadata"].(int64); ok {
				peerMetaID = id
			}
		}
		if size, ok := peerHS["metadata_size"].(int64); ok {
			metadataSize = size
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
	reportMetadataProgress(progress, "Metadata: peer %s advertised %d bytes across %d piece(s)", peerLabel, metadataSize, numPieces)
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

			value, _, err := decodeBencodeValue(msg.Payload[1 : 1+dictLen])
			if err != nil {
				continue
			}
			resp, ok := value.(map[string]bencodeValue)
			if !ok {
				continue
			}

			msgType, _ := resp["msg_type"].(int64)
			switch msgType {
			case 1: // data
				raw := msg.Payload[1+dictLen:]
				offset := i * metaPieceSize
				copy(assembled[offset:], raw)
				if shouldReportMetadataPiece(i, numPieces) {
					reportMetadataProgress(progress, "Metadata: received piece %d/%d from %s", i+1, numPieces, peerLabel)
				}
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
	reportMetadataProgress(progress, "Metadata: validated info dictionary from %s", peerLabel)

	return decodeInfoDict(assembled)
}

// FetchMetadataFromPeers tries each peer in order, returning the TorrentInfo
// from the first peer that provides valid metadata.
func FetchMetadataFromPeers(peers []types.Peer, infoHash [20]byte, peerID [20]byte) (*types.TorrentInfo, error) {
	return FetchMetadataFromPeersWithProgress(peers, infoHash, peerID, nil)
}

// FetchMetadataFromPeersWithProgress tries peers in parallel and emits progress
// messages while discovering metadata.
func FetchMetadataFromPeersWithProgress(peers []types.Peer, infoHash [20]byte, peerID [20]byte, progress MetadataProgressFunc) (*types.TorrentInfo, error) {
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers to fetch metadata from")
	}

	const maxConcurrentPeers = 8
	type result struct {
		info *types.TorrentInfo
		err  error
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerCh := make(chan types.Peer)
	resultCh := make(chan result, len(peers))
	workerCount := maxConcurrentPeers
	if len(peers) < workerCount {
		workerCount = len(peers)
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case peer, ok := <-peerCh:
					if !ok {
						return
					}

					addr := net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))
					reportMetadataProgress(progress, "Metadata: trying peer %s", addr)
					dialer := net.Dialer{Timeout: 10 * time.Second}
					conn, err := dialer.DialContext(ctx, "tcp", addr)
					if err != nil {
						reportMetadataProgress(progress, "Metadata: peer %s dial failed: %v", addr, err)
						select {
						case resultCh <- result{err: fmt.Errorf("%s: dial: %w", addr, err)}:
						case <-ctx.Done():
						}
						continue
					}

					info, err := fetchMetadataWithRecovery(conn, infoHash, peerID, addr, progress)
					_ = conn.Close()
					if err == nil {
						reportMetadataProgress(progress, "Metadata: peer %s returned valid metadata", addr)
						select {
						case resultCh <- result{info: info}:
						case <-ctx.Done():
						}
						return
					}
					reportMetadataProgress(progress, "Metadata: peer %s failed: %v", addr, err)

					select {
					case resultCh <- result{err: fmt.Errorf("%s: %w", addr, err)}:
					case <-ctx.Done():
					}
				}
			}
		}()
	}

	go func() {
		defer close(peerCh)
		for _, peer := range peers {
			select {
			case <-ctx.Done():
				return
			case peerCh <- peer:
			}
		}
	}()

	var errSummaries []string
	for i := 0; i < len(peers); i++ {
		res := <-resultCh
		if res.err == nil {
			cancel()
			return res.info, nil
		}
		if len(errSummaries) < 5 {
			errSummaries = append(errSummaries, res.err.Error())
		}
	}

	wg.Wait()
	if len(errSummaries) == 0 {
		return nil, fmt.Errorf("failed to fetch metadata from any of %d peers", len(peers))
	}
	return nil, fmt.Errorf("failed to fetch metadata from any of %d peers: %s", len(peers), strings.Join(errSummaries, "; "))
}

func fetchMetadataWithRecovery(conn net.Conn, infoHash [20]byte, peerID [20]byte, peerLabel string, progress MetadataProgressFunc) (info *types.TorrentInfo, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during metadata fetch: %v", recovered)
			reportMetadataProgress(progress, "Metadata: peer %s panicked: %v", peerLabel, recovered)
		}
	}()

	return fetchMetadata(conn, infoHash, peerID, peerLabel, progress)
}

func reportMetadataProgress(progress MetadataProgressFunc, format string, args ...interface{}) {
	if progress == nil {
		return
	}
	progress(fmt.Sprintf(format, args...))
}

func shouldReportMetadataPiece(pieceIndex, totalPieces int) bool {
	if totalPieces <= 8 {
		return true
	}
	if pieceIndex == 0 || pieceIndex == totalPieces-1 {
		return true
	}
	return (pieceIndex+1)%8 == 0
}

// decodeInfoDict parses a raw bencoded info-dictionary into a TorrentInfo.
func decodeInfoDict(data []byte) (*types.TorrentInfo, error) {
	value, _, err := decodeBencodeValue(data)
	if err != nil {
		return nil, fmt.Errorf("decode info dict: %w", err)
	}

	raw, ok := value.(map[string]bencodeValue)
	if !ok {
		return nil, fmt.Errorf("decode info dict: unexpected top-level type %T", value)
	}

	info := &types.TorrentInfo{}
	if name, ok := raw["name"].(string); ok {
		info.Name = name
	}
	if privateFlag, ok := raw["private"].(int64); ok && privateFlag == 1 {
		info.Private = true
	}

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
	if length, ok := raw["length"].(int64); ok {
		info.Length = length
	} else {
		files, ok := raw["files"].([]bencodeValue)
		if !ok {
			files = nil
		}
		for _, f := range files {
			fm, ok := f.(map[string]bencodeValue)
			if !ok {
				continue
			}
			fLen, _ := fm["length"].(int64)
			info.Length += fLen
			var path []string
			if parts, ok := fm["path"].([]bencodeValue); ok {
				for _, part := range parts {
					if s, ok := part.(string); ok {
						path = append(path, s)
					}
				}
			}
			info.Files = append(info.Files, types.FileInfo{
				Length: fLen,
				Path:   path,
			})
		}
	}

	if len(info.PieceHashes) == 0 {
		return nil, fmt.Errorf("info dict contains no piece hashes")
	}
	return info, nil
}

func decodeBencodeValue(data []byte) (bencodeValue, int, error) {
	if len(data) == 0 {
		return nil, 0, fmt.Errorf("empty bencode data")
	}

	switch data[0] {
	case 'i':
		end := bytes.IndexByte(data, 'e')
		if end == -1 {
			return nil, 0, fmt.Errorf("unterminated integer")
		}
		var value int64
		if _, err := fmt.Sscanf(string(data[1:end]), "%d", &value); err != nil {
			return nil, 0, fmt.Errorf("parse integer: %w", err)
		}
		return value, end + 1, nil
	case 'l':
		items := make([]bencodeValue, 0)
		pos := 1
		for pos < len(data) && data[pos] != 'e' {
			item, n, err := decodeBencodeValue(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			items = append(items, item)
			pos += n
		}
		if pos >= len(data) || data[pos] != 'e' {
			return nil, 0, fmt.Errorf("unterminated list")
		}
		return items, pos + 1, nil
	case 'd':
		items := make(map[string]bencodeValue)
		pos := 1
		for pos < len(data) && data[pos] != 'e' {
			keyValue, n, err := decodeBencodeValue(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			key, ok := keyValue.(string)
			if !ok {
				return nil, 0, fmt.Errorf("dictionary key is not a string")
			}
			pos += n
			value, n, err := decodeBencodeValue(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			items[key] = value
			pos += n
		}
		if pos >= len(data) || data[pos] != 'e' {
			return nil, 0, fmt.Errorf("unterminated dictionary")
		}
		return items, pos + 1, nil
	default:
		if data[0] < '0' || data[0] > '9' {
			return nil, 0, fmt.Errorf("unexpected bencode token %q", data[0])
		}
		colon := bytes.IndexByte(data, ':')
		if colon == -1 {
			return nil, 0, fmt.Errorf("string length missing colon")
		}
		length := 0
		for _, ch := range data[:colon] {
			if ch < '0' || ch > '9' {
				return nil, 0, fmt.Errorf("invalid string length")
			}
			length = length*10 + int(ch-'0')
		}
		start := colon + 1
		end := start + length
		if end > len(data) {
			return nil, 0, fmt.Errorf("string data truncated")
		}
		return string(data[start:end]), end, nil
	}
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
