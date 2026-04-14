package upnp

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// PortMapping represents a UPnP port mapping request.
type PortMapping struct {
	Protocol      string // "TCP" or "UDP"
	ExternalPort  uint16
	InternalPort  uint16
	InternalAddr  string
	Description   string
	LeaseDuration uint32 // 0 for permanent
}

// Device represents a UPnP device.
type Device struct {
	ServiceURL string
}

// Discover attempts to find a UPnP-capable gateway device on the LAN.
// Returns Device if found, nil otherwise.
func Discover(timeout time.Duration) (*Device, error) {
	// SSDP multicast request for Internet Gateway Device (IGD)
	ssdpRequest := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n" +
		"ST: ssdp:all\r\n" +
		"\r\n"

	conn, err := net.DialTimeout("udp", "239.255.255.250:1900", timeout)
	if err != nil {
		return nil, fmt.Errorf("upnp discovery failed: %w", err)
	}
	defer conn.Close()

	// Send SSDP request
	if _, err := conn.Write([]byte(ssdpRequest)); err != nil {
		return nil, fmt.Errorf("upnp ssdp send failed: %w", err)
	}

	// Wait for responses
	conn.SetReadDeadline(time.Now().Add(timeout))
	buffer := make([]byte, 4096)

	for {
		n, _, err := conn.(*net.UDPConn).ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return nil, fmt.Errorf("upnp discovery timeout: no gateway found")
			}
			return nil, fmt.Errorf("upnp discovery read failed: %w", err)
		}

		response := string(buffer[:n])
		if strings.Contains(response, "InternetGatewayDevice") {
			// Extract location header
			lines := strings.Split(response, "\r\n")
			for _, line := range lines {
				if strings.HasPrefix(strings.ToLower(line), "location:") {
					locationURL := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "location:"))
					if locationURL == "" {
						locationURL = strings.TrimSpace(strings.TrimPrefix(line, "Location:"))
						locationURL = strings.TrimSpace(strings.TrimPrefix(line, "LOCATION:"))
					}
					return &Device{ServiceURL: extractServiceURL(locationURL)}, nil
				}
			}
		}
	}
}

// AddPortMapping adds a port mapping to the UPnP device.
func (d *Device) AddPortMapping(mapping PortMapping) error {
	if d == nil || d.ServiceURL == "" {
		return fmt.Errorf("invalid device")
	}

	// Build UPnP AddPortMapping SOAP request
	soapBody := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:AddPortMapping xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewRemoteHost></NewRemoteHost>
      <NewExternalPort>%d</NewExternalPort>
      <NewProtocol>%s</NewProtocol>
      <NewInternalPort>%d</NewInternalPort>
      <NewInternalClient>%s</NewInternalClient>
      <NewEnabled>1</NewEnabled>
      <NewPortMappingDescription>%s</NewPortMappingDescription>
      <NewLeaseDuration>%d</NewLeaseDuration>
    </u:AddPortMapping>
  </s:Body>
</s:Envelope>`, mapping.ExternalPort, mapping.Protocol, mapping.InternalPort, mapping.InternalAddr, mapping.Description, mapping.LeaseDuration)

	return d.sendSOAP("AddPortMapping", soapBody)
}

// RemovePortMapping removes a port mapping from the UPnP device.
func (d *Device) RemovePortMapping(protocol string, externalPort uint16) error {
	if d == nil || d.ServiceURL == "" {
		return fmt.Errorf("invalid device")
	}

	soapBody := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:DeletePortMapping xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewRemoteHost></NewRemoteHost>
      <NewExternalPort>%d</NewExternalPort>
      <NewProtocol>%s</NewProtocol>
    </u:DeletePortMapping>
  </s:Body>
</s:Envelope>`, externalPort, protocol)

	return d.sendSOAP("DeletePortMapping", soapBody)
}

// GetExternalAddress retrieves the external IP address from the UPnP device.
func (d *Device) GetExternalAddress() (string, error) {
	if d == nil || d.ServiceURL == "" {
		return "", fmt.Errorf("invalid device")
	}

	soapBody := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:GetExternalIPAddress xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
    </u:GetExternalIPAddress>
  </s:Body>
</s:Envelope>`

	resp, err := d.sendSOAPAndReceive("GetExternalIPAddress", soapBody)
	if err != nil {
		return "", err
	}

	// Parse response for NewExternalIPAddress
	start := strings.Index(resp, "<NewExternalIPAddress>")
	if start == -1 {
		return "", fmt.Errorf("external ip not found in response")
	}
	start += len("<NewExternalIPAddress>")
	end := strings.Index(resp[start:], "</NewExternalIPAddress>")
	if end == -1 {
		return "", fmt.Errorf("malformed response")
	}
	return resp[start : start+end], nil
}

// sendSOAP sends a SOAP request without expecting a response.
func (d *Device) sendSOAP(action string, soapBody string) error {
	_, err := d.sendSOAPAndReceive(action, soapBody)
	return err
}

// sendSOAPAndReceive sends a SOAP request and returns the response.
func (d *Device) sendSOAPAndReceive(action string, soapBody string) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequest("POST", d.ServiceURL, bytes.NewBufferString(soapBody))
	if err != nil {
		return "", fmt.Errorf("upnp request creation failed: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
	req.Header.Set("SOAPAction", fmt.Sprintf(`"urn:schemas-upnp-org:service:WANIPConnection:1#%s"`, action))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upnp soap request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("upnp response read failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("upnp soap error: %s (status %d)", string(body), resp.StatusCode)
	}

	return string(body), nil
}

// extractServiceURL extracts the control service URL from a device description URL.
// This is a simplified version; full implementation would parse XML device descriptor.
func extractServiceURL(deviceURL string) string {
	// For simplicity, assume service is at /upnp/control/WANIPConnection1
	// Real implementation would fetch and parse device descriptor XML.
	parts := strings.Split(deviceURL, "/")
	if len(parts) >= 3 {
		baseURL := strings.Join(parts[:3], "/")
		return baseURL + ":49152/upnp/control/WANIPConnection1"
	}
	return deviceURL
}
