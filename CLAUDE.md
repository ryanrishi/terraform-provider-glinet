# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

This is a Terraform provider for managing GL.iNet routers running v4 firmware. The provider allows Terraform to interact with GL.iNet router APIs for configuration management.

## Development Commands

### Building
```bash
go install
```

### Testing
```bash
# Run acceptance tests (creates real resources)
make testacc

# Run with specific test arguments
TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m
```

### Documentation
```bash
# Generate or update documentation
go generate
```

### Dependencies
```bash
# Add new dependency
go get github.com/author/dependency
go mod tidy
```

## Architecture

### Provider Structure
- **Provider**: `internal/provider/provider.go` - Main provider implementation using Terraform Plugin Framework
- **Configuration**: Provider accepts `host`, `username`, and `password` parameters
- **Environment Variables**: `GLINET_HOST`, `GLINET_USERNAME`, `GLINET_PASSWORD`
- **Default Host**: `http://192.168.8.1` when not specified

### Client Integration
- Uses `github.com/ryanrishi/glinet-client-go` for API communication
- Client is configured in the provider's Configure method and passed to data sources/resources

### Current Implementation
- **Data Sources**: `system_timezone_config_data_source.go`
- **Resources**: none registered yet

### Testing Infrastructure
- Uses Terraform Plugin Testing framework
- Pure Go mocking with `testify/mock` — see Testing Architecture below

### File Structure
```
internal/provider/           # Provider implementation
├── provider.go             # Main provider logic
├── *_data_source.go        # Data source implementations
├── *_resource.go           # Resource implementations (future)
└── *_test.go              # Tests

examples/                   # Example Terraform configurations
docs/                      # Generated documentation
tools/tools.go             # Development tools
```

## Development Notes

### Adding New Data Sources/Resources
1. Create new file in `internal/provider/`
2. Implement the respective interface (datasource.DataSource or resource.Resource)
3. Register in provider.go DataSources() or Resources() method
4. Add tests and documentation

### Testing Environment
- Acceptance tests require `TF_ACC=1` environment variable
- Tests create real resources and may incur costs
- Unit tests use the `testify/mock` doubles, so they need no router and no `TF_ACC`

## Testing Architecture

### Pure Go Mocking with testify/mock
The provider uses a flexible mocking approach where:
- Tests provide their own data (no hardcoded values in mocks)
- Call verification ensures mocks are used correctly
- Scales perfectly for future services (AdGuard, VPN, etc.)

```go
// Example test pattern
mockSystemService := NewMockSystemService()
mockClient := NewMockGLiNetClient()

// Test configures exactly what it needs
mockSystemService.On("GetTimezoneConfig").Return(testData, nil)
mockClient.On("System").Return(mockSystemService)

// Verify calls were made as expected
mockSystemService.AssertExpectations(t)
mockClient.AssertExpectations(t)
```

### Test Coverage
- **Provider tests**: Schema, metadata, configuration, constructor functions
- **Data source tests**: Read operations, error handling, schema validation
- **Interface tests**: Mock implementations, wrapper compliance
- **Business logic tests**: Different configs, error scenarios, call verification
