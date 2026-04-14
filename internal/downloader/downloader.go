package downloader

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"torrent-client/internal/dht"
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

const (
	blockSize    = 16 * 1024 // 16 KB
	numGoroutine = 10
	connTimeout  = 10 * time.Second

	peerQueueCapacity = 512
	discoveryInterval = 25 * time.Second
	stateSaveInterval = 3 * time.Second

	maxPieceAttempts      = 12
	maxPiecesPerSession   = 8
	endgameThreshold      = 12
	endgameInjectInterval = 2 * time.Second
	statsLogInterval      = 10 * time.Second
)

type resumeState struct {
	Completed []bool `json:"completed"`
}

// DownloadOptions controls downloader runtime logging behavior.
type DownloadOptions struct {
	Verbose      bool
	EnableStats  bool
	ListenPort   uint16
	ProgressHook func(downloaded, left int64)
}

// DefaultDownloadOptions returns defaults suitable for interactive CLI runs.
func DefaultDownloadOptions() DownloadOptions {
	return DownloadOptions{
		Verbose:      true,
		EnableStats:  true,
		ListenPort:   6881,
		ProgressHook: nil,
	}
}

var (
	errPeerMissingPiece       = errors.New("peer missing requested piece")
	errPieceCancelledByOther  = errors.New("piece already completed by another worker")
)

// completionBus signals goroutines when a piece is completed by any worker.
// This enables endgame-mode cancel dispatch.
type completionBus struct {
	mu   sync.Mutex
	subs map[int][]chan struct{}
}

func newCompletionBus() *completionBus {
	return &completionBus{subs: make(map[int][]chan struct{})}
}

func (b *completionBus) subscribe(pieceIdx int) <-chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[pieceIdx] = append(b.subs[pieceIdx], ch)
	b.mu.Unlock()
	return ch
}

func (b *completionBus) complete(pieceIdx int) {
	b.mu.Lock()
	chans := b.subs[pieceIdx]
	delete(b.subs, pieceIdx)
	b.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// sessionLog writes structured JSON-newline events to a per-torrent log file.
type sessionLog struct {
	mu sync.Mutex
	w  *bufio.Writer
	f  *os.File
}

func openSessionLog(statePath string) *sessionLog {
	logPath := strings.TrimSuffix(statePath, ".state.json") + ".log"
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return &sessionLog{} // no-op if log file can't be created
	}
	return &sessionLog{w: bufio.NewWriter(f), f: f}
}

func (l *sessionLog) logEvent(event string, fields map[string]interface{}) {
	if l == nil || l.f == nil {
		return
	}
	entry := make(map[string]interface{}, len(fields)+2)
	entry["t"] = time.Now().UTC().Format(time.RFC3339)
	entry["event"] = event
	for k, v := range fields {
		entry[k] = v
	}
	data, _ := json.Marshal(entry)
	l.mu.Lock()
	_, _ = l.w.Write(data)
	_ = l.w.WriteByte('\n')
	_ = l.w.Flush()
	l.mu.Unlock()
}

func (l *sessionLog) close() {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	_ = l.w.Flush()
	_ = l.f.Close()
	l.mu.Unlock()
}

type peerSession struct {
	peer      types.Peer
	conn      net.Conn
	bitfield  []byte
	unchoked  bool
	servedCnt int
	utPexID   int
}

type peerStat struct {
	successes           int
	failures            int
	consecutiveFailures int
	bytes               int64
	latencyN            int64
	latencySum          time.Duration
	lastFailure         time.Time
	backoffUntil        time.Time
	quarantinedUntil    time.Time
}

type peerPool struct {
	mu        sync.Mutex
	peers     map[string]types.Peer
	available map[string]bool
	stats     map[string]*peerStat
}

type poolSnapshot struct {
	totalPeers      int
	availablePeers  int
	backoffPeers    int
	quarantinePeers int
	totalSuccesses  int
	totalFailures   int
	avgLatency      time.Duration
}

type outputPlan struct {
	dataPath    string
	statePath   string
	displayPath string
	rootDir     string
	isMultiFile bool
}

// SeedStore serves locally available piece data to inbound peers.
type SeedStore struct {
	torrent   *types.TorrentFile
	file      *os.File
	completed []bool
}

// ConnectToPeer establishes a connection to the specified peer.
func ConnectToPeer(peer types.Peer) (net.Conn, error) {
	address := net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))
	conn, err := net.DialTimeout("tcp", address, connTimeout)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// DownloadTorrent downloads pieces from the specified peers using default options.
func DownloadTorrent(torrent *types.TorrentFile, peers []types.Peer, peerID [20]byte) error {
	return DownloadTorrentWithOptions(torrent, peers, peerID, DefaultDownloadOptions())
}

