package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"torrent-client/internal/dht"
	"torrent-client/internal/downloader"
	"torrent-client/internal/protocol"
	"torrent-client/internal/tracker"
	"torrent-client/internal/types"
)

func main() {
	fs := flag.NewFlagSet("torrent-client", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "suppress downloader progress/stats output")
	verbose := fs.Bool("verbose", false, "force verbose downloader progress/stats output")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: torrent-client [--quiet|--verbose] <path-to-torrent-file-or-magnet-link>\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Println("Failed to parse arguments:", err)
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	input := fs.Arg(0)
	opts := downloader.DefaultDownloadOptions()
	if *quiet {
		opts.Verbose = false
		opts.EnableStats = false
	}
	if *verbose {
		opts.Verbose = true
		opts.EnableStats = true
	}
	peerID := types.GeneratePeerID()

	var torrentFile *types.TorrentFile
	var peers []types.Peer

	if strings.HasPrefix(input, "magnet:") {
		// ── Magnet link ───────────────────────────────────────────────────────
		magnet, err := types.ParseMagnetLink(input)
		if err != nil {
			fmt.Println("Failed to parse magnet link:", err)
			os.Exit(1)
		}
		fmt.Printf("Magnet: InfoHash=%x  Name=%q  Trackers=%d\n",
			magnet.InfoHash, magnet.Name, len(magnet.Trackers))

		// Minimal struct used only for tracker HTTP announces (needs InfoHash).
		torrentFile = &types.TorrentFile{InfoHash: magnet.InfoHash}

		peers = gatherPeers(magnet.InfoHash, magnet.Trackers, torrentFile, peerID)
		if len(peers) == 0 {
			fmt.Println("No peers from trackers — trying DHT (BEP 5)...")
			dhtPeers, err := dht.GetPeers(magnet.InfoHash, 50, 45*time.Second)
			if err != nil {
				fmt.Println("DHT:", err)
			} else {
				peers = append(peers, dhtPeers...)
			}
		} else if len(peers) < 5 {
			fmt.Printf("Only %d tracker peer(s) — also querying DHT...\n", len(peers))
			dhtPeers, _ := dht.GetPeers(magnet.InfoHash, 50, 30*time.Second)
			peers = append(peers, dhtPeers...)
		}
		fmt.Printf("Found %d peers — fetching torrent metadata via BEP 9...\n", len(peers))

		info, err := protocol.FetchMetadataFromPeers(peers, magnet.InfoHash, peerID)
		if err != nil {
			fmt.Println("Failed to fetch metadata:", err)
			os.Exit(1)
		}
		torrentFile.Info = *info
		fmt.Printf("Metadata ready: %d pieces, %d bytes total\n",
			len(info.PieceHashes), info.Length)

	} else {
		// ── Torrent file ──────────────────────────────────────────────────────
		var err error
		torrentFile, err = types.OpenTorrentFile(input)
		if err != nil {
			fmt.Println("Failed to open torrent file:", err)
			os.Exit(1)
		}
		fmt.Printf("Torrent: InfoHash=%x  Pieces=%d  Size=%d bytes\n",
			torrentFile.InfoHash,
			len(torrentFile.Info.PieceHashes),
			torrentFile.Info.Length)

		trackers := buildTrackerList(torrentFile)
		peers = gatherPeers(torrentFile.InfoHash, trackers, torrentFile, peerID)
		if len(peers) < 5 {
			fmt.Printf("Only %d tracker peer(s) — also querying DHT...\n", len(peers))
			dhtPeers, _ := dht.GetPeers(torrentFile.InfoHash, 50, 30*time.Second)
			peers = append(peers, dhtPeers...)
		}
	}

	if len(peers) == 0 {
		fmt.Println("No peers found. Cannot download.")
		os.Exit(1)
	}

	fmt.Printf("Starting download from %d peers...\n", len(peers))
	if err := downloader.DownloadTorrentWithOptions(torrentFile, peers, peerID, opts); err != nil {
		fmt.Println("Download failed:", err)
		os.Exit(1)
	}
}

// buildTrackerList returns a deduplicated, flat list of all tracker URLs from a
// TorrentFile (Announce + every tier of AnnounceList).
func buildTrackerList(tf *types.TorrentFile) []string {
	seen := make(map[string]struct{})
	var list []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" {
			if _, dup := seen[u]; !dup {
				seen[u] = struct{}{}
				list = append(list, u)
			}
		}
	}
	add(tf.Announce)
	for _, tier := range tf.AnnounceList {
		for _, u := range tier {
			add(u)
		}
	}
	return list
}

// gatherPeers contacts every tracker URL in order, collecting peers from each
// and stopping early once 50 or more distinct peers have been found.
func gatherPeers(infoHash [20]byte, trackers []string, tf *types.TorrentFile, peerID [20]byte) []types.Peer {
	var peers []types.Peer
	for _, tr := range trackers {
		var (
			newPeers []types.Peer
			err      error
		)
		switch {
		case strings.HasPrefix(tr, "udp://"):
			newPeers, err = tracker.AnnounceUDP(tr, infoHash, peerID, 6881)
		case strings.HasPrefix(tr, "http://"), strings.HasPrefix(tr, "https://"):
			tfCopy := *tf
			tfCopy.Announce = tr
			newPeers, err = tracker.AnnounceToHTTPTracker(&tfCopy, peerID)
		default:
			continue // ws://, wss://, etc. not supported
		}
		if err != nil {
			fmt.Printf("Tracker %s: %v\n", tr, err)
			continue
		}
		fmt.Printf("Tracker %s: %d peers\n", tr, len(newPeers))
		peers = append(peers, newPeers...)
		if len(peers) >= 50 {
			break // enough to start
		}
	}
	return peers
}

