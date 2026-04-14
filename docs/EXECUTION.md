# BitTorrent Client — Execution Guide

## Quick Start (30 seconds)

```bash
git clone https://github.com/Abhicodeitout/Bit_torrent.git
cd Bit_torrent
go build -o bin/torrent-client ./cmd/torrent-client/
./bin/torrent-client your-file.torrent
```

---

## Full Step-by-Step

### Step 1 — Prerequisites

```bash
go version
# need: go1.23.0 or higher
```

If not installed: https://go.dev/dl/

### Step 2 — Get the code

```bash
git clone https://github.com/Abhicodeitout/Bit_torrent.git
cd Bit_torrent
```

### Step 3 — Install dependencies

```bash
go mod download
go mod verify
```

### Step 4 — Build

```bash
go build -o bin/torrent-client ./cmd/torrent-client/
```

Verify:

```bash
./bin/torrent-client
# Usage: torrent-client <path-to-torrent-file-or-magnet-link>
```

---

## Running — Option A: Torrent File

```bash
./bin/torrent-client path/to/file.torrent
```

Example console output:

```
Torrent: InfoHash=8f7c6b1559607afa...  Pieces=8777  Size=367008767 bytes
Tracker udp://tracker.opentrackr.org:1337/announce: 45 peers
Starting download from 45 peers...
Downloading 8777 pieces from 45 peers
Downloaded piece 0 from 93.184.216.34
Downloaded piece 1 from 104.21.33.91
...
All pieces downloaded, assembling file...
File assembled successfully at: /home/user/Downloads/big_buck_bunny.mp4
```

---

## Running — Option B: Magnet Link with Trackers

```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>&dn=<NAME>&tr=udp://tracker.opentrackr.org:1337/announce"
```

Example console output:

```
Magnet: InfoHash=8f7c6b1559607afa...  Name="Big Buck Bunny"  Trackers=3
Tracker udp://tracker.opentrackr.org:1337/announce: 38 peers
Found 38 peers — fetching torrent metadata via BEP 9...
Metadata from 93.184.216.34: Fetched metadata
Metadata ready: 8777 pieces, 367008767 bytes total
Starting download from 38 peers...
...
File assembled successfully at: /home/user/Downloads/big_buck_bunny.mp4
```

---

## Running — Option C: Bare Magnet Link (No Trackers — DHT)

```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>"
```

Example console output:

```
Magnet: InfoHash=8f7c6b1559607afa...  Name=""  Trackers=0
No peers from trackers — trying DHT (BEP 5)...
DHT: discovered 28 peers
Found 28 peers — fetching torrent metadata via BEP 9...
Metadata ready: 8777 pieces, 367008767 bytes total
Starting download from 28 peers...
```

---

## Full Flow Diagram

```
User Input (.torrent file or magnet link)
              │
              ├─ .torrent ──► bencode decode ──► TorrentInfo ready
              │
              └─ magnet ────► parse URI
                                    │
                         ┌──────────▼──────────┐
                         │   Peer Discovery     │
                         │  1. HTTP trackers    │
                         │  2. UDP trackers     │
                         │  3. DHT (fallback)   │
                         └──────────┬──────────┘
                                    │
                         ┌──────────▼──────────┐
                         │  BEP 10 ext shake   │  (magnet only)
                         │  BEP 9  ut_metadata │
                         │  SHA-1 verify       │
                         └──────────┬──────────┘
                                    │
                         ┌──────────▼──────────┐
                         │  Download Engine    │
                         │  4 parallel peers   │
                         │  16 KB blocks       │
                         │  SHA-1 per piece    │
                         │  auto-retry         │
                         └──────────┬──────────┘
                                    │
                          $HOME/Downloads/<file>
```

---

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| `go: command not found` | Go not installed | Install from https://go.dev/dl/ |
| `No peers found` | All trackers offline + DHT dry | Try a more popular torrent, or check internet |
| `metadata SHA1 mismatch` | Corrupted data from peer | Client retries next peer automatically |
| `piece X hash mismatch` | Bad block from peer | Piece is re-queued automatically |
| `dial tcp: connection refused` | Peer offline | Expected — many peers are tried |

---

## Cross-Platform Build

```bash
# Linux (default)
go build -o bin/torrent-client ./cmd/torrent-client/

# macOS
GOOS=darwin GOARCH=arm64 go build -o bin/torrent-client-mac ./cmd/torrent-client/

# Windows
GOOS=windows GOARCH=amd64 go build -o bin/torrent-client.exe ./cmd/torrent-client/
```


## Execution Flow Diagram