// DownloadTorrentWithOptions downloads pieces with caller-controlled options.
func DownloadTorrentWithOptions(torrent *types.TorrentFile, peers []types.Peer, peerID [20]byte, opts DownloadOptions) error {
	if torrent == nil || len(torrent.Info.PieceHashes) == 0 {
		return fmt.Errorf("invalid torrent or no pieces to download")
	}

	logf := func(format string, args ...interface{}) {
		if opts.Verbose {
			fmt.Printf(format, args...)
		}
	}

	numPieces := len(torrent.Info.PieceHashes)
	plan, err := buildOutputPlan(torrent)
	if err != nil {
		return err
	}

	file, completed, remaining, err := prepareOutputAndState(plan.dataPath, plan.statePath, torrent)
	if err != nil {
		return err
	}
	defer file.Close()

	if remaining == 0 {
		if plan.isMultiFile {
			if err := materializeMultiFile(plan.dataPath, plan.rootDir, torrent); err != nil {
				return err
			}
		}
		os.Remove(plan.statePath) //nolint:errcheck
		logf("Already complete: %s\n", plan.displayPath)
		return nil
	}

	logf("Downloading %d/%d pieces to %s\n", remaining, numPieces, plan.displayPath)

	slog := openSessionLog(plan.statePath)
	defer slog.close()
	slog.logEvent("download_start", map[string]interface{}{
		"name":   torrent.Info.Name,
		"pieces": numPieces,
		"size":   torrent.Info.Length,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var completedCount int64
	var downloadedBytes int64
	for _, ok := range completed {
		if ok {
			completedCount++
		}
	}
	for i, ok := range completed {
		if ok {
			downloadedBytes += int64(pieceLengthAt(torrent, i))
		}
	}
	if opts.ProgressHook != nil {
		left := torrent.Info.Length - downloadedBytes
		if left < 0 {
			left = 0
		}
		opts.ProgressHook(downloadedBytes, left)
	}

	var stateMu sync.Mutex
	saveState := func() {
		stateMu.Lock()
		defer stateMu.Unlock()
		if err := saveResumeState(plan.statePath, completed); err != nil {
			logf("state save failed: %v\n", err)
		}
	}

	pieceQueue := make(chan int, numPieces*2)
	pool := newPeerPool()
	bus := newCompletionBus()
	seedPeer := func(p types.Peer) {
		pool.Add(p)
	}

	for _, p := range peers {
		seedPeer(p)
	}

	orderedPieces := buildRarestFirstOrder(torrent, peers, peerID)
	for _, idx := range orderedPieces {
		if !completed[idx] {
			pieceQueue <- idx
		}
	}

	trackers := buildTrackerList(torrent)
	go discoveryLoop(ctx, torrent, trackers, peerID, opts.ListenPort, seedPeer)

	go func() {
		ticker := time.NewTicker(stateSaveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				saveState()
			}
		}
	}()

	if opts.EnableStats {
		go func() {
			ticker := time.NewTicker(statsLogInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					snap := pool.Snapshot()
					done := atomic.LoadInt64(&completedCount)
					logf(
						"Stats: pieces=%d/%d queue=%d peers(total=%d available=%d backoff=%d quarantine=%d) success=%d failures=%d avg-latency=%s\n",
						done,
						numPieces,
						len(pieceQueue),
						snap.totalPeers,
						snap.availablePeers,
						snap.backoffPeers,
						snap.quarantinePeers,
						snap.totalSuccesses,
						snap.totalFailures,
						snap.avgLatency.Truncate(10*time.Millisecond),
					)
				}
			}
		}()
	}

	var pieceWG sync.WaitGroup
	pieceWG.Add(remaining)

	var endgameStarted atomic.Bool
	startEndgame := func() {
		if !endgameStarted.CompareAndSwap(false, true) {
			return
		}
		go func() {
			ticker := time.NewTicker(endgameInjectInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					stateMu.Lock()
					for idx := range completed {
						if completed[idx] {
							continue
						}
						select {
						case pieceQueue <- idx:
						default:
						}
					}
					stateMu.Unlock()
				}
			}
		}()
	}

	var workers sync.WaitGroup
	workerCount := numGoroutine
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var sess *peerSession
			defer func() {
				if sess != nil {
					sess.conn.Close() //nolint:errcheck
					pool.Release(sess.peer)
				}
			}()

			releaseSession := func() {
				if sess == nil {
					return
				}
				sess.conn.Close() //nolint:errcheck
				pool.Release(sess.peer)
				sess = nil
			}

			for {
				select {
				case <-ctx.Done():
					return
				case pieceIdx := <-pieceQueue:
					stateMu.Lock()
					alreadyCompleted := completed[pieceIdx]
					stateMu.Unlock()
					if alreadyCompleted {
						continue
					}

					ok := false
					cancelCh := bus.subscribe(pieceIdx)
					for attempt := 0; attempt < maxPieceAttempts; attempt++ {
						if sess == nil || sess.servedCnt >= maxPiecesPerSession {
							releaseSession()
							peer, err := pool.Acquire(ctx)
							if err != nil {
								return
							}
							newSession, err := newPeerSession(peer, torrent, peerID, pool.Add)
							if err != nil {
								pool.ReportFailureWith(peer, classifyError(err))
								pool.Release(peer)
								slog.logEvent("peer_fail", map[string]interface{}{
									"peer":  peerKey(peer),
									"class": classifyError(err),
									"err":   err.Error(),
								})
								continue
							}
							sess = newSession
							slog.logEvent("peer_connected", map[string]interface{}{"peer": peerKey(sess.peer)})
						}

						started := time.Now()
						pieceData, err := downloadPieceFromSession(sess, torrent, pieceIdx, pool.Add, cancelCh)
						if err != nil {
							if errors.Is(err, errPieceCancelledByOther) {
								// Another worker completed this piece; move on cleanly.
								break
							}
							if errors.Is(err, errPeerMissingPiece) {
								pool.ReportFailureWith(sess.peer, FailClassGeneral)
								releaseSession()
								continue
							}
							pool.ReportFailureWith(sess.peer, classifyError(err))
							slog.logEvent("peer_fail", map[string]interface{}{
								"peer":  peerKey(sess.peer),
								"class": classifyError(err),
								"err":   err.Error(),
							})
							releaseSession()
							continue
						}

						if err := writePiece(file, torrent, pieceIdx, pieceData); err != nil {
							pool.ReportFailureWith(sess.peer, FailClassIO)
							releaseSession()
							continue
						}
						pool.ReportSuccess(sess.peer, len(pieceData), time.Since(started))

						stateMu.Lock()
						if !completed[pieceIdx] {
							completed[pieceIdx] = true
							stateMu.Unlock()
							bus.complete(pieceIdx)
							pieceWG.Done()
							current := atomic.AddInt64(&completedCount, 1)
							db := atomic.AddInt64(&downloadedBytes, int64(len(pieceData)))
							if opts.ProgressHook != nil {
								left := torrent.Info.Length - db
								if left < 0 {
									left = 0
								}
								opts.ProgressHook(db, left)
							}
							slog.logEvent("piece_complete", map[string]interface{}{
								"piece":      pieceIdx,
								"peer":       peerKey(sess.peer),
								"latency_ms": time.Since(started).Milliseconds(),
							})
							remainingNow := numPieces - int(current)
							if remainingNow <= endgameThreshold {
								startEndgame()
							}
							logf("Piece %d complete (%d/%d)\n", pieceIdx, current, numPieces)
						} else {
							stateMu.Unlock()
						}

						sess.servedCnt++
						ok = true
						break
					}

					if !ok {
						select {
						case pieceQueue <- pieceIdx:
						case <-ctx.Done():
						}
					}
				}
			}
		}()
	}

	doneCh := make(chan struct{})
	go func() {
		pieceWG.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		cancel()
	case <-time.After(25 * time.Minute):
		cancel()
		workers.Wait()
		saveState()
		done := atomic.LoadInt64(&completedCount)
		slog.logEvent("download_failed", map[string]interface{}{
			"reason":          string(FailTimeout),
			"completed_pieces": done,
			"total_pieces":    numPieces,
		})
		return newDLError(FailTimeout,
			fmt.Sprintf("no progress after 25 minutes (%d/%d pieces done)", done, numPieces),
			"Check if the swarm has active seeders. Try again later or use --verbose for peer details.",
			nil)
	}

	workers.Wait()
	saveState()

	if !allPiecesComplete(completed) {
		done := atomic.LoadInt64(&completedCount)
		slog.logEvent("download_failed", map[string]interface{}{
			"reason":           string(FailIncomplete),
			"completed_pieces": done,
			"total_pieces":     numPieces,
		})
		return newDLError(FailIncomplete,
			fmt.Sprintf("%d/%d pieces completed; resume file saved: %s", done, numPieces, plan.statePath),
			"Re-run the same command to resume from where it stopped.",
			nil)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync output file: %w", err)
	}
	if plan.isMultiFile {
		if err := materializeMultiFile(plan.dataPath, plan.rootDir, torrent); err != nil {
			return err
		}
	}
	if err := os.Remove(plan.statePath); err != nil && !os.IsNotExist(err) {
		logf("warning: failed to remove state file: %v\n", err)
	}

	logf("Download complete: %s\n", plan.displayPath)
	slog.logEvent("download_complete", map[string]interface{}{
		"name": torrent.Info.Name,
		"path": plan.displayPath,
	})
	return nil
}

