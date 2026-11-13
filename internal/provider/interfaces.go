package provider

import "github.com/ryanrishi/glinet-client-go"

// GLiNetClient interface defines the methods we need from the glinet client
type GLiNetClient interface {
	System() SystemService
}

// SystemService interface defines the methods we need from the system service
type SystemService interface {
	GetTimezoneConfig() (*glinet.GetSystemTimezoneConfigResponse, error)
}

// Ensure real client implements our interface
var _ GLiNetClient = (*ClientWrapper)(nil)

// ClientWrapper wraps the real glinet client to implement our interface
type ClientWrapper struct {
	*glinet.Client
}

func (c *ClientWrapper) System() SystemService {
	return &SystemServiceWrapper{c.Client.System}
}

// SystemServiceWrapper wraps the real system service to implement our interface
type SystemServiceWrapper struct {
	*glinet.SystemService
}

func (s *SystemServiceWrapper) GetTimezoneConfig() (*glinet.GetSystemTimezoneConfigResponse, error) {
	return s.SystemService.GetTimezoneConfig()
}
