package peer

import (
	"context"
	"fmt"
	"net"
	"time"

	"torrent-client/internal/protocol"
)

// StartInboundListener starts a minimal BitTorrent listener for inbound peers.
// It currently accepts and validates handshakes, replies with our handshake,
// advertises no pieces, and keeps connections briefly alive.
func StartInboundListener(ctx context.Context, infoHash [20]byte, peerID [20]byte, port uint16, verbose bool) error {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	logf := func(format string, args ...interface{}) {
		if verbose {
			fmt.Printf(format, args...)
		}
	}
	logf("Inbound listener active on %s\n", addr)

	go func() {
		<-ctx.Done()
		ln.Close() //nolint:errcheck
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}

		go func(c net.Conn) {
			defer c.Close()

			if _, err := protocol.ReadHandshake(c, infoHash); err != nil {
				return
			}
			if err := protocol.WriteHandshake(c, infoHash, peerID); err != nil {
				return
			}

			// We do not upload yet; explicitly announce an empty bitfield and choke.
			_ = protocol.SendMessage(c, protocol.BitfieldMessage(nil))
			_ = protocol.SendMessage(c, protocol.Message{ID: protocol.MsgChoke})

			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			for {
				if _, err := protocol.ReadMessage(c); err != nil {
					return
				}
			}
		}(conn)
	}
}