// OpenSeedStore opens local torrent data for inbound upload serving.
func OpenSeedStore(torrent *types.TorrentFile) (*SeedStore, error) {
	if torrent == nil || len(torrent.Info.PieceHashes) == 0 {
		return nil, fmt.Errorf("invalid torrent for seed store")
	}
	plan, err := buildOutputPlan(torrent)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(plan.dataPath)
	if err != nil {
		return nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if st.Size() < torrent.Info.Length {
		file.Close()
		return nil, fmt.Errorf("seed data file is incomplete")
	}

	completed := make([]bool, len(torrent.Info.PieceHashes))
	if rs, err := loadResumeState(plan.statePath); err == nil && len(rs.Completed) == len(completed) {
		copy(completed, rs.Completed)
	} else {
		for i := range completed {
			completed[i] = true
		}
	}

	return &SeedStore{torrent: torrent, file: file, completed: completed}, nil
}

func (s *SeedStore) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	return s.file.Close()
}

func (s *SeedStore) NumPieces() int {
	if s == nil {
		return 0
	}
	return len(s.completed)
}

func (s *SeedStore) HasPiece(index int) bool {
	if s == nil || index < 0 || index >= len(s.completed) {
		return false
	}
	return s.completed[index]
}

func (s *SeedStore) ReadPiece(index, begin, length int) ([]byte, error) {
	if s == nil || !s.HasPiece(index) {
		return nil, fmt.Errorf("piece not available")
	}
	pLen := pieceLengthAt(s.torrent, index)
	if begin < 0 || length <= 0 || begin+length > pLen {
		return nil, fmt.Errorf("invalid piece range")
	}
	b := make([]byte, length)
	off := pieceOffset(s.torrent, index) + int64(begin)
	if _, err := s.file.ReadAt(b, off); err != nil {
		return nil, err
	}
	return b, nil
}

