# BitTorrent Client — Universal Compatibility

This client works with any valid `.torrent` file or magnet link without any configuration changes.

---

## Why It Works for Any Torrent

### 1. Generic bencode parser
Every `.torrent` file uses the bencode format. The client decodes it generically — no torrent-specific logic, no hardcoded names or file types.

### 2. Three-layer peer discovery

| Layer | Protocol | Used when |
|---|---|---|
| HTTP tracker | BEP 3 | `http://` or `https://` announce URL present |
| UDP tracker | BEP 15 | `udp://` announce URL present |
| DHT (Kademlia) | BEP 5 | Fewer than 5 tracker peers found, or no trackers at all |

Even a bare `magnet:?xt=urn:btih:<hash>` with zero `tr=` parameters will find peers via DHT.

### 3. Metadata from peers (BEP 9 / BEP 10)
For magnet links, the full torrent info dictionary (piece hashes, file layout) is fetched from peers using the `ut_metadata` extension, then SHA-1 verified against the info hash. The client never blindly trusts unverified metadata.

### 4. Standard wire protocol
All downloading uses standardised protocol messages. The protocol is content-agnostic — it requests, verifies, and writes raw bytes.

---

## Supported File Types

| Category | Examples |
|---|---|
| Video | .mp4, .mkv, .avi, .mov |
| Audio | .mp3, .flac, .aac, .wav |
| Documents | .pdf, .docx, .epub |
| Archives | .zip, .tar.gz, .7z, .rar |
| Disk images | .iso, .img |
| Software | .deb, .rpm, .dmg, .exe |
| Source code | .go, .py, .c, .js |
| Multi-file folders | Any combination of the above |

---

## Usage Patterns

### Single torrent file
```bash
./bin/torrent-client ubuntu-24.04.iso.torrent
```

### Magnet with trackers
```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>&tr=udp://tracker.opentrackr.org:1337/announce"
```

### Bare magnet (DHT-only — no trackers needed)
```bash
./bin/torrent-client "magnet:?xt=urn:btih:<HASH>"
```

### Batch from torrent files
```bash
for f in *.torrent; do
    ./bin/torrent-client "$f"
done
```

### Batch from a magnet list file
```bash
while IFS= read -r magnet; do
    ./bin/torrent-client "$magnet"
done < magnets.txt
```

---

## Output

All downloads land in `$HOME/Downloads/`:

```
$HOME/Downloads/
    ├── ubuntu-24.04-desktop-amd64.iso
    ├── movie.mkv
    ├── downloaded_8f7c6b1559607afa   ← when name unavailable
    └── …
```

The directory is created automatically if it does not exist.
