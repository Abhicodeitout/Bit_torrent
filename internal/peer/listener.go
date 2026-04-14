package peer

import (
	"context"
	"fmt"
	"net"
	"time"

	"torrent-client/internal/protocol"
	"torrent-client/internal/types"
)

// PieceProvider provides local piece availability for inbound upload serving.
type PieceProvider interface {
	NumPieces() int
	HasPiece(index int) bool
	ReadPiece(index, begin, length int) ([]byte, error)
}

// ListenerOptions controls inbound listener behavior.
type ListenerOptions struct {
	Verbose       bool
	Provider      PieceProvider
	OnUploaded    func(peer types.Peer, bytes int)
	OnPeerSeen    func(peer types.Peer)
	ShouldUnchoke func(peer types.Peer) bool
}

func buildBitfield(provider PieceProvider) []byte {
	if provider == nil || provider.NumPieces() == 0 {
		return nil
	}
	n := provider.NumPieces()
	bf := make([]byte, (n+7)/8)
	for i := 0; i < n; i++ {
		if !provider.HasPiece(i) {
			continue
		}
		byteIdx := i / 8
		bit := uint(7 - (i % 8))
		bf[byteIdx] |= (1 << bit)
	}
	return bf
}

// StartInboundListener starts a minimal BitTorrent listener for inbound peers.
func StartInboundListener(ctx context.Context, infoHash [20]byte, peerID [20]byte, port uint16, opts ListenerOptions) error {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	logf := func(format string, args ...interface{}) {
		if opts.Verbose {
			fmt.Printf(format, args...)
		}
	}
	logf("Inbound listener active on %s\n", addr)
	bitfield := buildBitfield(opts.Provider)

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

			remotePeer, ok := peerFromRemoteAddr(c.RemoteAddr())
			if ok && opts.OnPeerSeen != nil {
				opts.OnPeerSeen(remotePeer)
			}

			if _, err := protocol.ReadHandshake(c, infoHash); err != nil {
				return
			}
			if err := protocol.WriteHandshake(c, infoHash, peerID); err != nil {
				return
			}

			_ = protocol.SendMessage(c, protocol.BitfieldMessage(bitfield))
			_ = protocol.SendMessage(c, protocol.Message{ID: protocol.MsgChoke})
			peerChoked := true

			_ = c.SetDeadline(time.Now().Add(90 * time.Second))
			for {
				if ok && opts.ShouldUnchoke != nil {
					wantUnchoked := opts.ShouldUnchoke(remotePeer)
					if wantUnchoked && peerChoked {
						if err := protocol.SendMessage(c, protocol.Message{ID: protocol.MsgUnchoke}); err != nil {
							return
						}
						peerChoked = false
					} else if !wantUnchoked && !peerChoked {
						if err := protocol.SendMessage(c, protocol.Message{ID: protocol.MsgChoke}); err != nil {
							return
						}
						peerChoked = true
					}
				}

				msg, err := protocol.ReadMessage(c)
				if err != nil {
					return
				}
				switch msg.ID {
				case protocol.MsgInterested:
					if ok && opts.ShouldUnchoke != nil {
						if opts.ShouldUnchoke(remotePeer) {
							if peerChoked {
								if err := protocol.SendMessage(c, protocol.Message{ID: protocol.MsgUnchoke}); err != nil {
									return
								}
								peerChoked = false
							}
						} else if !peerChoked {
							if err := protocol.SendMessage(c, protocol.Message{ID: protocol.MsgChoke}); err != nil {
								return
							}
							peerChoked = true
						}
					} else {
						if err := protocol.SendMessage(c, protocol.Message{ID: protocol.MsgUnchoke}); err != nil {
							return
						}
						peerChoked = false
					}
				case protocol.MsgRequest:
					if opts.Provider == nil {
						continue
					}
					if peerChoked {
						continue
					}
					idx, begin, length, err := protocol.ParseRequestMessage(msg.Payload)
					if err != nil {
						continue
					}
					block, err := opts.Provider.ReadPiece(int(idx), int(begin), int(length))
					if err != nil {
						continue
					}
					if err := protocol.SendMessage(c, protocol.PieceMessage(idx, begin, block)); err != nil {
						return
					}
					if opts.OnUploaded != nil {
						if ok {
							opts.OnUploaded(remotePeer, len(block))
						}
					}
				}
			}
		}(conn)
	}
}

func peerFromRemoteAddr(addr net.Addr) (types.Peer, bool) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr == nil || tcpAddr.IP == nil || tcpAddr.Port <= 0 || tcpAddr.Port > 65535 {
		return types.Peer{}, false
	}
	return types.Peer{IP: tcpAddr.IP, Port: uint16(tcpAddr.Port)}, true
}