func newPeerPool() *peerPool {
	return &peerPool{
		peers:     make(map[string]types.Peer),
		available: make(map[string]bool),
		stats:     make(map[string]*peerStat),
	}
}

func peerKey(peer types.Peer) string {
	return net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))
}

func (p *peerPool) Add(peer types.Peer) {
	if peer.IP == nil || peer.Port == 0 {
		return
	}
	// Accept both IPv4 and IPv6; reject unspecified/loopback addresses.
	if peer.IP.IsUnspecified() || peer.IP.IsLoopback() {
		return
	}
	if peer.IP.To4() == nil && peer.IP.To16() == nil {
		return
	}

	key := peerKey(peer)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.peers[key]; ok {
		return
	}
	p.peers[key] = peer
	p.available[key] = true
	p.stats[key] = &peerStat{}
}

func (p *peerPool) Acquire(ctx context.Context) (types.Peer, error) {
	for {
		now := time.Now()
		p.mu.Lock()
		bestKey := ""
		bestScore := -1e9
		for key, isAvailable := range p.available {
			if !isAvailable {
				continue
			}
			st := p.stats[key]
			if st == nil {
				continue
			}
			if now.Before(st.quarantinedUntil) || now.Before(st.backoffUntil) {
				continue
			}
			score := scorePeer(st)
			if score > bestScore {
				bestScore = score
				bestKey = key
			}
		}
		if bestKey != "" {
			p.available[bestKey] = false
			peer := p.peers[bestKey]
			p.mu.Unlock()
			return peer, nil
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return types.Peer{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (p *peerPool) Release(peer types.Peer) {
	key := peerKey(peer)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.peers[key]; ok {
		p.available[key] = true
	}
}

func (p *peerPool) ReportSuccess(peer types.Peer, bytes int, latency time.Duration) {
	key := peerKey(peer)
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.stats[key]
	if !ok {
		return
	}
	st.successes++
	st.consecutiveFailures = 0
	st.bytes += int64(bytes)
	st.latencyN++
	st.latencySum += latency
}

func (p *peerPool) ReportFailure(peer types.Peer) {
	p.ReportFailureWith(peer, FailClassGeneral)
}

// ReportFailureWith records a failure with an explicit class, tuning backoff accordingly.
func (p *peerPool) ReportFailureWith(peer types.Peer, class FailureClass) {
	key := peerKey(peer)
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.stats[key]
	if !ok {
		return
	}
	st.failures++
	st.consecutiveFailures++
	st.lastFailure = time.Now()

	// Base backoff depends on the error class.
	var (
		baseBackoff      time.Duration
		quarantineAfter  int
		quarantinePeriod time.Duration
	)
	switch class {
	case FailClassConnRefused:
		// Port is closed; back off aggressively, long quarantine.
		baseBackoff = 5 * time.Second
		quarantineAfter = 3
		quarantinePeriod = 10 * time.Minute
	case FailClassTimeout:
		// Network latency; moderate backoff.
		baseBackoff = 2 * time.Second
		quarantineAfter = 6
		quarantinePeriod = 2 * time.Minute
	case FailClassHashMismatch:
		// Data corruption; quarantine immediately.
		baseBackoff = 30 * time.Second
		quarantineAfter = 2
		quarantinePeriod = 15 * time.Minute
	case FailClassChoked:
		// Peer choked us; short wait and retry.
		baseBackoff = 1 * time.Second
		quarantineAfter = 10
		quarantinePeriod = 1 * time.Minute
	case FailClassProtocol:
		// Bad protocol; quarantine sooner.
		baseBackoff = 3 * time.Second
		quarantineAfter = 4
		quarantinePeriod = 5 * time.Minute
	case FailClassIO:
		// Local I/O error; peer is not at fault — no backoff.
		baseBackoff = 0
		quarantineAfter = 999
		quarantinePeriod = 0
	default:
		baseBackoff = 500 * time.Millisecond
		quarantineAfter = 6
		quarantinePeriod = 2 * time.Minute
	}

	// Exponential backoff.
	if baseBackoff > 0 {
		backoff := baseBackoff
		if st.consecutiveFailures > 1 {
			shift := st.consecutiveFailures - 1
			if shift > 6 {
				shift = 6
			}
			backoff = backoff * time.Duration(1<<uint(shift))
		}
		max := 2 * time.Minute
		if class == FailClassConnRefused || class == FailClassHashMismatch {
			max = 20 * time.Minute
		}
		if backoff > max {
			backoff = max
		}
		st.backoffUntil = time.Now().Add(backoff)
	}

	// Quarantine persistently bad peers.
	if st.consecutiveFailures >= quarantineAfter && quarantinePeriod > 0 {
		st.quarantinedUntil = time.Now().Add(quarantinePeriod)
		st.consecutiveFailures = 0
	}
}

func scorePeer(st *peerStat) float64 {
	if st == nil {
		return 0
	}
	score := 1.0 + float64(st.successes)*1.25 - float64(st.failures)*0.9
	if st.latencyN > 0 {
		avg := st.latencySum / time.Duration(st.latencyN)
		score -= avg.Seconds() * 0.35
	}
	if time.Since(st.lastFailure) < 8*time.Second {
		score -= 2
	}
	if time.Now().Before(st.quarantinedUntil) {
		score -= 100
	}
	if time.Now().Before(st.backoffUntil) {
		score -= 10
	}
	return score
}

func (p *peerPool) Snapshot() poolSnapshot {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	snap := poolSnapshot{}
	snap.totalPeers = len(p.peers)

	var latencySamples int64
	for key, peer := range p.peers {
		_ = peer
		if p.available[key] {
			snap.availablePeers++
		}
		st := p.stats[key]
		if st == nil {
			continue
		}
		snap.totalSuccesses += st.successes
		snap.totalFailures += st.failures
		if now.Before(st.quarantinedUntil) {
			snap.quarantinePeers++
		}
		if now.Before(st.backoffUntil) {
			snap.backoffPeers++
		}
		latencySamples += st.latencyN
		snap.avgLatency += st.latencySum
	}

	if latencySamples > 0 {
		snap.avgLatency = snap.avgLatency / time.Duration(latencySamples)
	}

	return snap
}

func newPeerSession(peer types.Peer, torrent *types.TorrentFile, peerID [20]byte, onPeerDiscovered func(types.Peer)) (*peerSession, error) {
	conn, err := ConnectToPeer(peer)
	if err != nil {
		return nil, err
	}

	_, supportsExt, err := protocol.HandshakeExtended(conn, torrent.InfoHash, peerID)
	if err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("handshake failed: %w", err)
	}

	if err := protocol.SendMessage(conn, protocol.InterestedMessage()); err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("send interested: %w", err)
	}

	sess := &peerSession{peer: peer, conn: conn}
	if supportsExt {
		type extHandshake struct {
			M map[string]int64 `bencode:"m"`
		}
		ourHS := extHandshake{M: map[string]int64{"ut_pex": 1}}
		var b strings.Builder
		if err := bencode.Marshal(&b, ourHS); err == nil {
			_ = protocol.SendMessage(conn, protocol.Message{ID: protocol.MsgExtended, Payload: append([]byte{0}, []byte(b.String())...)})
		}
	}

	for i := 0; i < 40; i++ {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			conn.Close() //nolint:errcheck
			return nil, fmt.Errorf("session setup read: %w", err)
		}
		switch msg.ID {
		case protocol.MsgBitfield:
			sess.bitfield = append([]byte(nil), msg.Payload...)
		case protocol.MsgHave:
			if len(msg.Payload) >= 4 {
				idx := parseHaveIndex(msg.Payload)
				setBitfieldPiece(&sess.bitfield, idx)
			}
		case protocol.MsgExtended:
			if len(msg.Payload) < 2 {
				continue
			}
			if msg.Payload[0] == 0 {
				type extHandshake struct {
					M map[string]int64 `bencode:"m"`
				}
				var hs extHandshake
				if err := bencode.Unmarshal(strings.NewReader(string(msg.Payload[1:])), &hs); err == nil {
					if v, ok := hs.M["ut_pex"]; ok && v > 0 && v < 256 {
						sess.utPexID = int(v)
					}
				}
			} else if sess.utPexID > 0 && int(msg.Payload[0]) == sess.utPexID {
				for _, p := range parseUtPexPeers(msg.Payload[1:]) {
					onPeerDiscovered(p)
				}
			}
		case protocol.MsgUnchoke:
			sess.unchoked = true
			return sess, nil
		case protocol.MsgChoke:
			conn.Close() //nolint:errcheck
			return nil, fmt.Errorf("peer choked us")
		}
	}

	if !sess.unchoked {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("peer did not unchoke")
	}
	return sess, nil
}

func downloadPieceFromSession(sess *peerSession, torrent *types.TorrentFile, pieceIdx int, onPeerDiscovered func(types.Peer), cancelCh <-chan struct{}) ([]byte, error) {
	if len(sess.bitfield) > 0 && !bitfieldHasPiece(sess.bitfield, pieceIdx) {
		return nil, errPeerMissingPiece
	}

	pieceSize := pieceLengthAt(torrent, pieceIdx)
	pieceData := make([]byte, pieceSize)

	inflight := make(map[uint32]uint32)
	nextBegin := uint32(0)
	written := 0

	// sendCancelInflight cancels all blocks we have requested but not yet received.
	sendCancelInflight := func() {
		for off, blen := range inflight {
			_ = protocol.SendMessage(sess.conn, protocol.CancelMessage(uint32(pieceIdx), off, blen))
		}
	}

	for written < pieceSize {
		// Non-blocking: check if another worker already completed this piece.
		select {
		case <-cancelCh:
			sendCancelInflight()
			return nil, errPieceCancelledByOther
		default:
		}

		for len(inflight) < 5 && int(nextBegin) < pieceSize {
			blockLen := blockSize
			if int(nextBegin)+blockLen > pieceSize {
				blockLen = pieceSize - int(nextBegin)
			}
			if err := protocol.SendMessage(sess.conn, protocol.RequestMessage(uint32(pieceIdx), nextBegin, uint32(blockLen))); err != nil {
				return nil, fmt.Errorf("send request: %w", err)
			}
			inflight[nextBegin] = uint32(blockLen)
			nextBegin += uint32(blockLen)
		}

		msg, err := protocol.ReadMessage(sess.conn)
		if err != nil {
			return nil, fmt.Errorf("read piece: %w", err)
		}
		// Reset read deadline after each successful message.
		_ = sess.conn.SetDeadline(time.Now().Add(30 * time.Second))

		switch msg.ID {
		case protocol.MsgPiece:
			idx, off, block, err := protocol.ParsePieceMessage(msg.Payload)
			if err != nil {
				return nil, err
			}
			if idx != uint32(pieceIdx) {
				continue
			}
			expectLen, ok := inflight[off]
			if !ok {
				continue
			}
			if uint32(len(block)) != expectLen {
				return nil, fmt.Errorf("short block for piece %d off %d", pieceIdx, off)
			}
			copy(pieceData[off:off+uint32(len(block))], block)
			delete(inflight, off)
			written += len(block)
		case protocol.MsgHave:
			if len(msg.Payload) >= 4 {
				idx := parseHaveIndex(msg.Payload)
				setBitfieldPiece(&sess.bitfield, idx)
			}
		case protocol.MsgBitfield:
			sess.bitfield = append([]byte(nil), msg.Payload...)
			if !bitfieldHasPiece(sess.bitfield, pieceIdx) {
				return nil, errPeerMissingPiece
			}
		case protocol.MsgExtended:
			if len(msg.Payload) < 2 {
				continue
			}
			if sess.utPexID > 0 && int(msg.Payload[0]) == sess.utPexID {
				for _, p := range parseUtPexPeers(msg.Payload[1:]) {
					onPeerDiscovered(p)
				}
			}
		case protocol.MsgChoke:
			sendCancelInflight()
			return nil, fmt.Errorf("peer choked during piece transfer")
		}
	}

	hash := sha1.Sum(pieceData)
	if hash != torrent.Info.PieceHashes[pieceIdx] {
		return nil, fmt.Errorf("piece %d hash mismatch", pieceIdx)
	}

	return pieceData, nil
}

// downloadPieceFromPeer downloads a specific piece from a peer.
func downloadPieceFromPeer(peer types.Peer, torrent *types.TorrentFile, pieceIdx int, peerID [20]byte) ([]byte, error) {
	conn, err := ConnectToPeer(peer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Perform handshake
	_, err = protocol.Handshake(conn, torrent.InfoHash, peerID)
	if err != nil {
		return nil, fmt.Errorf("handshake failed: %v", err)
	}

	// Send interested message
	if err := protocol.SendMessage(conn, protocol.InterestedMessage()); err != nil {
		return nil, fmt.Errorf("failed to send interested: %v", err)
	}

	haveSignal := false

	// Wait for unchoke
	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			return nil, fmt.Errorf("error reading message: %v", err)
		}

		if msg.ID == protocol.MsgBitfield {
			if bitfieldHasPiece(msg.Payload, pieceIdx) {
				haveSignal = true
			}
			continue
		}
		if msg.ID == protocol.MsgHave && len(msg.Payload) >= 4 {
			haveIdx := int(uint32(msg.Payload[0])<<24 | uint32(msg.Payload[1])<<16 | uint32(msg.Payload[2])<<8 | uint32(msg.Payload[3]))
			if haveIdx == pieceIdx {
				haveSignal = true
			}
			continue
		}

		if msg.ID == protocol.MsgUnchoke {
			break
		} else if msg.ID == protocol.MsgChoke {
			return nil, fmt.Errorf("peer choked us")
		}
	}

	if !haveSignal {
		return nil, fmt.Errorf("peer did not advertise piece %d", pieceIdx)
	}

	// Calculate piece size
	pieceSize := pieceLengthAt(torrent, pieceIdx)

	// Download the piece in blocks
	pieceData := make([]byte, pieceSize)
	for begin := 0; begin < pieceSize; begin += blockSize {
		blockLen := blockSize
		if begin+blockLen > pieceSize {
			blockLen = pieceSize - begin
		}

		// Request block
		reqMsg := protocol.RequestMessage(uint32(pieceIdx), uint32(begin), uint32(blockLen))
		if err := protocol.SendMessage(conn, reqMsg); err != nil {
			return nil, fmt.Errorf("failed to send request: %v", err)
		}

		// Read piece message
		for {
			msg, err := protocol.ReadMessage(conn)
			if err != nil {
				return nil, fmt.Errorf("error reading message: %v", err)
			}

			if msg.ID == protocol.MsgPiece {
				idx, off, block, err := protocol.ParsePieceMessage(msg.Payload)
				if err != nil {
					return nil, err
				}

				if idx == uint32(pieceIdx) && off == uint32(begin) {
					copy(pieceData[off:off+uint32(len(block))], block)
					break
				}
			}
		}
	}

	// Verify hash
	hash := sha1.Sum(pieceData)
	if hash != torrent.Info.PieceHashes[pieceIdx] {
		return nil, fmt.Errorf("piece %d hash mismatch", pieceIdx)
	}

	return pieceData, nil
}

