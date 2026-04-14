package downloader

import (
	"crypto/sha1"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"torrent-client/internal/protocol"
	"torrent-client/internal/types"
)

const (
	blockSize    = 16 * 1024 // 16 KB
	numGoroutine = 4
	connTimeout  = 10 * time.Second
)

// ConnectToPeer establishes a connection to the specified peer.
func ConnectToPeer(peer types.Peer) (net.Conn, error) {
	address := net.JoinHostPort(peer.IP.String(), fmt.Sprintf("%d", peer.Port))
	fmt.Printf("Attempting to connect to peer: %s\n", address)

	conn, err := net.DialTimeout("tcp", address, connTimeout)
	if err != nil {
		fmt.Printf("Failed to connect to peer %s: %v\n", address, err)
		return nil, err
	}

	fmt.Printf("Connected to peer: %s\n", address)
	return conn, nil
}

// DownloadTorrent downloads pieces from the specified peers.
func DownloadTorrent(torrent *types.TorrentFile, peers []types.Peer, peerID [20]byte) error {
	if torrent == nil || len(torrent.Info.PieceHashes) == 0 {
		return fmt.Errorf("invalid torrent or no pieces to download")
	}
	if len(peers) == 0 {
		return fmt.Errorf("no peers available")
	}

	numPieces := len(torrent.Info.PieceHashes)
	pieces := make([][]byte, numPieces)
	var mu sync.Mutex

	fmt.Printf("Downloading %d pieces from %d peers\n", numPieces, len(peers))

	// Buffer at 2x numPieces to allow re-queuing without blocking.
	taskChan := make(chan int, numPieces*2)
	for i := 0; i < numPieces; i++ {
		taskChan <- i
	}

	numWorkers := numGoroutine
	if numWorkers > len(peers) {
		numWorkers = len(peers)
	}

	var (
		wg         sync.WaitGroup
		downloaded int
		stopOnce   sync.Once
	)
	stopCh := make(chan struct{})

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			peer := peers[workerIdx%len(peers)]

			for {
				select {
				case <-stopCh:
					return
				case pieceIdx := <-taskChan:
					if err := downloadPieceFromPeer(&peer, torrent, pieceIdx, pieces, &mu, peerID); err != nil {
						fmt.Printf("Failed to download piece %d from peer %s: %v\n", pieceIdx, peer.IP, err)
						// Re-queue; if stop has been signalled drop the piece.
						select {
						case taskChan <- pieceIdx:
						case <-stopCh:
							return
						}
					} else {
						fmt.Printf("Downloaded piece %d from %s\n", pieceIdx, peer.IP)
						mu.Lock()
						downloaded++
						done := downloaded >= numPieces
						mu.Unlock()
						if done {
							stopOnce.Do(func() { close(stopCh) })
							return
						}
					}
				}
			}
		}(i)
	}

	wg.Wait()

	mu.Lock()
	allDownloaded := downloaded >= numPieces
	mu.Unlock()

	if !allDownloaded {
		return fmt.Errorf("not all pieces were downloaded")
	}

	fmt.Println("All pieces downloaded, assembling file...")
	return AssembleFile(pieces, torrent)
}

// downloadPieceFromPeer downloads a specific piece from a peer.
func downloadPieceFromPeer(peer *types.Peer, torrent *types.TorrentFile, pieceIdx int, pieces [][]byte, mu *sync.Mutex, peerID [20]byte) error {
	conn, err := ConnectToPeer(*peer)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Perform handshake
	_, err = protocol.Handshake(conn, torrent.InfoHash, peerID)
	if err != nil {
		return fmt.Errorf("handshake failed: %v", err)
	}

	// Send interested message
	if err := protocol.SendMessage(conn, protocol.InterestedMessage()); err != nil {
		return fmt.Errorf("failed to send interested: %v", err)
	}

	// Wait for unchoke
	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			return fmt.Errorf("error reading message: %v", err)
		}

		if msg.ID == protocol.MsgUnchoke {
			break
		} else if msg.ID == protocol.MsgChoke {
			return fmt.Errorf("peer choked us")
		}
	}

	// Calculate piece size
	pieceSize := int(torrent.Info.PieceLength)
	if pieceIdx == len(torrent.Info.PieceHashes)-1 {
		// Last piece might be smaller
		lastPieceSize := int(torrent.Info.Length % torrent.Info.PieceLength)
		if lastPieceSize > 0 {
			pieceSize = lastPieceSize
		}
	}

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
			return fmt.Errorf("failed to send request: %v", err)
		}

		// Read piece message
		for {
			msg, err := protocol.ReadMessage(conn)
			if err != nil {
				return fmt.Errorf("error reading message: %v", err)
			}

			if msg.ID == protocol.MsgPiece {
				idx, off, block, err := protocol.ParsePieceMessage(msg.Payload)
				if err != nil {
					return err
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
		return fmt.Errorf("piece %d hash mismatch", pieceIdx)
	}

	// Store the piece
	mu.Lock()
	pieces[pieceIdx] = pieceData
	mu.Unlock()

	return nil
}

// AssembleFile assembles downloaded pieces into the final file.
func AssembleFile(pieces [][]byte, torrent *types.TorrentFile) error {
	fmt.Println("Creating the downloadable file")

	// Save downloads to the user's Downloads folder when available.
	outputDir := os.Getenv("HOME")
	if outputDir == "" {
		outputDir = "."
	} else {
		outputDir = fmt.Sprintf("%s/Downloads", outputDir)
	}

	// Ensure the output directory exists.
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	var outputFileName string
	if len(torrent.Info.Files) > 0 {
		// Multi-file torrent - use first file name
		outputFileName = torrent.Info.Files[0].Path[len(torrent.Info.Files[0].Path)-1]
	} else {
		// Single file torrent - use a generic name
		outputFileName = fmt.Sprintf("downloaded_%x", torrent.InfoHash[:8])
	}

	outputFilePath := fmt.Sprintf("%s/%s", outputDir, outputFileName)

	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outputFile.Close()

	for i, piece := range pieces {
		if piece == nil {
			return fmt.Errorf("piece %d is nil", i)
		}

		_, err := outputFile.Write(piece)
		if err != nil {
			return fmt.Errorf("failed to write to output file: %v", err)
		}

		fmt.Printf("Wrote piece %d (%d bytes)\n", i, len(piece))
	}

	fmt.Printf("File assembled successfully at: %s\n", outputFilePath)
	return nil
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
