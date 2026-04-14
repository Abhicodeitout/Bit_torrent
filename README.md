# BitTorrent Client

A lightweight BitTorrent client written in Go that can download files from both torrent files and magnet links.

## Features

- **Torrent File Support**: Parse and download from `.torrent` files using bencode decoding
- **Magnet Link Support**: Parse magnet URIs and extract info hash and trackers
- **HTTP Tracker Support**: Communicate with HTTP trackers to discover peers
- **UDP Tracker Support**: Basic UDP tracker support (simplified implementation)
- **BitTorrent Wire Protocol**: Full implementation of the BitTorrent peer-to-peer protocol
  - Handshake negotiation
  - Message handling (interested, choke, unchoke, request, piece, etc.)
  - Piece validation using SHA-1 hashing
- **Multi-threaded Downloading**: Download pieces concurrently from multiple peers
- **Piece Validation**: Verify downloaded pieces against their SHA-1 hashes

## Prerequisites

- **Go 1.23.0 or higher** installed on your system
- **git** (optional, for cloning the repository)

## Installation & Setup

### Step 1: Navigate to the Project Directory

```bash
cd /workspaces/Bit_torrent
```

### Step 2: Install Dependencies

```bash
go mod download
```

This will download the required dependency: `github.com/jackpal/bencode-go`

### Step 3: Build the Executable

```bash
go build -o torrent-client
```

This creates an executable file named `torrent-client` in the current directory.

### Step 4: Verify Installation

```bash
./torrent-client
```

You should see the usage message if the build was successful.

## Execution Process

### Method 1: Download from a Torrent File

```bash
./torrent-client path/to/file.torrent
```

**Example:**
```bash
./torrent-client big-buck-bunny.torrent
```

**Process Flow:**
1. Client reads and parses the `.torrent` file using bencode
2. Extracts info hash, piece list, and tracker information
3. Contacts the tracker to get available peers
4. Connects to peers and performs BitTorrent handshake
5. Downloads file pieces concurrently (4 simultaneous connections)
6. Validates each piece using SHA-1 hash
7. Assembles all pieces into the final file
8. Saves the file to `$HOME/[filename]` or `$HOME/downloaded_[hash]`

### Method 2: Download from a Magnet Link

```bash
./torrent-client "magnet:?xt=urn:btih:..."
```

**Example:**
```bash
./torrent-client "magnet:?xt=urn:btih:8F7C6B1559607AFA3A4CEFB1836E9E8415E3355F&dn=Justin+Beiber+-All+That+Matters"
```

**Or from the provided file:**
```bash
./torrent-client "$(cat magnets.txt | head -1)"
```

**Process Flow:**
1. Client parses the magnet URI
2. Extracts info hash and tracker list from the magnet link
3. Contacts trackers to discover peers (supports HTTP and UDP trackers)
4. Proceeds with the same peer connection and download process as torrent files
5. Saves the downloaded file

## Step-by-Step Guide to Run

### Quick Start (Using Test Files)

```bash
# Step 1: Navigate to project
cd /workspaces/Bit_torrent

# Step 2: Build (if not already built)
go build -o torrent-client

# Step 3a: Download using torrent file
./torrent-client big-buck-bunny.torrent

# OR Step 3b: Download using magnet link
./torrent-client "$(cat magnets.txt | head -1)"
```

### Using Custom Files

```bash
# Download from your own torrent file
./torrent-client /path/to/your/file.torrent

# Download from a magnet link you have
./torrent-client "magnet:?xt=urn:btih:YOUR_INFO_HASH&tr=http://tracker.example.com"
```

## Output

The downloaded file will be saved to:
- **For single-file torrents:** `$HOME/[original-filename]`
- **For multi-file torrents:** `$HOME/[first-file-name]`
- **Fallback:** `$HOME/downloaded_[HASH]` if name unavailable

**Example outputs:**
```
/home/user/Downloaded_Movie.mp4
/home/user/document.pdf
/home/user/downloaded_8f7c6b155960
```

## Project Structure

- **client.go**: Main entry point, torrent file parsing, and magnet link parsing
- **types.go**: Data structures for torrent metadata, peers, and magnet links
- **tracker.go**: Communication with HTTP and UDP trackers
- **downloader.go**: Piece downloading logic and file assembly
- **protocol.go**: BitTorrent wire protocol implementation
- **go.mod**: Go module file with dependencies

## Dependencies

- `github.com/jackpal/bencode-go`: Bencode encoding/decoding for torrent files

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
./torrent-client big-buck-bunny.torrent
```

Or with the magnet link from `magnets.txt`:

```bash
./torrent-client "$(cat magnets.txt | head -1)"
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
