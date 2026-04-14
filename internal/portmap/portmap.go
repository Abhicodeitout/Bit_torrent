package portmap

import (
	"fmt"
	"time"

	"torrent-client/internal/natpmp"
	"torrent-client/internal/upnp"
)

// PortMapper handles NAT port mapping via UPnP or NAT-PMP.
type PortMapper interface {
	// MapPort attempts to map the given port and returns external IP.
	MapPort(internalPort uint16) (externalIP string, mappedPort uint16, err error)
	// UnmapPort removes the port mapping.
	UnmapPort(port uint16) error
}

// Mapper implements PortMapper.
type Mapper struct {
	device   *upnp.Device
	natpmp   *natpmp.Client
	protocol string
	port     uint16
}

// NewMapper discovers and creates a PortMapper using UPnP or NAT-PMP.
func NewMapper(timeout time.Duration) (*Mapper, error) {
	m := &Mapper{}

	// Try UPnP first
	device, err := upnp.Discover(timeout)
	if err == nil {
		m.device = device
		m.protocol = "upnp"
		return m, nil
	}

	// Fallback to NAT-PMP
	client, err := natpmp.Discover(timeout)
	if err == nil {
		m.natpmp = client
		m.protocol = "natpmp"
		return m, nil
	}

	return nil, fmt.Errorf("no NAT mapping protocol available (tried UPnP and NAT-PMP)")
}

// MapPort maps the internal port to external and returns external IP and mapped port.
func (m *Mapper) MapPort(internalPort uint16) (string, uint16, error) {
	if m == nil {
		return "", 0, fmt.Errorf("mapper not initialized")
	}

	if m.protocol == "upnp" {
		externalIP, err := m.device.GetExternalAddress()
		if err != nil {
			return "", 0, fmt.Errorf("upnp get external address: %w", err)
		}

		mapping := upnp.PortMapping{
			Protocol:      "TCP",
			ExternalPort:  internalPort,
			InternalPort:  internalPort,
			InternalAddr:  "0.0.0.0",
			Description:   "BitTorrent client",
			LeaseDuration: 3600,
		}
		if err := m.device.AddPortMapping(mapping); err != nil {
			return "", 0, fmt.Errorf("upnp add mapping: %w", err)
		}

		m.port = internalPort
		return externalIP, internalPort, nil
	}

	if m.protocol == "natpmp" {
		externalIP, err := m.natpmp.GetExternalAddress(5 * time.Second)
		if err != nil {
			return "", 0, fmt.Errorf("natpmp get external address: %w", err)
		}

		mapping := natpmp.PortMapping{
			Protocol:     "TCP",
			InternalPort: internalPort,
			ExternalPort: internalPort,
			Lifetime:     3600,
		}
		_, err = m.natpmp.AddPortMapping(mapping, 5*time.Second)
		if err != nil {
			return "", 0, fmt.Errorf("natpmp add mapping: %w", err)
		}

		m.port = internalPort
		return externalIP, internalPort, nil
	}

	return "", 0, fmt.Errorf("unknown protocol")
}

// UnmapPort removes the port mapping.
func (m *Mapper) UnmapPort(port uint16) error {
	if m == nil {
		return nil
	}

	if m.protocol == "upnp" {
		return m.device.RemovePortMapping("TCP", port)
	}

	if m.protocol == "natpmp" {
		// NAT-PMP doesn't have explicit unmapping; mappings expire
		return nil
	}

	return nil
}

// Protocol returns the mapping protocol used.
func (m *Mapper) Protocol() string {
	if m != nil {
		return m.protocol
	}
	return ""
}