func buildOutputPlan(torrent *types.TorrentFile) (*outputPlan, error) {
	baseDir := os.Getenv("HOME")
	if baseDir == "" {
		baseDir = "."
	} else {
		baseDir = filepath.Join(baseDir, "Downloads")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	fallbackName := fmt.Sprintf("downloaded_%x", torrent.InfoHash[:8])
	name := strings.TrimSpace(torrent.Info.Name)
	if name == "" {
		if len(torrent.Info.Files) == 0 {
			name = fallbackName + ".bin"
		} else {
			name = fallbackName
		}
	}

	if len(torrent.Info.Files) == 0 {
		dataPath := filepath.Join(baseDir, name)
		return &outputPlan{
			dataPath:    dataPath,
			statePath:   dataPath + ".state.json",
			displayPath: dataPath,
			rootDir:     baseDir,
			isMultiFile: false,
		}, nil
	}

	rootDir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create torrent output directory: %w", err)
	}

	return &outputPlan{
		dataPath:    filepath.Join(rootDir, ".torrent-client.payload"),
		statePath:   filepath.Join(rootDir, ".torrent-client.state.json"),
		displayPath: rootDir,
		rootDir:     rootDir,
		isMultiFile: true,
	}, nil
}

func materializeMultiFile(dataPath, rootDir string, torrent *types.TorrentFile) error {
	payload, err := os.Open(dataPath)
	if err != nil {
		return fmt.Errorf("open payload file: %w", err)
	}
	defer payload.Close()

	var offset int64
	for idx, f := range torrent.Info.Files {
		if f.Length < 0 {
			return fmt.Errorf("invalid negative file length at index %d", idx)
		}
		parts := sanitizePathParts(f.Path)
		if len(parts) == 0 {
			parts = []string{fmt.Sprintf("file_%d.bin", idx)}
		}
		dst := filepath.Join(append([]string{rootDir}, parts...)...)

		dir := filepath.Dir(dst)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", dst, err)
		}

		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("create output file %s: %w", dst, err)
		}

		section := io.NewSectionReader(payload, offset, f.Length)
		if _, err := io.CopyN(out, section, f.Length); err != nil {
			out.Close()
			return fmt.Errorf("write output file %s: %w", dst, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close output file %s: %w", dst, err)
		}

		offset += f.Length
	}

	return nil
}

