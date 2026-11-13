package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/ryanrishi/glinet-client-go"
)

const (
	// providerConfig is a shared configuration to combine with the actual
	// test configuration. Since we're using mocks, the values don't matter
	// but are required by the provider schema.
	providerConfig = `
provider "glinet" {
	username = "test"
	password = "password"
}
`
)

var (
	// testAccProtoV6ProviderFactories are used to instantiate a provider during
	// acceptance testing. The factory function will be invoked for every Terraform
	// CLI command executed to create a provider server to which the CLI can
	// reattach.
	testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
		"glinet": providerserver.NewProtocol6WithError(NewWithClientFactory("test", func(host, username string, password []byte) interface{} {
			// Return a configured mock for acceptance tests
			mockSystemService := NewMockSystemService()
			mockClient := NewMockGLiNetClient()

			// Configure default acceptance test expectations
			mockSystemService.On("GetTimezoneConfig").Return(&glinet.GetSystemTimezoneConfigResponse{
				Zonename:            "Asia/Shanghai",
				TZOffset:            "+0800",
				AutoTimezoneEnabled: true,
				Localtime:           1643200134,
				Timezone:            "CST-8",
			}, nil)

			mockClient.On("System").Return(mockSystemService)
			return mockClient
		})()),
	}
)
