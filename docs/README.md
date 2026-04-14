# BitTorrent Client — Technical Overview

A production-ready BitTorrent client written in Go. Supports `.torrent` files, magnet links with tracker-based peer discovery (HTTP + UDP), DHT peer discovery (BEP 5), and metadata-from-peers (BEP 9/10) for fully trackerless operation.

---

## Features

- **Torrent File Support** — Full bencode decoding, single-file and multi-file torrents
- **Magnet Link Support** — Hex and base32 info hash, multiple `tr=` parameters
- **HTTP Tracker (BEP 3)** — Compact byte-stream and dictionary peer formats
- **UDP Tracker (BEP 15)** — Full connect/announce handshake over UDP
- **DHT Peer Discovery (BEP 5)** — Kademlia iterative `get_peers` lookup via public bootstrap nodes; used automatically when trackers return no/few peers or the magnet has no `tr=`
- **Extension Protocol (BEP 10)** — Capability negotiation handshake
- **ut_metadata / BEP 9** — Fetch and SHA-1-verify the info dictionary directly from swarm peers; enables full magnet-link downloads
- **BitTorrent Wire Protocol** — Handshake, choke/unchoke/interested, bitfield, request, piece, cancel, keep-alive
- **Concurrent Downloading** — 4 parallel peer connections with automatic piece re-queuing
- **Piece Integrity** — Every piece SHA-1-verified before being written
- **IPv4 + IPv6** — `net.JoinHostPort` safe addressing throughout

---

## Prerequisites

- **Go 1.23.0 or higher** — [go.dev/dl](https://go.dev/dl/)

---

## Installation & Setup

```bash
# 1. Clone
git clone https://github.com/Abhicodeitout/Bit_torrent.git
cd Bit_torrent

# 2. Dependencies
go mod download

# 3. Build
go build -o bin/torrent-client ./cmd/torrent-client/

# 4. Verify
./bin/torrent-client
# Usage: torrent-client <path-to-torrent-file-or-magnet-link>
```

---

## Usage

### Torrent file

```bash
./bin/torrent-client path/to/file.torrent
```

### Magnet link (with trackers)

```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>&dn=<NAME>&tr=udp://tracker.opentrackr.org:1337/announce"
```

### Bare magnet link (no trackers — DHT only)

```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>"
```

All three forms download to `$HOME/Downloads/`.

---

## Full Download Flow

```
Input
  │
  ├─ .torrent ──► Bencode decode ──────────────────────────────────────┐
  │                                                                      │
  └─ magnet ───► Parse URI                                              │
                    │                                                    │
                    ▼                                                    │
            Peer Discovery                                               │
            1. HTTP trackers  (BEP 3)                                   │
            2. UDP trackers   (BEP 15)                                  │
            3. DHT bootstrap  (BEP 5)  ← automatic fallback            │
                    │                                                    │
                    ▼                                                    │
            BEP 10 ext handshake                                         │
            BEP 9  ut_metadata fetch  ◄──────── (magnet only) ─────────┘
            SHA-1 verify metadata                                        │
                    │                                                    │
                    └──────────────────┬─────────────────────────────────┘
                                       ▼
                              Download Engine
                              4 parallel peers
                              16 KB blocks
                              SHA-1 per piece
                              auto-retry on fail
                                       │
                              $HOME/Downloads/
```

---

## Project Structure

```
Bit_torrent/
├── cmd/
│   └── torrent-client/
│       └── main.go              # Entry point — orchestrates the full flow
├── internal/
│   ├── dht/
│   │   └── dht.go               # BEP 5 — Kademlia DHT (get_peers, bootstrap)
│   ├── protocol/
│   │   ├── protocol.go          # Wire protocol (handshake, messages, io.ReadFull)
│   │   └── metadata.go          # BEP 10 ext handshake + BEP 9 ut_metadata
│   ├── tracker/
│   │   └── tracker.go           # HTTP (BEP 3) + UDP tracker (BEP 15)
│   ├── types/
│   │   ├── types.go             # Core structs: TorrentFile, Peer, MagnetLink …
│   │   └── parser.go            # .torrent bencode parser + magnet URI parser
│   └── downloader/
│       └── downloader.go        # Concurrent downloader + file assembly
├── bin/
│   └── torrent-client           # Compiled binary
├── docs/                        # Extended documentation
├── go.mod
├── go.sum
└── LICENSE                      # MIT
```

---

## Output Location

| Torrent type | Output |
|---|---|
| Single-file with name | `$HOME/Downloads/<filename>` |
| Multi-file torrent | `$HOME/Downloads/<first-file>` |
| Name unavailable | `$HOME/Downloads/downloaded_<8-hex-bytes>` |

---

## License

[MIT](../LICENSE)

- Easily portable to other systems

**`docs/`** - Documentation
- Complete guides and references
- Usage examples and scripts
- Generated by development team

### Why This Structure?

✅ **Go Conventions** - Follows standard Go project layout  
✅ **Clear Boundaries** - `internal/` packages can't be imported externally  
✅ **Modular Design** - Each package has a specific responsibility  
✅ **Scalable** - Easy to add new packages or features  
✅ **Maintainable** - Clear organization makes code easy to find and modify  
✅ **Testable** - Separate packages enable easy unit testing  

## Dependencies

- **github.com/jackpal/bencode-go**: Bencode encoding/decoding for torrent files

## How It Works

1. **Parse Input**: Identify whether input is a torrent file or magnet link
   - Torrent files are decoded using bencode
   - Magnet links are parsed to extract info hash and trackers

2. **Discover Peers**: Contact trackers to get a list of available peers
   - HTTP trackers: Send GET request with parameters
   - UDP trackers: Send UDP packets (simplified)

3. **Download Pieces**: Connect to peers and download file pieces
   - Perform BitTorrent handshake
   - Send interested message
   - Request pieces in blocks
   - Validate pieces using SHA-1 hash

4. **Assemble File**: Combine all pieces into the final file

## Configuration

- **Block Size**: 16 KB per block request
- **Concurrent Downloads**: 4 simultaneous peer connections
- **Connection Timeout**: 10 seconds
- **Read Timeout**: 10 seconds

## Testing

You can test with the provided `big-buck-bunny.torrent`:

```bash
./bin/torrent-client big-buck-bunny.torrent
```

Or with the magnet link from `magnets.txt`:

```bash
./bin/torrent-client "$(cat magnets.txt | head -1)"
```

## Limitations

- UDP tracker implementation is simplified (no full protocol support)
- No DHT (Distributed Hash Table) support for magnet links
- No PEX (Peer Exchange) support
- No encryption support
- Single-threaded tracker communication
- No bandwidth throttling

## Future Improvements

- Full UDP tracker protocol implementation
- DHT support for magnet links
- PEX support
- Connection encryption
- Bandwidth management
- Progress UI
- Resume capability for interrupted downloads

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

You are free to:
- ✅ Use this project for personal or commercial purposes
- ✅ Modify and distribute the code
- ✅ Use it in your own projects

Just ensure you include the original license notice.

## Author

Created by Abhicodeitout

## Support

For issues, questions, or improvements, feel free to contribute to this project or open an issue.