func sanitizePathParts(parts []string) []string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		p = strings.ReplaceAll(p, "\\", "_")
		p = strings.ReplaceAll(p, "/", "_")
		clean = append(clean, p)
	}
	return clean
}

func prepareOutputAndState(outputPath, statePath string, torrent *types.TorrentFile) (*os.File, []bool, int, error) {
	numPieces := len(torrent.Info.PieceHashes)
	completed := make([]bool, numPieces)

	if st, err := loadResumeState(statePath); err == nil && len(st.Completed) == numPieces {
		copy(completed, st.Completed)
	}

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("open output file: %w", err)
	}
	if err := file.Truncate(torrent.Info.Length); err != nil {
		file.Close()
		return nil, nil, 0, fmt.Errorf("size output file: %w", err)
	}

	remaining := numPieces
	for i := 0; i < numPieces; i++ {
		if !completed[i] {
			continue
		}
		ok, err := verifyPieceFromDisk(file, torrent, i)
		if err != nil || !ok {
			completed[i] = false
			continue
		}
		remaining--
	}

	if err := saveResumeState(statePath, completed); err != nil {
		file.Close()
		return nil, nil, 0, err
	}

	return file, completed, remaining, nil
}

func pieceLengthAt(torrent *types.TorrentFile, pieceIdx int) int {
	pieceSize := int(torrent.Info.PieceLength)
	if pieceIdx == len(torrent.Info.PieceHashes)-1 {
		lastPieceSize := int(torrent.Info.Length % torrent.Info.PieceLength)
		if lastPieceSize > 0 {
			pieceSize = lastPieceSize
		}
	}
	return pieceSize
}

