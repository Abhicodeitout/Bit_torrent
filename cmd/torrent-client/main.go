package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"torrent-client/internal/dht"
	"torrent-client/internal/downloader"
	peerpkg "torrent-client/internal/peer"
	"torrent-client/internal/protocol"
	"torrent-client/internal/tracker"
	"torrent-client/internal/types"
)

var publicTrackerFallback = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.openbittorrent.com:6969/announce",
	"udp://tracker.dler.org:6969/announce",
}

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

	var downloadedBytes atomic.Int64
	var uploadedBytes atomic.Int64
	var leftBytes atomic.Int64

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

		peers = gatherPeers(magnet.InfoHash, trackerURLs, torrentFile, peerID, opts.ListenPort, "started")
		if len(magnet.PeerAddrs) > 0 {
			for _, raw := range magnet.PeerAddrs {
				if p, ok := parsePeerAddr(raw); ok {
					peers = append(peers, p)
				}
			}
			peers = dedupePeers(peers)
		}
		if len(peers) == 0 {
			fmt.Println("No peers from trackers — trying DHT (BEP 5)...")
			dhtPeers, err := discoverDHTPeers(magnet.InfoHash, 2, 50, 30*time.Second)
			if err != nil || len(dhtPeers) == 0 {
				fmt.Println("DHT:", err)
			} else {
				peers = append(peers, dhtPeers...)
			}
		} else if len(peers) < 5 {
			fmt.Printf("Only %d tracker peer(s) — also querying DHT...\n", len(peers))
			dhtPeers, _ := discoverDHTPeers(magnet.InfoHash, 2, 50, 20*time.Second)
			peers = append(peers, dhtPeers...)
		}
		peers = dedupePeers(peers)
		fmt.Printf("Found %d peers — fetching torrent metadata via BEP 9...\n", len(peers))

		info, err := protocol.FetchMetadataFromPeers(peers, magnet.InfoHash, peerID)
		if err != nil {
			fmt.Println("Failed to fetch metadata:", err)
			os.Exit(1)
		}
		torrentFile.Info = *info
		leftBytes.Store(info.Length)
		fmt.Printf("Metadata ready: %d pieces, %d bytes total\n",
			len(info.PieceHashes), info.Length)
		if info.Private {
			fmt.Println("Metadata indicates private torrent; DHT discovery is disabled for download phase.")
		}

		provider, _ := downloader.OpenSeedStore(torrentFile)
		if provider != nil {
			defer provider.Close() //nolint:errcheck
		}
		go func() {
			err := peerpkg.StartInboundListener(ctx, magnet.InfoHash, peerID, opts.ListenPort, peerpkg.ListenerOptions{
				Verbose:  opts.Verbose,
				Provider: provider,
				OnUploaded: func(bytes int) {
					uploadedBytes.Add(int64(bytes))
				},
			})
			if err != nil && opts.Verbose {
				fmt.Printf("Inbound listener error: %v\n", err)
			}
		}()

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
		leftBytes.Store(torrentFile.Info.Length)

		trackers := buildTrackerList(torrentFile)
		trackerURLs = trackers
		provider, _ := downloader.OpenSeedStore(torrentFile)
		if provider != nil {
			defer provider.Close() //nolint:errcheck
		}
		go func() {
			err := peerpkg.StartInboundListener(ctx, torrentFile.InfoHash, peerID, opts.ListenPort, peerpkg.ListenerOptions{
				Verbose:  opts.Verbose,
				Provider: provider,
				OnUploaded: func(bytes int) {
					uploadedBytes.Add(int64(bytes))
				},
			})
			if err != nil && opts.Verbose {
				fmt.Printf("Inbound listener error: %v\n", err)
			}
		}()
		peers = gatherPeers(torrentFile.InfoHash, trackers, torrentFile, peerID, opts.ListenPort, "started")
		if len(peers) < 5 && !torrentFile.Info.Private {
			fmt.Printf("Only %d tracker peer(s) — also querying DHT...\n", len(peers))
			dhtPeers, _ := discoverDHTPeers(torrentFile.InfoHash, 2, 50, 20*time.Second)
			peers = append(peers, dhtPeers...)
		} else if len(peers) < 5 && torrentFile.Info.Private {
			fmt.Printf("Only %d tracker peer(s) and torrent is private; skipping DHT.\n", len(peers))
		}
		peers = dedupePeers(peers)
	}

	if len(peers) == 0 {
		fmt.Println("No peers found. Cannot download.")
		os.Exit(1)
	}

	opts.ProgressHook = func(downloaded, left int64) {
		downloadedBytes.Store(downloaded)
		leftBytes.Store(left)
	}

	if len(trackerURLs) > 0 {
		go func() {
			ticker := time.NewTicker(4 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					announceLifecycleEventWithStats(
						trackerURLs,
						torrentFile,
						peerID,
						opts.ListenPort,
						"",
						downloadedBytes.Load(),
						uploadedBytes.Load(),
						leftBytes.Load(),
					)
				}
			}
		}()
	}

	fmt.Printf("Starting download from %d peers...\n", len(peers))
	defer announceLifecycleEventWithStats(
		trackerURLs,
		torrentFile,
		peerID,
		opts.ListenPort,
		"stopped",
		downloadedBytes.Load(),
		uploadedBytes.Load(),
		leftBytes.Load(),
	)
	if err := downloader.DownloadTorrentWithOptions(torrentFile, peers, peerID, opts); err != nil {
		fmt.Println("Download failed:", err)
		os.Exit(1)
	}
	announceLifecycleEventWithStats(
		trackerURLs,
		torrentFile,
		peerID,
		opts.ListenPort,
		"completed",
		downloadedBytes.Load(),
		uploadedBytes.Load(),
		0,
	)
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
	if !tf.Info.Private {
		for _, tr := range publicTrackerFallback {
			add(tr)
		}
	}
	return list
}