```
User Input (Torrent File or Magnet Link)
        ↓
[client.go] Parse Input
        ↓
    ├─ Torrent File? → Decode Bencode → Extract Info Hash & Trackers
    │
    └─ Magnet Link? → Parse URI → Extract Info Hash & Trackers
        ↓
[tracker.go] Contact Tracker(s)
        ↓
Get Peer List (IP:Port pairs)
        ↓
[downloader.go] Multi-threaded Download
        ↓
For each Piece:
    │
    ├─ [protocol.go] Handshake with Peer
    ├─ Send Interested Message
    ├─ Wait for Unchoke
    ├─ Request Blocks (16 KB each)
    └─ Validate SHA-1 Hash
        ↓
[downloader.go] Assemble File
        ↓
Save to $HOME/Downloads/[filename]
        ↓
Download Complete
```

---

## Real-World Execution Examples

### Example 1: Download Big Buck Bunny

```bash
$ cd /workspaces/Bit_torrent
$ ./bin/torrent-client big-buck-bunny.torrent

Parsing torrent file...
Torrent parsed. InfoHash: 8f7c6b1559607afa3a4cefb1836e9e8415e3355f
Files: 367008767 bytes
Pieces: 8777
Contacting tracker...
Tracker returned 45 peers
Found 45 peers, starting download...

Downloading 8777 pieces from 45 peers
Downloaded piece 0 from 192.168.1.100
Downloaded piece 1 from 192.168.1.101
Downloaded piece 2 from 192.168.1.102
...
Downloaded piece 8776 from 192.168.1.120
All pieces downloaded, assembling file...
Wrote piece 0 (262144 bytes)
Wrote piece 1 (262144 bytes)
...
File assembled successfully at: /home/user/Downloads/big_buck_bunny.mp4
```

---

### Example 2: Download from Magnet Link

```bash
$ ./bin/torrent-client "magnet:?xt=urn:btih:8F7C6B1559607AFA3A4CEFB1836E9E8415E3355F&dn=Big+Buck+Bunny"

Parsing magnet link...
Magnet link parsed. InfoHash: 8f7c6b1559607afa3a4cefb1836e9e8415e3355f
Name: Big Buck Bunny
Trackers: 15
Contacting tracker: http://p4p.arenabg.com:1337/announce
Found 42 peers
Found 42 peers, starting download...

Downloading 8777 pieces from 42 peers
Downloaded piece 0 from 192.168.1.50
...
File assembled successfully at: /home/user/Downloads/Big+Buck+Bunny.mp4
```

---

## Troubleshooting

### Issue: "go: command not found"
**Solution:** Install Go from https://golang.org/dl/

### Issue: "Port already in use"
**Solution:** The default port 6881 may be in use. The client will continue with other ports.

### Issue: "No peers found"
**Solution:** 
- Check your internet connection
- Try a different torrent/magnet link
- Some trackers may be offline

### Issue: "Hash mismatch"
**Solution:** 
- Your connection may be unstable
- Try downloading again
- The peer may have corrupted data

### Issue: Permission denied
**Solution:** Make the binary executable:
```bash
chmod +x torrent-client
```

---

## Performance Tips

### Speed up Downloads
1. Use torrents with more seeders (peers with complete files)
2. Increase `numGoroutine` value in [downloader.go](downloader.go) (default: 4)
   ```go
   const numGoroutine = 8  // Download from 8 peers concurrently
   ```

### Monitor Progress
The client logs each piece download:
```bash
./bin/torrent-client file.torrent | tee download.log
```

### Check Available Peers
```bash
./bin/torrent-client file.torrent 2>&1 | grep "Found.*peers"
```

---

## Advanced Options

### Build with Optimizations

```bash
# Release build (smaller binary, faster)
go build -ldflags="-s -w" -o bin/torrent-client ./cmd/torrent-client/

# Debug build (includes symbols for debugging)
go build -gcflags="all=-N -l" -o bin/torrent-client-debug ./cmd/torrent-client/
```

### Cross-Compilation

```bash
# Build for Linux
GOOS=linux GOARCH=amd64 go build -o bin/torrent-client-linux ./cmd/torrent-client/

# Build for macOS
GOOS=darwin GOARCH=amd64 go build -o bin/torrent-client-mac ./cmd/torrent-client/

# Build for Windows
GOOS=windows GOARCH=amd64 go build -o bin/torrent-client.exe ./cmd/torrent-client/
```

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

**The code is yours to use, modify, and distribute!** ✅

---

## Support & Contributions

Report issues or suggest improvements on GitHub or by creating issues in this repository.

**Happy downloading!** 🚀
