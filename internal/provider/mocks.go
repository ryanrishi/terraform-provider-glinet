package provider

import (
	"github.com/ryanrishi/glinet-client-go"
	"github.com/stretchr/testify/mock"
)

// MockGLiNetClient implements the GLiNetClient interface using testify/mock
type MockGLiNetClient struct {
	mock.Mock
}

func (m *MockGLiNetClient) System() SystemService {
	args := m.Called()
	return args.Get(0).(SystemService)
}

// MockSystemService implements the SystemService interface using testify/mock
type MockSystemService struct {
	mock.Mock
}

func (m *MockSystemService) GetTimezoneConfig() (*glinet.GetSystemTimezoneConfigResponse, error) {
	args := m.Called()
	return args.Get(0).(*glinet.GetSystemTimezoneConfigResponse), args.Error(1)
}

// Ensure mock implements our interface
var _ GLiNetClient = (*MockGLiNetClient)(nil)
var _ SystemService = (*MockSystemService)(nil)

// NewMockGLiNetClient creates a new mock client with no preset expectations
// Tests must configure their own expectations
func NewMockGLiNetClient() *MockGLiNetClient {
	return &MockGLiNetClient{}
}

// NewMockSystemService creates a new mock system service with no preset expectations
// Tests must configure their own expectations
func NewMockSystemService() *MockSystemService {
	return &MockSystemService{}
}
