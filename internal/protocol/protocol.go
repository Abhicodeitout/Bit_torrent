package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	connTimeout   = 10 * time.Second
	readTimeout   = 10 * time.Second
	handshakePStr = "BitTorrent protocol"
	handshakeLen  = 68
)

// Message IDs for the BitTorrent wire protocol
const (
	MsgChoke         = 0
	MsgUnchoke       = 1
	MsgInterested    = 2
	MsgNotInterested = 3
	MsgHave          = 4
	MsgBitfield      = 5
	MsgRequest       = 6
	MsgPiece         = 7
	MsgCancel        = 8
)

// Handshake performs the BitTorrent handshake with a peer.
func Handshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) ([20]byte, error) {
	if err := WriteHandshake(conn, infoHash, peerID); err != nil {
		return [20]byte{}, err
	}
	return ReadHandshake(conn, infoHash)
}

// BuildHandshake builds the canonical 68-byte BitTorrent handshake payload.
func BuildHandshake(infoHash [20]byte, peerID [20]byte) []byte {
	// Protocol: <pstrlen><pstr><reserved><info_hash><peer_id>
	handshakeMsg := new(bytes.Buffer)
	handshakeMsg.WriteByte(byte(len(handshakePStr)))
	handshakeMsg.WriteString(handshakePStr)
	handshakeMsg.Write(make([]byte, 8))
	handshakeMsg.Write(infoHash[:])
	handshakeMsg.Write(peerID[:])
	return handshakeMsg.Bytes()
}

// WriteHandshake writes a BitTorrent handshake to conn.
func WriteHandshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) error {
	conn.SetWriteDeadline(time.Now().Add(connTimeout))
	if _, err := conn.Write(BuildHandshake(infoHash, peerID)); err != nil {
		return fmt.Errorf("failed to send handshake: %v", err)
	}
	return nil
}

// ReadHandshake reads and validates a BitTorrent handshake from conn.
// If expectedInfoHash is non-zero, the incoming info-hash must match.
func ReadHandshake(conn net.Conn, expectedInfoHash [20]byte) ([20]byte, error) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	response := make([]byte, handshakeLen)
	if _, err := io.ReadFull(conn, response); err != nil {
		return [20]byte{}, fmt.Errorf("failed to receive handshake: %v", err)
	}

	if response[0] != byte(len(handshakePStr)) || string(response[1:20]) != handshakePStr {
		return [20]byte{}, fmt.Errorf("invalid protocol name in handshake")
	}

	if expectedInfoHash != ([20]byte{}) {
		var recvInfoHash [20]byte
		copy(recvInfoHash[:], response[28:48])
		if recvInfoHash != expectedInfoHash {
			return [20]byte{}, fmt.Errorf("unexpected info hash in handshake")
		}
	}

	var remotePeerID [20]byte
	copy(remotePeerID[:], response[48:68])
	return remotePeerID, nil
}

// SendMessage sends a message to the peer.
func SendMessage(conn net.Conn, msg Message) error {
	conn.SetWriteDeadline(time.Now().Add(connTimeout))
	_, err := conn.Write(msg.Encode())
	return err
}

// maxMessageSize caps a single protocol message at 32 MB to prevent OOM.
const maxMessageSize = 32 * 1024 * 1024

// ReadMessage reads a message from the peer.
func ReadMessage(conn net.Conn) (Message, error) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return Message{}, err
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		// Keep-alive - return empty message
		return Message{ID: -1}, nil
	}
	if length > maxMessageSize {
		return Message{}, fmt.Errorf("message size %d exceeds limit", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return Message{}, err
	}

	return Message{
		ID:      int(payload[0]),
		Payload: payload[1:],
	}, nil
}

// Message represents a BitTorrent protocol message.
type Message struct {
	ID      int
	Payload []byte
}

// Encode encodes a message to bytes.
func (m Message) Encode() []byte {
	if m.ID == -1 {
		// Keep-alive message is just 4 bytes of 0
		return []byte{0, 0, 0, 0}
	}

	length := uint32(1 + len(m.Payload))
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.BigEndian, length)
	buf.WriteByte(byte(m.ID))
	buf.Write(m.Payload)

	return buf.Bytes()
}

// String returns a string representation of the message.
func (m Message) String() string {
	switch m.ID {
	case -1:
		return "KeepAlive"
	case MsgChoke:
		return "Choke"
	case MsgUnchoke:
		return "Unchoke"
	case MsgInterested:
		return "Interested"
	case MsgNotInterested:
		return "NotInterested"
	case MsgHave:
		return "Have"
	case MsgBitfield:
		return "Bitfield"
	case MsgRequest:
		return "Request"
	case MsgPiece:
		return "Piece"
	case MsgCancel:
		return "Cancel"
	case MsgExtended:
		return "Extended"
	default:
		return "Unknown"
	}
}

// RequestMessage creates a request message for a piece.
func RequestMessage(index, begin, length uint32) Message {
	payload := new(bytes.Buffer)
	binary.Write(payload, binary.BigEndian, index)
	binary.Write(payload, binary.BigEndian, begin)
	binary.Write(payload, binary.BigEndian, length)

	return Message{
		ID:      MsgRequest,
		Payload: payload.Bytes(),
	}
}

// ParsePieceMessage parses a piece message.
func ParsePieceMessage(payload []byte) (index, begin uint32, block []byte, err error) {
	if len(payload) < 8 {
		err = fmt.Errorf("invalid piece message length")
		return
	}

	index = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	block = payload[8:]

	return
}

// InterestedMessage creates an interested message.
func InterestedMessage() Message {
	return Message{ID: MsgInterested}
}

// BitfieldMessage creates a bitfield message.
func BitfieldMessage(bitfield []byte) Message {
	return Message{
		ID:      MsgBitfield,
		Payload: bitfield,
	}
}
