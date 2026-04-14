#!/bin/bash

# Quick usage examples for the BitTorrent client

echo "BitTorrent Client - Usage Examples"
echo "==================================="
echo ""

# Example 1: Download from torrent file
echo "1. Download from a torrent file:"
echo "   ./torrent-client big-buck-bunny.torrent"
echo ""

# Example 2: Download from magnet link
echo "2. Download from a magnet link:"
MAGNET=$(cat magnets.txt | head -1)
echo "   ./torrent-client \"$MAGNET\""
echo ""

echo "The client will:"
echo "  1. Parse the torrent/magnet link"
echo "  2. Contact the tracker(s) to get peers"
echo "  3. Connect to peers and download pieces"
echo "  4. Validate each piece using SHA-1 hash"
echo "  5. Assemble the final file in your home directory"
echo ""

echo "Output files are saved to: \$HOME/[filename] or \$HOME/downloaded_[hash]"
