package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// DownloadState tracks the progress of a torrent download.
type DownloadState struct {
	InfoHash       [20]byte  `json:"info_hash"`
	Name           string    `json:"name"`
	TotalSize      int64     `json:"total_size"`
	PieceLength    int64     `json:"piece_length"`
	NumPieces      int       `json:"num_pieces"`
	DownloadedPieces []bool  `json:"downloaded_pieces"`
	
	mu sync.RWMutex
}

// NewDownloadState creates a new download state.
func NewDownloadState(infoHash [20]byte, name string, totalSize, pieceLength int64) *DownloadState {
	numPieces := int((totalSize + pieceLength - 1) / pieceLength)
	return &DownloadState{
		InfoHash:         infoHash,
		Name:             name,
		TotalSize:        totalSize,
		PieceLength:      pieceLength,
		NumPieces:        numPieces,
		DownloadedPieces: make([]bool, numPieces),
	}
}

// LoadState loads download state from a file.
func LoadState(stateFile string) (*DownloadState, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var state DownloadState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to decode state: %w", err)
	}
	return &state, nil
}

// Save persists the download state to a file.
func (ds *DownloadState) Save(stateFile string) error {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFile, data, 0644)
}

// MarkPieceDownloaded marks a piece as downloaded.
func (ds *DownloadState) MarkPieceDownloaded(pieceIdx int) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if pieceIdx >= 0 && pieceIdx < len(ds.DownloadedPieces) {
		ds.DownloadedPieces[pieceIdx] = true
	}
}

// IsPieceDownloaded checks if a piece is already downloaded.
func (ds *DownloadState) IsPieceDownloaded(pieceIdx int) bool {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	if pieceIdx >= 0 && pieceIdx < len(ds.DownloadedPieces) {
		return ds.DownloadedPieces[pieceIdx]
	}
	return false
}

// GetPendingPieces returns indices of pieces that haven't been downloaded yet.
func (ds *DownloadState) GetPendingPieces() []int {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	var pending []int
	for i, downloaded := range ds.DownloadedPieces {
		if !downloaded {
			pending = append(pending, i)
		}
	}
	return pending
}

// GetDownloadProgress returns the number of downloaded pieces and total pieces.
func (ds *DownloadState) GetDownloadProgress() (int, int) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	downloaded := 0
	for _, d := range ds.DownloadedPieces {
		if d {
			downloaded++
		}
	}
	return downloaded, len(ds.DownloadedPieces)
}

// IsComplete checks if all pieces have been downloaded.
func (ds *DownloadState) IsComplete() bool {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	for _, downloaded := range ds.DownloadedPieces {
		if !downloaded {
			return false
		}
	}
	return true
}
