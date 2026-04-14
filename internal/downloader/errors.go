package downloader

import (
	"errors"
	"fmt"
	"net"
)

// FailureReason is a machine-readable code identifying why a download failed.
type FailureReason string

const (
	// FailNoPeers means no reachable peers were ever found in the swarm.
	FailNoPeers FailureReason = "no_peers"
	// FailTimeout means the global download deadline elapsed with pieces still pending.
	FailTimeout FailureReason = "timeout"
	// FailHashMismatch means too many pieces failed SHA-1 verification.
	FailHashMismatch FailureReason = "hash_mismatch"
	// FailIncomplete means the download finished the time window but pieces are still missing.
	FailIncomplete FailureReason = "incomplete"
	// FailIO means the local disk could not be written or read.
	FailIO FailureReason = "io_error"
	// FailInvalidTorrent means the torrent metadata is malformed or missing.
	FailInvalidTorrent FailureReason = "invalid_torrent"
	// FailMetadata means BEP-9 metadata could not be fetched from the swarm.
	FailMetadata FailureReason = "metadata_fetch_failed"
)

// DownloadError wraps a download failure with a machine-readable reason code
// and a human-readable explanation of what to try next.
type DownloadError struct {
	Reason  FailureReason
	Details string
	Hint    string // actionable suggestion for the user
	Cause   error
}

func (e *DownloadError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Reason, e.Details, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Reason, e.Details)
}

func (e *DownloadError) Unwrap() error { return e.Cause }

func newDLError(reason FailureReason, details, hint string, cause error) *DownloadError {
	return &DownloadError{Reason: reason, Details: details, Hint: hint, Cause: cause}
}

// FailureClass classifies a peer-level error for backoff tuning.
type FailureClass int

const (
	// FailClassGeneral is an unclassified failure.
	FailClassGeneral FailureClass = iota
	// FailClassConnRefused means the TCP dial was actively refused (port closed).
	FailClassConnRefused
	// FailClassTimeout means a network operation timed out.
	FailClassTimeout
	// FailClassHashMismatch means a piece failed SHA-1 integrity check.
	FailClassHashMismatch
	// FailClassChoked means the peer choked us mid-transfer.
	FailClassChoked
	// FailClassProtocol means an unexpected or malformed protocol message.
	FailClassProtocol
	// FailClassIO means a local disk I/O error.
	FailClassIO
)

// classifyError maps a runtime error to a FailureClass for backoff decisions.
func classifyError(err error) FailureClass {
	if err == nil {
		return FailClassGeneral
	}
	msg := err.Error()
	// Check network timeout via net.Error interface.
	if isNetTimeout(err) {
		return FailClassTimeout
	}
	// Connection-level rejections.
	for _, substr := range []string{"connection refused", "no route to host", "network unreachable", "host unreachable"} {
		if contains(msg, substr) {
			return FailClassConnRefused
		}
	}
	// Data integrity failure.
	if contains(msg, "hash mismatch") {
		return FailClassHashMismatch
	}
	// Peer-initiated choke.
	if contains(msg, "choked") || contains(msg, "choke") {
		return FailClassChoked
	}
	// Protocol-level errors.
	for _, substr := range []string{"handshake", "protocol", "unexpected", "invalid"} {
		if contains(msg, substr) {
			return FailClassProtocol
		}
	}
	return FailClassGeneral
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// isNetTimeout reports whether err is a network timeout.
func isNetTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
