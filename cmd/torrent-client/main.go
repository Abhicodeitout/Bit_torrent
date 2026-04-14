package main

import (
	"fmt"
	"os"
	"strings"

	"torrent-client/internal/downloader"
	"torrent-client/internal/tracker"
	"torrent-client/internal/types"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: torrent-client <path-to-torrent-file-or-magnet-link>")
		return
	}

	input := os.Args[1]
	var torrentFile *types.TorrentFile
	var peerID [20]byte
	var peers []types.Peer

	// Check if input is a magnet link or torrent file
	if strings.HasPrefix(input, "magnet:") {
		fmt.Println("Parsing magnet link...")
		magnet, err := types.ParseMagnetLink(input)
		if err != nil {
			fmt.Println("Failed to parse magnet link:", err)
			return
		}

		fmt.Printf("Magnet link parsed. InfoHash: %x\nName: %s\nTrackers: %d\n",
			magnet.InfoHash, magnet.Name, len(magnet.Trackers))

		if len(magnet.Trackers) > 0 {
			peerID = types.GeneratePeerID()

			// Create a minimal TorrentFile for tracker communication
			torrentFile = &types.TorrentFile{
				Announce: magnet.Trackers[0],
				InfoHash: magnet.InfoHash,
				Info: types.TorrentInfo{
					Length: 0, // Unknown from magnet link
				},
			}

			for _, tr := range magnet.Trackers {
				fmt.Printf("Contacting tracker: %s\n", tr)
				if strings.HasPrefix(tr, "udp://") {
					trackerHost := strings.TrimPrefix(tr, "udp://")
					if newPeers, err := tracker.AnnounceUDP(trackerHost, magnet.InfoHash, peerID, 6881); err == nil {
						peers = append(peers, newPeers...)
					}
				} else if strings.HasPrefix(tr, "http://") || strings.HasPrefix(tr, "https://") {
					torrentFile.Announce = tr
					if newPeers, err := tracker.AnnounceToHTTPTracker(torrentFile, peerID); err == nil {
						peers = append(peers, newPeers...)
					}
				}
			}
		}
	} else {
		// It's a torrent file
		fmt.Println("Parsing torrent file...")
		var err error
		torrentFile, err = types.OpenTorrentFile(input)
		if err != nil {
			fmt.Println("Failed to open torrent file:", err)
			return
		}

		fmt.Printf("Torrent parsed. InfoHash: %x\nFiles: %d bytes\nPieces: %d\n",
			torrentFile.InfoHash, torrentFile.Info.Length, len(torrentFile.Info.PieceHashes))

		// Generate a peer ID
		peerID = types.GeneratePeerID()

		// Announce to the tracker and get the list of peers
		fmt.Println("Contacting tracker...")
		peers, err = tracker.AnnounceToHTTPTracker(torrentFile, peerID)
		if err != nil {
			fmt.Println("Failed to get peers from tracker:", err)
			return
		}
	}

	if len(peers) == 0 {
		fmt.Println("No peers found")
		return
	}

	fmt.Printf("Found %d peers, starting download...\n", len(peers))
	if err := downloader.DownloadTorrent(torrentFile, peers, peerID); err != nil {
		fmt.Printf("Download error: %v\n", err)
	}
}
