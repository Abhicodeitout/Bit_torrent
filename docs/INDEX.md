# BitTorrent Client — Documentation Index

## Status: Production Ready

| Capability | Implemented |
|---|---|
| `.torrent` file parsing | Yes |
| HTTP tracker (BEP 3) | Yes |
| UDP tracker (BEP 15) | Yes — full connect/announce |
| DHT peer discovery (BEP 5) | Yes — Kademlia iterative lookup |
| Extension protocol (BEP 10) | Yes |
| Metadata from peers (BEP 9) | Yes — enables bare magnet downloads |
| Wire protocol | Yes |
| Piece SHA-1 validation | Yes |
| Concurrent download | Yes — 4 parallel peer connections |

---

## Documents

### [../README.md](../README.md) — Start here
Badges, full feature table, installation, usage examples for all three input types, architecture diagram, project structure.

### [README.md](README.md) — Technical overview
Detailed description of every feature and BEP implemented, full project structure breakdown, output location table.

### [EXECUTION.md](EXECUTION.md) — Step-by-step guide
Prerequisites, build instructions, example console output for all three modes (torrent file / magnet with trackers / bare magnet via DHT), troubleshooting table, cross-platform build commands.

### [REFERENCE.md](REFERENCE.md) — Architecture & internals
Package-by-package breakdown, BEP compliance table, data flow, peer discovery strategy.

### [UNIVERSAL_SUPPORT.md](UNIVERSAL_SUPPORT.md) — Compatibility
Why the client works with any torrent or magnet link, supported file types, batch usage.

---

## Project Layout

```
Bit_torrent/
├── cmd/torrent-client/main.go       # Entry point
├── internal/
│   ├── dht/dht.go                   # BEP 5 — DHT
│   ├── protocol/protocol.go         # Wire protocol
│   ├── protocol/metadata.go         # BEP 10 + BEP 9
│   ├── tracker/tracker.go           # HTTP + UDP trackers
│   ├── types/types.go               # Data structures
│   ├── types/parser.go              # .torrent + magnet parser
│   └── downloader/downloader.go     # Download engine
├── go.mod / go.sum
└── LICENSE
```
