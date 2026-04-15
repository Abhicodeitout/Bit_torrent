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
| Magnet link support | ✅ | Hex/base32 info hash, multiple `tr=` trackers, `x.pe` peer hints |
| HTTP tracker (BEP 3) | ✅ | Compact & dictionary peer formats |
| UDP tracker (BEP 15) | ✅ | Full connect → announce handshake |
| DHT peer discovery (BEP 5) | ✅ | Kademlia iterative lookup, public bootstrap nodes |
| Extension protocol (BEP 10) | ✅ | Capability handshake with peers |
| Metadata from peers (BEP 9) | ✅ | `ut_metadata` — download info-dict from swarm |
| BitTorrent wire protocol | ✅ | Handshake, choke/unchoke, request, piece, cancel |
| Private torrent safety | ✅ | Honors `private=1` and skips DHT where required |
| Piece integrity (SHA-1) | ✅ | Every piece verified before writing |
| Resumable downloads | ✅ | Persisted piece state + on-disk piece verification |
| Disk-backed piece writes | ✅ | No full-file in-memory buffering |
| Adaptive peer scheduling | ✅ | Peer scoring, backoff, temporary quarantine |
| Continuous peer discovery | ✅ | Trackers + DHT queried during active download |
| Inbound peer listener | ✅ | Configurable listen port for incoming peer handshakes |
| UPnP/NAT-PMP port mapping | ✅ | Automatic external reachability via gateway mapping |
| Tracker lifecycle events | ✅ | `started`, periodic announce, `completed`, `stopped` |
| Basic upload serving | ✅ | Serves available pieces to inbound peers via request/piece messages |
| Tracker progress counters | ✅ | Announces live `downloaded`, `left`, and `uploaded` values |
| Discovery retries/fallbacks | ✅ | Multi-round tracker announces + DHT rounds + peer dedupe |
| Peer exchange (PEX ingest) | ✅ | Accepts ut_pex peers from BEP10-capable sessions |
| Rarest-first + endgame mode | ✅ | Better swarm efficiency and tail completion |
| Concurrent downloading | ✅ | Configured worker pool with long-lived peer sessions |
| Runtime telemetry | ✅ | Piece/peer stats during download, toggleable |
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

## Platform Setup

### Linux

```bash
# Build
go build -o bin/torrent-client ./cmd/torrent-client/

# Run
./bin/torrent-client path/to/file.torrent
```

No extra steps required. The binary runs natively on any Linux distribution with Go 1.23+.

---

### macOS

```bash
# Build
go build -o bin/torrent-client ./cmd/torrent-client/

# Run
./bin/torrent-client path/to/file.torrent
```

**First-run Gatekeeper warning:** macOS may block the binary because it was built locally and is unsigned. If you see *"cannot be opened because the developer cannot be verified"*, run once:

```bash
xattr -d com.apple.quarantine bin/torrent-client
```

Then re-run normally. Alternatively, go to **System Settings → Privacy & Security → Allow Anyway**.

---

### Windows

