package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
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

func readsTimezoneConfig(config string) resource.TestCase {
	return resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config + timezoneConfigDataSourceConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezoneConfig("id", knownvalue.NotNull()),
				},
			},
		},
	}
}

func TestProviderConfigFromEnvironment(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_HOST", srv.URL)
	t.Setenv("GLINET_USERNAME", srv.Username)
	t.Setenv("GLINET_PASSWORD", srv.Password)

	resource.UnitTest(t, readsTimezoneConfig(`provider "glinet" {}`))
}

func TestProviderConfigOverridesEnvironment(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_HOST", "http://127.0.0.1:1")
	t.Setenv("GLINET_USERNAME", "not-"+srv.Username)
	t.Setenv("GLINET_PASSWORD", "not-"+srv.Password)

	resource.UnitTest(t, readsTimezoneConfig(fakeRouterConfig(srv)))
}

func TestProviderDefaultUsername(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.Username = defaultUsername
	t.Setenv("GLINET_USERNAME", "")

	resource.UnitTest(t, readsTimezoneConfig(fmt.Sprintf(`
provider "glinet" {
  host     = %q
  password = %q
}
`, srv.URL, srv.Password)))
}

func TestValueOrEnv(t *testing.T) {
	const envVar = "GLINET_TEST_VALUE"

	tests := []struct {
		name  string
		value types.String
		env   string
		want  string
	}{
		{"config value", types.StringValue("config"), "env", "config"},
		{"empty config value", types.StringValue(""), "env", "env"},
		{"environment", types.StringNull(), "env", "env"},
		{"fallback", types.StringNull(), "", "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envVar, tt.env)
			if got := valueOrEnv(tt.value, envVar, "fallback"); got != tt.want {
				t.Errorf("valueOrEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}