func pieceOffset(torrent *types.TorrentFile, pieceIdx int) int64 {
	return int64(pieceIdx) * torrent.Info.PieceLength
}

func writePiece(file *os.File, torrent *types.TorrentFile, pieceIdx int, pieceData []byte) error {
	if len(pieceData) != pieceLengthAt(torrent, pieceIdx) {
		return fmt.Errorf("piece %d invalid size: got %d", pieceIdx, len(pieceData))
	}
	_, err := file.WriteAt(pieceData, pieceOffset(torrent, pieceIdx))
	if err != nil {
		return fmt.Errorf("write piece %d: %w", pieceIdx, err)
	}
	return nil
}

func verifyPieceFromDisk(file *os.File, torrent *types.TorrentFile, pieceIdx int) (bool, error) {
	pieceData := make([]byte, pieceLengthAt(torrent, pieceIdx))
	_, err := file.ReadAt(pieceData, pieceOffset(torrent, pieceIdx))
	if err != nil {
		return false, err
	}
	return sha1.Sum(pieceData) == torrent.Info.PieceHashes[pieceIdx], nil
}

func saveResumeState(path string, completed []bool) error {
	st := resumeState{Completed: completed}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal resume state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func loadResumeState(path string) (*resumeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st resumeState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func allPiecesComplete(completed []bool) bool {
	for _, ok := range completed {
		if !ok {
			return false
		}
	}
	return true
}

func buildTrackerList(tf *types.TorrentFile) []string {
	seen := make(map[string]struct{})
	var list []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		list = append(list, u)
	}

	add(tf.Announce)
	for _, tier := range tf.AnnounceList {
		for _, tr := range tier {
			add(tr)
		}
	}

	if !tf.Info.Private {
		for _, tr := range publicTrackerFallback {
			add(tr)
		}
	}
	return list
}

func bitfieldHasPiece(bitfield []byte, pieceIdx int) bool {
	byteIdx := pieceIdx / 8
	if byteIdx < 0 || byteIdx >= len(bitfield) {
		return false
	}
	bit := uint(7 - (pieceIdx % 8))
	return ((bitfield[byteIdx] >> bit) & 1) == 1
}

func probePeerBitfield(peer types.Peer, torrent *types.TorrentFile, peerID [20]byte) ([]byte, error) {
	conn, err := ConnectToPeer(peer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := protocol.Handshake(conn, torrent.InfoHash, peerID); err != nil {
		return nil, err
	}

	for i := 0; i < 8; i++ {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			return nil, err
		}
		if msg.ID == protocol.MsgBitfield {
			return msg.Payload, nil
		}
	}
	return nil, fmt.Errorf("no bitfield")
}

func buildRarestFirstOrder(torrent *types.TorrentFile, peers []types.Peer, peerID [20]byte) []int {
	n := len(torrent.Info.PieceHashes)
	if n == 0 {
		return nil
	}

	availability := make([]int, n)
	probeLimit := len(peers)
	if probeLimit > 24 {
		probeLimit = 24
	}

	for i := 0; i < probeLimit; i++ {
		bf, err := probePeerBitfield(peers[i], torrent, peerID)
		if err != nil || len(bf) == 0 {
			continue
		}
		for pieceIdx := 0; pieceIdx < n; pieceIdx++ {
			if bitfieldHasPiece(bf, pieceIdx) {
				availability[pieceIdx]++
			}
		}
	}

	order := make([]int, n)
	for i := 0; i < n; i++ {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		ai := availability[order[i]]
		aj := availability[order[j]]
		if ai == aj {
			return order[i] < order[j]
		}
		if ai == 0 {
			return false
		}
		if aj == 0 {
			return true
		}
		return ai < aj
	})

	return order
}

