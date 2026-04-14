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
# Build with new modular structure
go build -o bin/torrent-client ./cmd/torrent-client/
```

This creates an executable file named `torrent-client` in the `bin/` directory.

### Step 4: Verify Installation

```bash
./bin/torrent-client
```

You should see the usage message if the build was successful.

## Execution Process

### Method 1: Download from a Torrent File

```bash
./bin/torrent-client path/to/file.torrent
```

**Example:**
```bash
./bin/torrent-client big-buck-bunny.torrent
```

**Process Flow:**
1. Client reads and parses the `.torrent` file using bencode
2. Extracts info hash, piece list, and tracker information
3. Contacts the tracker to get available peers
4. Connects to peers and performs BitTorrent handshake
5. Downloads file pieces concurrently (4 simultaneous connections)
6. Validates each piece using SHA-1 hash
7. Assembles all pieces into the final file
8. Saves the file to `$HOME/Downloads/[filename]` or `$HOME/Downloads/downloaded_[hash]`

### Method 2: Download from a Magnet Link

```bash
./bin/torrent-client "magnet:?xt=urn:btih:..."
```

**Example:**
```bash
./bin/torrent-client "magnet:?xt=urn:btih:8F7C6B1559607AFA3A4CEFB1836E9E8415E3355F&dn=Justin+Beiber+-All+That+Matters"
```

**Or from the provided file:**
```bash
./bin/torrent-client "$(cat magnets.txt | head -1)"
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
go build -o bin/torrent-client ./cmd/torrent-client/

# Step 3a: Download using torrent file
./bin/torrent-client big-buck-bunny.torrent

# OR Step 3b: Download using magnet link
./bin/torrent-client "$(cat magnets.txt | head -1)"
```

### Using Custom Files

```bash
# Download from your own torrent file
./bin/torrent-client /path/to/your/file.torrent

# Download from a magnet link you have
./bin/torrent-client "magnet:?xt=urn:btih:YOUR_INFO_HASH&tr=http://tracker.example.com"
```

## Output

The downloaded file will be saved to:
- **For single-file torrents:** `$HOME/Downloads/[original-filename]`
- **For multi-file torrents:** `$HOME/Downloads/[first-file-name]`
- **Fallback:** `$HOME/Downloads/downloaded_[HASH]` if name unavailable

**Example outputs:**
```
/home/user/Downloads/Downloaded_Movie.mp4
/home/user/Downloads/document.pdf
/home/user/Downloads/downloaded_8f7c6b155960
```

## Project Structure

The project follows Go conventions for clean, maintainable code organization:

```
Bit_torrent/
├── cmd/
│   └── torrent-client/
│       └── main.go              # Application entry point
├── internal/
│   ├── types/
│   │   ├── types.go            # Type definitions and data structures
│   │   └── parser.go           # Torrent/Magnet link parsing functions
│   ├── protocol/
│   │   └── protocol.go         # BitTorrent wire protocol implementation
│   ├── tracker/
│   │   └── tracker.go          # Tracker communication (HTTP/UDP)
│   └── downloader/
│       └── downloader.go       # Piece downloading and file assembly
├── bin/
│   └── torrent-client          # Compiled executable
├── docs/
│   ├── README.md              # Project overview (this file)
│   ├── EXECUTION.md           # Step-by-step execution guide
│   ├── UNIVERSAL_SUPPORT.md   # Universal compatibility details
│   ├── REFERENCE.md           # Complete reference guide
│   ├── INDEX.md               # Documentation index
│   └── examples/              
│       ├── EXAMPLES.sh        # Real-world usage examples
│       └── USAGE.sh           # Quick usage reference
├── LICENSE                     # MIT License
├── go.mod / go.sum            # Go module dependencies
├── magnets.txt                # Example magnet links
└── big-buck-bunny.torrent     # Test torrent file
```

### Folder Breakdown

**`cmd/`** - Command-line application entry point
- Contains only the main executable code
- Clean separation between app and library code

**`internal/`** - Internal packages (not importable by external code)
- `types/` - Data structures and torrent file parsing
  - `types.go` - Type definitions for Torrent, Peer, MagnetLink, etc.
  - `parser.go` - Functions for parsing .torrent files and magnet links
- `protocol/` - BitTorrent wire protocol
  - Handshake, message encoding/decoding, piece requests
- `tracker/` - Tracker communication
  - HTTP tracker announcement and peer discovery
  - UDP tracker support (simplified)
- `downloader/` - Download engine
  - Multi-threaded piece downloading
  - Hash validation and file assembly

**`bin/`** - Build output
- Compiled executable goes here
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
