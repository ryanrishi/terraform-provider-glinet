package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"glinet": providerserver.NewProtocol6WithError(New("test")()),
}

// realRouter reports whether tests run against the router at GLINET_HOST instead of a fake router.
// The provider then reads GLINET_HOST, GLINET_USERNAME and GLINET_PASSWORD from the environment.
func realRouter() bool {
	return os.Getenv(resource.EnvTfAcc) != "" && os.Getenv("GLINET_HOST") != ""
}

func providerConfig(host, username, password string) string {
	return fmt.Sprintf(`
provider "glinet" {
  host     = %q
  username = %q
  password = %q
}
`, host, username, password)
}

func fakeRouterConfig(srv *glinettest.Server) string {
	return providerConfig(srv.URL, srv.Username, srv.Password)
}

const timezoneConfigDataSourceConfig = `
data "glinet_system_timezone_config" "test" {}
`

func TestProviderWrongPassword(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(srv.URL, srv.Username, "wrong") + timezoneConfigDataSourceConfig,
				ExpectError: regexp.MustCompile(`(?s)Unable to Create GL\.iNet API Client.*Access denied`),
			},
		},
	})
}

func TestProviderMissingPassword(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_PASSWORD", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "glinet" {
  host = %q
}
`, srv.URL) + timezoneConfigDataSourceConfig,
				ExpectError: regexp.MustCompile(`Missing GL\.iNet API Password`),
			},
		},
	})
}