**Prerequisites:** Install Go from [go.dev/dl](https://go.dev/dl/) and ensure `%GOPATH%\bin` is on your `PATH`.

**Command Prompt (cmd.exe):**

```cmd
:: Build
go build -o bin\torrent-client.exe .\cmd\torrent-client\

:: Run .torrent
bin\torrent-client.exe path\to\file.torrent

:: Run magnet link (quote the full URI)
bin\torrent-client.exe "magnet:?xt=urn:btih:AABBCCDD...&tr=udp://tracker.opentrackr.org:1337/announce"
```

**PowerShell:**

```powershell
# Build
go build -o bin\torrent-client.exe .\cmd\torrent-client\

# Run .torrent
.\bin\torrent-client.exe path\to\file.torrent

# Run magnet link
.\bin\torrent-client.exe "magnet:?xt=urn:btih:AABBCCDD...&tr=udp://tracker.opentrackr.org:1337/announce"
```

> **Note:** Windows Defender SmartScreen may prompt on first run. Click **More info → Run anyway**, or right-click the `.exe` → **Properties → Unblock**.

> **Note:** Output files are saved to `%USERPROFILE%\Downloads\` on Windows.

---

## Usage

### Download from a `.torrent` file

```bash
./bin/torrent-client path/to/file.torrent
```

Optional runtime flags:

```bash
./bin/torrent-client --quiet path/to/file.torrent
./bin/torrent-client --verbose path/to/file.torrent
./bin/torrent-client --listen-port 51413 path/to/file.torrent
./bin/torrent-client --enable-nat=false path/to/file.torrent   # disable port mapping
```

**Example:**

```bash
./bin/torrent-client ubuntu-24.04.iso.torrent
```

**What happens:**

1. Parse torrent file (bencode) — extracts info hash, piece hashes, trackers, file list
2. Load existing resume state (if present) and verify completed pieces from disk
3. Start inbound listener on the configured `--listen-port`
4. Contact HTTP/UDP trackers (plus DHT only when torrent is not private) to seed the peer pool
5. Announce tracker lifecycle (`started`, periodic progress announces, `completed`/`stopped`)
6. Start adaptive workers with long-lived peer sessions and pipelined block requests
7. Continue peer discovery in the background while downloading
8. SHA-1 verify every piece, write directly to output offsets, and persist state
9. Enter endgame mode near completion to finish tail pieces faster

Notes:

- Non-private torrents automatically include a curated public UDP tracker fallback list.
- Startup discovery uses multiple tracker rounds with retries and deduplicates discovered peers.
- Runtime discovery repeats tracker announces and DHT rounds to continually refresh the peer pool.

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

# Bare magnet (public UDP tracker fallback + DHT)
./bin/torrent-client "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD"

# Magnet with peer hints (x.pe)
./bin/torrent-client "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD&x.pe=203.0.113.10:51413"
```

**What happens (extra steps vs .torrent):**

- If trackers are listed: contacts all of them (HTTP + UDP)
- If no trackers are listed: seeds discovery with the built-in public UDP tracker fallback list before falling back to DHT
- If `x.pe` peer hints are present: seeds those peers immediately
- If no trackers, or fewer than 5 peers found: runs a DHT (BEP 5) Kademlia lookup against public bootstrap nodes to find peers
- Once peers are found: fetches the full torrent metadata from the swarm using BEP 9 (`ut_metadata`) with parallel peer probes and verifies it against the info hash
- If fetched metadata marks the torrent as private (`private=1`): DHT is disabled for the download phase
- Then proceeds identically to a .torrent file download

---

## Download Location

Files are saved to:

| Scenario | Output path |
|---|---|
| Single-file torrent with name | `$HOME/Downloads/<filename>` |
| Multi-file torrent | `$HOME/Downloads/<torrent-name>/...` (full file tree restored) |
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
                    │     (disabled for private torrents)         │
                    └───────────────────────┬────────────────────┘
                                            │
                    ┌───────────────────────▼────────────────────┐
                    │  BEP 10 Extension Handshake (magnet only)   │
                    │  BEP 9  ut_metadata fetch → SHA-1 verify    │
                    └───────────────────────┬────────────────────┘
                                            │
                    ┌───────────────────────▼────────────────────┐
                    │         Download Engine                     │
                    │  • Resume state + piece verification        │
                    │  • Rarest-first scheduling                  │
                    │  • Long-lived sessions + pipelined requests │
                    │  • Adaptive peer scoring/backoff/quarantine │
                    │  • Inbound peer listener on configured port │
                    │  • Serves piece blocks to inbound peers     │
                    │  • Endgame duplication near completion      │
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
│   ├── peer/
│   │   ├── manager.go           # Background peer discovery manager (used by scheduler)
│   │   └── listener.go          # Inbound peer listener + handshake responder
│   ├── state/
│   │   └── state.go             # Persistent state helpers
│   ├── types/
│   │   ├── types.go             # Core data structures
│   │   └── parser.go            # .torrent & magnet link parsing
│   └── downloader/
│       └── downloader.go        # Resumable adaptive downloader + telemetry
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

