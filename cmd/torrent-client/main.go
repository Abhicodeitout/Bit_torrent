package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"torrent-client/internal/dht"
	"torrent-client/internal/downloader"
	peerpkg "torrent-client/internal/peer"
	"torrent-client/internal/protocol"
	"torrent-client/internal/tracker"
	"torrent-client/internal/types"
)

func main() {
	fs := flag.NewFlagSet("torrent-client", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "suppress downloader progress/stats output")
	verbose := fs.Bool("verbose", false, "force verbose downloader progress/stats output")
	listenPort := fs.Uint("listen-port", 6881, "tcp port to announce and listen on")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: torrent-client [--quiet|--verbose] [--listen-port <port>] <path-to-torrent-file-or-magnet-link>\n")
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
	opts.ListenPort = uint16(*listenPort)
	peerID := types.GeneratePeerID()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var torrentFile *types.TorrentFile
	var peers []types.Peer
	var trackerURLs []string

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
		trackerURLs = magnet.Trackers

		go func() {
			if err := peerpkg.StartInboundListener(ctx, magnet.InfoHash, peerID, opts.ListenPort, opts.Verbose); err != nil && opts.Verbose {
				fmt.Printf("Inbound listener error: %v\n", err)
			}
		}()

		peers = gatherPeers(magnet.InfoHash, trackerURLs, torrentFile, peerID, opts.ListenPort, "started")
		if len(magnet.PeerAddrs) > 0 {
			for _, raw := range magnet.PeerAddrs {
				if p, ok := parsePeerAddr(raw); ok {
					peers = append(peers, p)
				}
			}
		}
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
		if info.Private {
			fmt.Println("Metadata indicates private torrent; DHT discovery is disabled for download phase.")
		}

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
		trackerURLs = trackers
		go func() {
			if err := peerpkg.StartInboundListener(ctx, torrentFile.InfoHash, peerID, opts.ListenPort, opts.Verbose); err != nil && opts.Verbose {
				fmt.Printf("Inbound listener error: %v\n", err)
			}
		}()
		peers = gatherPeers(torrentFile.InfoHash, trackers, torrentFile, peerID, opts.ListenPort, "started")
		if len(peers) < 5 && !torrentFile.Info.Private {
			fmt.Printf("Only %d tracker peer(s) — also querying DHT...\n", len(peers))
			dhtPeers, _ := dht.GetPeers(torrentFile.InfoHash, 50, 30*time.Second)
			peers = append(peers, dhtPeers...)
		} else if len(peers) < 5 && torrentFile.Info.Private {
			fmt.Printf("Only %d tracker peer(s) and torrent is private; skipping DHT.\n", len(peers))
		}
	}

	if len(peers) == 0 {
		fmt.Println("No peers found. Cannot download.")
		os.Exit(1)
	}

	fmt.Printf("Starting download from %d peers...\n", len(peers))
	defer announceLifecycleEvent(trackerURLs, torrentFile, peerID, opts.ListenPort, "stopped", 0)
	if err := downloader.DownloadTorrentWithOptions(torrentFile, peers, peerID, opts); err != nil {
		fmt.Println("Download failed:", err)
		os.Exit(1)
	}
	announceLifecycleEvent(trackerURLs, torrentFile, peerID, opts.ListenPort, "completed", 0)
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
func gatherPeers(infoHash [20]byte, trackers []string, tf *types.TorrentFile, peerID [20]byte, port uint16, event string) []types.Peer {
	var peers []types.Peer
	for _, tr := range trackers {
		var (
			newPeers []types.Peer
			err      error
		)
		req := tracker.AnnounceRequest{
			InfoHash: infoHash,
			PeerID:   peerID,
			Port:     port,
			Left:     tf.Info.Length,
			Event:    event,
		}
		switch {
		case strings.HasPrefix(tr, "udp://"):
			newPeers, err = tracker.AnnounceUDPWithRequest(tr, req)
		case strings.HasPrefix(tr, "http://"), strings.HasPrefix(tr, "https://"):
			tfCopy := *tf
			tfCopy.Announce = tr
			newPeers, err = tracker.AnnounceToHTTPTrackerWithRequest(&tfCopy, req)
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

func parsePeerAddr(raw string) (types.Peer, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return types.Peer{}, false
	}
	addr, err := net.ResolveTCPAddr("tcp", raw)
	if err != nil || addr == nil || addr.IP == nil || addr.Port <= 0 || addr.Port > 65535 {
		return types.Peer{}, false
	}
	return types.Peer{IP: addr.IP, Port: uint16(addr.Port)}, true
}

func announceLifecycleEvent(trackers []string, tf *types.TorrentFile, peerID [20]byte, port uint16, event string, left int64) {
	if tf == nil || len(trackers) == 0 {
		return
	}
	req := tracker.AnnounceRequest{
		InfoHash: tf.InfoHash,
		PeerID:   peerID,
		Port:     port,
		Left:     left,
		Event:    event,
	}
	for _, tr := range trackers {
		switch {
		case strings.HasPrefix(tr, "udp://"):
			_, _ = tracker.AnnounceUDPWithRequest(tr, req)
		case strings.HasPrefix(tr, "http://"), strings.HasPrefix(tr, "https://"):
			tfCopy := *tf
			tfCopy.Announce = tr
			_, _ = tracker.AnnounceToHTTPTrackerWithRequest(&tfCopy, req)
		}
	}
}
