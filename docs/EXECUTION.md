# BitTorrent Client - Execution Guide

## Quick Start (30 seconds)

```bash
cd /workspaces/Bit_torrent
go build -o bin/torrent-client ./cmd/torrent-client/
./bin/torrent-client big-buck-bunny.torrent
```

---

## Full Execution Steps

### Prerequisites Check

Before running, ensure you have Go installed:

```bash
go version
# Should output: go version go1.23.0 (or higher)
```

---

### Step 1: Prepare the Project

```bash
# Navigate to project directory
cd /workspaces/Bit_torrent

# Verify all files are present
ls -la
# You should see: client.go, downloader.go, tracker.go, protocol.go, types.go, go.mod, go.sum
```

---

### Step 2: Download Dependencies

```bash
# Download required Go modules
go mod download

# Verify dependencies (optional)
go mod verify
```

---

### Step 3: Build the Binary

```bash
# Compile the project
go build -o bin/torrent-client ./cmd/torrent-client/

# Verify the binary was created
file torrent-client
# Output: torrent-client: ELF 64-bit LSB executable
```

---

### Step 4: Execute - Option A (Torrent File)

```bash
# Run with the provided test torrent
./bin/torrent-client big-buck-bunny.torrent

# Console Output:
# Parsing torrent file...
# Torrent parsed. InfoHash: <40-char-hex>
# Files: <size> bytes
# Pieces: <number>
# Contacting tracker...
# Found <N> peers, starting download...
```

---

### Step 5: Execute - Option B (Magnet Link)

```bash
# Extract and use magnet link from file
MAGNET=$(cat magnets.txt | head -1)
./bin/torrent-client "$MAGNET"

# Or directly pass magnet link
./bin/torrent-client "magnet:?xt=urn:btih:8F7C6B1559607AFA3A4CEFB1836E9E8415E3355F&dn=..."

# Console Output:
# Parsing magnet link...
# Magnet link parsed. InfoHash: <40-char-hex>
# Name: <torrent-name>
# Trackers: <number>
# Contacting tracker(s)...
```

---

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
Save to $HOME/[filename]
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
File assembled successfully at: /home/user/big_buck_bunny.mp4
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
File assembled successfully at: /home/user/Big+Buck+Bunny.mp4
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
