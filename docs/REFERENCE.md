# BitTorrent Client — Architecture Reference

## BEP Compliance

| BEP | Name | Status | File |
|---|---|---|---|
| BEP 3 | BitTorrent Protocol | Full | `internal/protocol/protocol.go` |
| BEP 3 | HTTP Tracker | Full | `internal/tracker/tracker.go` |
| BEP 5 | DHT Protocol | Full (IPv4) | `internal/dht/dht.go` |
| BEP 9 | ut_metadata extension | Full | `internal/protocol/metadata.go` |
| BEP 10 | Extension Protocol | Full | `internal/protocol/metadata.go` |
| BEP 15 | UDP Tracker Protocol | Full | `internal/tracker/tracker.go` |
| BEP 23 | Compact Peer Lists | Full | `internal/tracker/tracker.go` |

---

## Package Reference

### `internal/types`

| Symbol | Description |
|---|---|
| `TorrentFile` | Parsed .torrent — announce, announce-list, info hash, `TorrentInfo` |
| `TorrentInfo` | Piece length, piece hashes, total length, files |
| `FileInfo` | Per-file length and path (multi-file torrents) |
| `Peer` | `net.IP` + `uint16` port |
| `MagnetLink` | Info hash, display name, tracker list, peer addrs |
| `OpenTorrentFile(path)` | Bencode-decode a .torrent file into `*TorrentFile` |
| `ParseMagnetLink(uri)` | Parse a `magnet:?...` URI into `*MagnetLink` |
| `GeneratePeerID()` | Generates a random 20-byte peer ID with `-GO0001-` prefix |

### `internal/tracker`

| Symbol | Description |
|---|---|
| `AnnounceToHTTPTracker(tf, peerID)` | BEP 3 HTTP GET announce; handles compact + dict formats |
| `AnnounceUDP(url, infoHash, peerID, port)` | BEP 15 UDP tracker — full connect/announce handshake |
| `ParsePeersCompact(data)` | Parse 6-byte-per-peer compact peer list |

### `internal/dht`

| Symbol | Description |
|---|---|
| `GetPeers(infoHash, maxPeers, timeout)` | Iterative Kademlia `get_peers` lookup. Seeds from 4 public bootstrap nodes. Returns up to `maxPeers` peers within `timeout`. |

**Bootstrap nodes:**
- `router.bittorrent.com:6881`
- `router.utorrent.com:6881`
- `dht.transmissionbt.com:6881`
- `dht.aelitis.com:6881`

### `internal/protocol`

| Symbol | Description |
|---|---|
| `Handshake(conn, infoHash, peerID)` | BEP 3 handshake; uses `io.ReadFull` |
| `ReadMessage(conn)` | Read length-prefixed message; capped at 32 MB |
| `SendMessage(conn, msg)` | Write length-prefixed message |
| `RequestMessage(index, begin, length)` | Build a `request` message |
| `ParsePieceMessage(payload)` | Decode a `piece` message payload |
| `FetchMetadata(conn, infoHash, peerID)` | BEP 10 ext handshake + BEP 9 metadata fetch over a single connection |
| `FetchMetadataFromPeers(peers, …)` | Try peers in order; return first valid `*TorrentInfo` |

### `internal/downloader`

| Symbol | Description |
|---|---|
| `DownloadTorrent(tf, peers, peerID)` | Concurrent download engine. Uses stop-channel pattern. |
| `ConnectToPeer(peer)` | Dial TCP with 10s timeout |
| `AssembleFile(pieces, tf)` | Write all pieces sequentially to `$HOME/Downloads/` |

---

## Peer Discovery Strategy

```
For every tracker URL (HTTP + UDP) in order:
  Stop early once >= 50 peers collected.

If peers < 5 after all trackers (or 0 trackers):
  DHT.GetPeers(infoHash, 50, 30-45s)
```

---

## Data Flow

```
.torrent file
    │
    ▼
OpenTorrentFile → TorrentFile{InfoHash, Info, AnnounceList}
    │
    ▼
gatherPeers → []Peer
    │
    ▼
DownloadTorrent
  ├─ ConnectToPeer
  ├─ Handshake
  ├─ Interested → wait Unchoke
  ├─ RequestMessage (16 KB blocks)
  ├─ ReadMessage → MsgPiece
  ├─ SHA-1 verify
  └─ assembledPieces → AssembleFile → $HOME/Downloads/<name>

magnet link
    │
    ▼
ParseMagnetLink
    │
    ▼
gatherPeers (same as above)
    │
    ▼
FetchMetadataFromPeers
  ├─ handshakeWithExtBit (reserved[5] |= 0x10)
  ├─ Send ext handshake (ID 0, m:{ut_metadata:1})
  ├─ Read peer ext handshake → peerMetaID, metadataSize
  ├─ Request each 16 KB metadata piece
  ├─ Assemble → SHA-1 == infoHash?
  └─ decodeInfoDict → *TorrentInfo
         │
         ▼
    DownloadTorrent (same as above)
```

---

## Tunable Constants

| Constant | Package | Default | Effect |
|---|---|---|---|
| `numGoroutine` | downloader | `4` | Parallel peer connections |
| `blockSize` | downloader | `16384` | Block request size (bytes) |
| `connTimeout` | downloader / protocol | `10s` | TCP dial + write deadline |
| `readTimeout` | protocol | `10s` | Read deadline per message |
| `alpha` | dht | `3` | Parallel DHT queries per round |
| `maxCandidates` | dht | `200` | DHT working set size |
| `maxMessageSize` | protocol | `33554432` (32 MB) | Guard against OOM |