func parseHaveIndex(payload []byte) int {
	if len(payload) < 4 {
		return -1
	}
	return int(uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3]))
}

func setBitfieldPiece(bitfield *[]byte, pieceIdx int) {
	if pieceIdx < 0 {
		return
	}
	byteIdx := pieceIdx / 8
	if byteIdx < 0 {
		return
	}
	if len(*bitfield) <= byteIdx {
		grown := make([]byte, byteIdx+1)
		copy(grown, *bitfield)
		*bitfield = grown
	}
	bit := uint(7 - (pieceIdx % 8))
	(*bitfield)[byteIdx] |= (1 << bit)
}

func parseUtPexPeers(payload []byte) []types.Peer {
	type utPexMessage struct {
		Added string `bencode:"added"`
	}
	var msg utPexMessage
	if err := bencode.Unmarshal(strings.NewReader(string(payload)), &msg); err != nil {
		return nil
	}
	data := []byte(msg.Added)
	if len(data) < 6 {
		return nil
	}
	peers := make([]types.Peer, 0, len(data)/6)
	for i := 0; i+6 <= len(data); i += 6 {
		ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3])
		port := uint16(data[i+4])<<8 | uint16(data[i+5])
		if port == 0 {
			continue
		}
		peers = append(peers, types.Peer{IP: ip, Port: port})
	}
	return peers
}

func discoveryLoop(ctx context.Context, torrent *types.TorrentFile, trackers []string, peerID [20]byte, listenPort uint16, addPeer func(types.Peer)) {
	announceWithRetry := func(tr string, req tracker.AnnounceRequest) []types.Peer {
		const retries = 2
		var lastErr error
		for attempt := 0; attempt < retries; attempt++ {
			switch {
			case strings.HasPrefix(tr, "udp://"):
				pp, err := tracker.AnnounceUDPWithRequest(tr, req)
				if err == nil {
					return pp
				}
				lastErr = err
			case strings.HasPrefix(tr, "http://"), strings.HasPrefix(tr, "https://"):
				tfCopy := *torrent
				tfCopy.Announce = tr
				pp, err := tracker.AnnounceToHTTPTrackerWithRequest(&tfCopy, req)
				if err == nil {
					return pp
				}
				lastErr = err
			}
			time.Sleep(time.Duration(attempt+1) * 350 * time.Millisecond)
		}
		if lastErr != nil {
			return nil
		}
		return nil
	}

	fetch := func() {
		for _, tr := range trackers {
			select {
			case <-ctx.Done():
				return
			default:
			}

			pp := announceWithRetry(tr, tracker.AnnounceRequest{
				InfoHash: torrent.InfoHash,
				PeerID:   peerID,
				Port:     listenPort,
				Left:     torrent.Info.Length,
				Event:    "",
			})
			for _, p := range pp {
				addPeer(p)
			}
		}

		if !torrent.Info.Private {
			for i := 0; i < 2; i++ {
				pp, err := dht.GetPeers(torrent.InfoHash, 120, 20*time.Second)
				if err == nil {
					for _, p := range pp {
						addPeer(p)
					}
				}
			}
		}
	}

	fetch()
	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetch()
		}
	}
}

// RequestPiece requests a specific piece from the peer (legacy, kept for compatibility).
func RequestPiece(peer types.Peer, index int) ([]byte, error) {
	address := net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))

	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer %s: %v", address, err)
	}
	defer conn.Close()

	// Read the response (this is a placeholder)
	response := make([]byte, 16384) // Assuming piece size is 16KB
	n, err := conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read piece %d: %v", index, err)
	}

	return response[:n], nil // Return the downloaded piece
}