// gatherPeers contacts every tracker URL in order, collecting peers from each
// and stopping early once 50 or more distinct peers have been found.
func gatherPeers(infoHash [20]byte, trackers []string, tf *types.TorrentFile, peerID [20]byte, port uint16, event string) []types.Peer {
	const (
		targetPeers    = 80
		maxRounds      = 3
		trackerRetries = 2
	)

	var peers []types.Peer
	for round := 0; round < maxRounds && len(peers) < targetPeers; round++ {
		for _, tr := range trackers {
			req := tracker.AnnounceRequest{
				InfoHash: infoHash,
				PeerID:   peerID,
				Port:     port,
				Left:     tf.Info.Length,
				Event:    event,
			}

			newPeers, err := announceTrackerWithRetries(tr, tf, req, trackerRetries)
			if err != nil {
				fmt.Printf("Tracker %s: %v\n", tr, err)
				continue
			}
			fmt.Printf("Tracker %s: %d peers\n", tr, len(newPeers))
			peers = dedupePeers(append(peers, newPeers...))
			if len(peers) >= targetPeers {
				break
			}
		}
		if len(peers) < targetPeers {
			time.Sleep(1200 * time.Millisecond)
		}
	}

	return peers
}

func announceTrackerWithRetries(tr string, tf *types.TorrentFile, req tracker.AnnounceRequest, retries int) ([]types.Peer, error) {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		var (
			newPeers []types.Peer
			err      error
		)
		switch {
		case strings.HasPrefix(tr, "udp://"):
			newPeers, err = tracker.AnnounceUDPWithRequest(tr, req)
		case strings.HasPrefix(tr, "http://"), strings.HasPrefix(tr, "https://"):
			tfCopy := *tf
			tfCopy.Announce = tr
			newPeers, err = tracker.AnnounceToHTTPTrackerWithRequest(&tfCopy, req)
		default:
			return nil, fmt.Errorf("unsupported tracker scheme")
		}
		if err == nil {
			return newPeers, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 450 * time.Millisecond)
	}
	return nil, lastErr
}

func discoverDHTPeers(infoHash [20]byte, rounds, maxPeers int, timeout time.Duration) ([]types.Peer, error) {
	var peers []types.Peer
	var lastErr error
	for i := 0; i < rounds; i++ {
		pp, err := dht.GetPeers(infoHash, maxPeers, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		peers = dedupePeers(append(peers, pp...))
		if len(peers) >= maxPeers {
			return peers, nil
		}
	}
	if len(peers) > 0 {
		return peers, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no DHT peers found")
	}
	return nil, lastErr
}

func dedupePeers(peers []types.Peer) []types.Peer {
	seen := make(map[string]struct{}, len(peers))
	out := make([]types.Peer, 0, len(peers))
	for _, p := range peers {
		if p.IP == nil || p.Port == 0 {
			continue
		}
		key := net.JoinHostPort(p.IP.String(), fmt.Sprintf("%d", p.Port))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
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

func announceLifecycleEventWithStats(trackers []string, tf *types.TorrentFile, peerID [20]byte, port uint16, event string, downloaded, uploaded, left int64) {
	if tf == nil || len(trackers) == 0 {
		return
	}
	req := tracker.AnnounceRequest{
		InfoHash:   tf.InfoHash,
		PeerID:     peerID,
		Port:       port,
		Downloaded: downloaded,
		Uploaded:   uploaded,
		Left:       left,
		Event:      event,
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
