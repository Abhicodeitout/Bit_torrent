package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const (
	connTimeout = 10 * time.Second
	readTimeout = 10 * time.Second
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
	// Prepare handshake message
	// Protocol: <pstrlen><pstr><reserved><info_hash><peer_id>
	handshakeMsg := new(bytes.Buffer)

	// Write protocol name
	pstr := "BitTorrent protocol"
	handshakeMsg.WriteByte(byte(len(pstr)))
	handshakeMsg.WriteString(pstr)

	// Write reserved bytes (8 bytes)
	reserved := make([]byte, 8)
	handshakeMsg.Write(reserved)

	// Write info hash
	handshakeMsg.Write(infoHash[:])

	// Write peer ID
	handshakeMsg.Write(peerID[:])

	// Send handshake
	conn.SetWriteDeadline(time.Now().Add(connTimeout))
	_, err := conn.Write(handshakeMsg.Bytes())
	if err != nil {
		return [20]byte{}, fmt.Errorf("failed to send handshake: %v", err)
	}

	// Receive handshake response
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	response := make([]byte, 68) // 1 + 19 + 8 + 20 + 20
	_, err = conn.Read(response)
	if err != nil {
		return [20]byte{}, fmt.Errorf("failed to receive handshake: %v", err)
	}

	// Verify protocol name
	if response[0] != byte(len(pstr)) || string(response[1:20]) != pstr {
		return [20]byte{}, fmt.Errorf("invalid protocol name in handshake")
	}

	// Extract peer ID from response
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

// ReadMessage reads a message from the peer.
func ReadMessage(conn net.Conn) (Message, error) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	lengthBuf := make([]byte, 4)
	_, err := conn.Read(lengthBuf)
	if err != nil {
		return Message{}, err
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		// Keep-alive - return empty message
		return Message{ID: -1}, nil
	}

	payload := make([]byte, length)
	_, err = conn.Read(payload)
	if err != nil {
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
