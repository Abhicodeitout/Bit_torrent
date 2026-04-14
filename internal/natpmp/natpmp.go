package natpmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const (
	// NAT-PMP version
	nempVersion = 0

	// NAT-PMP opcodes
	opcodePubAddrRequest = 0
	opaodeMapUDP         = 1
	opcodeMaptCP         = 2

	// Response opcodes
	responsePubAddr = 128
	responseMapUDP  = 129
	responseMapTCP  = 130
)

// PortMapping represents a NAT-PMP port mapping.
type PortMapping struct {
	Protocol     string // "TCP" or "UDP"
	InternalPort uint16
	ExternalPort uint16
	Lifetime     uint32 // seconds
}

// Client represents a NAT-PMP client.
type Client struct {
	addr net.Addr
}

// Discover attempts to find the gateway address for NAT-PMP.
// Typically the gateway is at 192.168.1.1 or 192.168.0.1.
func Discover(timeout time.Duration) (*Client, error) {
	// Try common gateway addresses
	gateways := []string{
		"192.168.1.1:5351",
		"192.168.0.1:5351",
		"10.0.0.1:5351",
	}

	for _, gwAddr := range gateways {
		addr, err := net.ResolveUDPAddr("udp", gwAddr)
		if err != nil {
			continue
		}

		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			continue
		}
		defer conn.Close()

		// Try to get external address
		req := bytes.Buffer{}
		req.WriteByte(nempVersion)
		req.WriteByte(opcodePubAddrRequest)
		req.WriteString("\x00\x00") // reserved

		conn.SetReadDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(req.Bytes()); err != nil {
			continue
		}

		resp := make([]byte, 12)
		n, _, err := conn.ReadFromUDP(resp)
		if err != nil || n < 12 {
			continue
		}

		if resp[1] == responsePubAddr {
			return &Client{addr: addr}, nil
		}
	}

	return nil, fmt.Errorf("natpmp gateway not found")
}

// AddPortMapping adds a port mapping via NAT-PMP.
func (c *Client) AddPortMapping(mapping PortMapping, timeout time.Duration) (uint16, error) {
	var opcode byte
	if mapping.Protocol == "UDP" {
		opcode = opaodeMapUDP
	} else {
		opcode = opcodeMaptCP
	}

	conn, err := net.DialUDP("udp", nil, c.addr.(*net.UDPAddr))
	if err != nil {
		return 0, fmt.Errorf("natpmp dial failed: %w", err)
	}
	defer conn.Close()

	// Build NAT-PMP request
	req := bytes.Buffer{}
	req.WriteByte(nempVersion)
	req.WriteByte(opcode)
	req.WriteString("\x00\x00") // reserved
	binary.Write(&req, binary.BigEndian, mapping.InternalPort)
	binary.Write(&req, binary.BigEndian, mapping.ExternalPort)
	binary.Write(&req, binary.BigEndian, mapping.Lifetime)

	conn.SetReadDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(req.Bytes()); err != nil {
		return 0, fmt.Errorf("natpmp send failed: %w", err)
	}

	// Read response
	resp := make([]byte, 16)
	n, _, err := conn.ReadFromUDP(resp)
	if err != nil {
		return 0, fmt.Errorf("natpmp response failed: %w", err)
	}

	if n < 12 {
		return 0, fmt.Errorf("natpmp response too short")
	}

	// Check response type
	if resp[1] != responsePubAddr && resp[1] != responseMapUDP && resp[1] != responseMapTCP {
		resultCode := binary.BigEndian.Uint16(resp[2:4])
		return 0, fmt.Errorf("natpmp error code: %d", resultCode)
	}

	// Extract external port from response
	externalPort := binary.BigEndian.Uint16(resp[8:10])
	return externalPort, nil
}

// GetExternalAddress retrieves the external IP address via NAT-PMP.
func (c *Client) GetExternalAddress(timeout time.Duration) (string, error) {
	conn, err := net.DialUDP("udp", nil, c.addr.(*net.UDPAddr))
	if err != nil {
		return "", fmt.Errorf("natpmp dial failed: %w", err)
	}
	defer conn.Close()

	req := bytes.Buffer{}
	req.WriteByte(nempVersion)
	req.WriteByte(opcodePubAddrRequest)
	req.WriteString("\x00\x00") // reserved

	conn.SetReadDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(req.Bytes()); err != nil {
		return "", fmt.Errorf("natpmp send failed: %w", err)
	}

	resp := make([]byte, 12)
	n, _, err := conn.ReadFromUDP(resp)
	if err != nil {
		return "", fmt.Errorf("natpmp response failed: %w", err)
	}

	if n < 12 || resp[1] != responsePubAddr {
		return "", fmt.Errorf("invalid natpmp response")
	}

	// Extract IP address (bytes 4-7)
	ip := net.IPv4(resp[4], resp[5], resp[6], resp[7])
	return ip.String(), nil
}
