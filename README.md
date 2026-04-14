# BitTorrent Client

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/Abhicodeitout/Bit_torrent)](https://goreportcard.com/report/github.com/Abhicodeitout/Bit_torrent)
[![GitHub Stars](https://img.shields.io/github/stars/Abhicodeitout/Bit_torrent?style=flat&logo=github)](https://github.com/Abhicodeitout/Bit_torrent/stargazers)
[![GitHub Forks](https://img.shields.io/github/forks/Abhicodeitout/Bit_torrent?style=flat&logo=github)](https://github.com/Abhicodeitout/Bit_torrent/network/members)
[![GitHub Issues](https://img.shields.io/github/issues/Abhicodeitout/Bit_torrent?style=flat)](https://github.com/Abhicodeitout/Bit_torrent/issues)

A fully functional, production-ready BitTorrent client written in Go — supports `.torrent` files, magnet links, DHT peer discovery, and the extension protocol for trackerless downloads.

---

## Features

| Feature | Status | Details |
|---|---|---|
| `.torrent` file parsing | ✅ | Full bencode decoding, multi-file torrents |
| Magnet link support | ✅ | Hex & base32 info hash, multiple `tr=` params |
| HTTP tracker (BEP 3) | ✅ | Compact & dictionary peer formats |
| UDP tracker (BEP 15) | ✅ | Full connect → announce handshake |
| DHT peer discovery (BEP 5) | ✅ | Kademlia iterative lookup, public bootstrap nodes |
| Extension protocol (BEP 10) | ✅ | Capability handshake with peers |
| Metadata from peers (BEP 9) | ✅ | `ut_metadata` — download info-dict from swarm |
| BitTorrent wire protocol | ✅ | Handshake, choke/unchoke, request, piece, cancel |
| Piece integrity (SHA-1) | ✅ | Every piece verified before writing |
| Concurrent downloading | ✅ | 4 parallel peer connections |
| Single & multi-file torrents | ✅ | Correct file assembly for both layouts |
| IPv4 + IPv6 peers | ✅ | `net.JoinHostPort` safe addressing |

---

## Requirements

- **Go 1.23.0 or higher** — [Download Go](https://go.dev/dl/)
- No other system dependencies

---

## Installation

```bash
# Clone the repository
git clone https://github.com/Abhicodeitout/Bit_torrent.git
cd Bit_torrent

# Download dependencies
go mod download

# Build the binary
go build -o bin/torrent-client ./cmd/torrent-client/
```

---

## Usage

### Download from a `.torrent` file

```bash
./bin/torrent-client path/to/file.torrent
```

**Example:**

```bash
./bin/torrent-client ubuntu-24.04.iso.torrent
```

**What happens:**

1. Parse torrent file (bencode) — extracts info hash, piece hashes, trackers, file list
2. Contact all HTTP + UDP trackers to collect peers
3. If fewer than 5 peers found — also query DHT bootstrap nodes
4. Connect, handshake, download all pieces concurrently
5. SHA-1 verify every piece
6. Assemble and write output to `$HOME/Downloads/`

---

### Download from a magnet link

```bash
./bin/torrent-client "magnet:?xt=urn:btih:<INFO_HASH>&dn=<NAME>&tr=<TRACKER_URL>"
```

**Examples:**

```bash
# Magnet with HTTP tracker
./bin/torrent-client "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD&dn=MyFile&tr=http://tracker.example.com/announce"

# Magnet with UDP tracker (most common)
./bin/torrent-client "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD&tr=udp://tracker.opentrackr.org:1337/announce"

# Bare magnet (no trackers — uses DHT automatically)
./bin/torrent-client "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD"
```

**What happens (extra steps vs .torrent):**

- If trackers are listed: contacts all of them (HTTP + UDP)
- If no trackers, or fewer than 5 peers found: runs a DHT (BEP 5) Kademlia lookup against public bootstrap nodes to find peers
- Once peers are found: fetches the full torrent metadata from the swarm using BEP 9 (`ut_metadata`) and verifies it against the info hash
- Then proceeds identically to a .torrent file download

---

## Download Location

Files are saved to:

| Scenario | Output path |
|---|---|
| Single-file torrent with name | `$HOME/Downloads/<filename>` |
| Multi-file torrent | `$HOME/Downloads/<first-file-name>` |
| Name unavailable | `$HOME/Downloads/downloaded_<8-byte-hash>` |

---

## How It Works — Full Flow

```
User input (.torrent file or magnet link)
         │
         ├─ .torrent ──► Bencode decode ──► TorrentInfo (pieces, files, trackers)
         │
         └─ magnet ───► Parse URI ──────────┐
                                            │
                    ┌───────────────────────▼────────────────────┐
                    │         Peer Discovery (in order)           │
                    │  1. HTTP trackers  (BEP 3)                  │
                    │  2. UDP trackers   (BEP 15)                 │
                    │  3. DHT bootstrap  (BEP 5) — fallback       │
                    └───────────────────────┬────────────────────┘
                                            │
                    ┌───────────────────────▼────────────────────┐
                    │  BEP 10 Extension Handshake (magnet only)   │
                    │  BEP 9  ut_metadata fetch → SHA-1 verify    │
                    └───────────────────────┬────────────────────┘
                                            │
                    ┌───────────────────────▼────────────────────┐
                    │         Download Engine                     │
                    │  • 4 concurrent peer connections            │
                    │  • Request piece blocks (16 KB each)        │
                    │  • SHA-1 verify per piece                   │
                    │  • Re-queue failed pieces automatically     │
                    └───────────────────────┬────────────────────┘
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
│   │   └── dht.go               # BEP 5 — Kademlia DHT peer discovery
│   ├── protocol/
│   │   ├── protocol.go          # BitTorrent wire protocol (handshake, messages)
│   │   └── metadata.go          # BEP 10 + BEP 9 — extension + ut_metadata
│   ├── tracker/
│   │   └── tracker.go           # HTTP tracker (BEP 3) + UDP tracker (BEP 15)
│   ├── types/
│   │   ├── types.go             # Core data structures
│   │   └── parser.go            # .torrent & magnet link parsing
│   └── downloader/
│       └── downloader.go        # Concurrent piece downloader + file assembly
├── bin/
│   └── torrent-client           # Compiled binary
├── docs/                        # Extended documentation
├── go.mod
├── go.sum
└── LICENSE
```

---

## Reporting Issues

Please open an issue at: https://github.com/Abhicodeitout/Bit_torrent/issues

Include:
- The command you ran (redact any private info hashes if needed)
- Console output
- Your OS and Go version (`go version`)

---

## Contributing

Pull requests are welcome. Fork the repo, make your changes, and open a PR against `main`. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

---

## License

[MIT](LICENSE) — free to use, modify, and distribute.

