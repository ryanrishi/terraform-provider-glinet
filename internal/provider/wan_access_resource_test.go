package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// The tests of glinet_wan_access always use the fake router: the write can cut the management of the router over the WAN.

const wanAccessAddress = "glinet_wan_access.test"

func expectWANAccess(srv *glinettest.Server, want glinettest.WANAccess) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Firewall.WANAccess(); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("WAN access is %+v, want %+v", got, want)
		}
		return nil
	}
}

// TestWANAccessResource sets all attributes, removes the allow list, and checks that destroy disables the access.
func TestWANAccessResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "firewall", "set_wan_access")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(`{"enable_https":false,"enable_ssh":false,"enable_ping":false,"enable_whitelist":false,"whitelist":[]}`),
			expectWANAccess(srv, glinettest.WANAccess{Whitelist: []glinettest.WANAccessSource{}}),
		),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_wan_access" "test" {
  https_enabled             = true
  ssh_enabled               = false
  ping_enabled              = true
  allow_list_enabled = true
  allow_list = {
    "203.0.113.10" = "office"
    "2001:db8::10" = ""
  }
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("id"), knownvalue.StringExact("wan_access")),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("https_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("ssh_enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("ping_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("allow_list_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("allow_list"), knownvalue.MapExact(map[string]knownvalue.Check{
						"203.0.113.10": knownvalue.StringExact("office"),
						"2001:db8::10": knownvalue.StringExact(""),
					})),
				},
				Check: calls.expect(`{"enable_https":true,"enable_ssh":false,"enable_ping":true,"enable_whitelist":true,` +
					`"whitelist":[{"ipaddr":"2001:db8::10","name":""},{"ipaddr":"203.0.113.10","name":"office"}]}`),
			},
			{
				ResourceName:      wanAccessAddress,
				ImportState:       true,
				ImportStateId:     "wan_access",
				ImportStateVerify: true,
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_wan_access" "test" {
  https_enabled             = false
  ssh_enabled               = true
  ping_enabled              = false
  allow_list_enabled = false
  allow_list         = {}
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("allow_list"), knownvalue.MapExact(map[string]knownvalue.Check{})),
				},
				Check: calls.expect(`{"enable_https":false,"enable_ssh":true,"enable_ping":false,"enable_whitelist":false,"whitelist":[]}`),
			},
		},
	})
}

// TestWANAccessResourceKeepsRouterAllowList sends back the allow list and its switch of the router when they are not set.
func TestWANAccessResourceKeepsRouterAllowList(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetWANAccess(glinettest.WANAccess{
		EnableWhitelist: true,
		Whitelist:       []glinettest.WANAccessSource{{IPAddr: "198.51.100.7", Name: "home"}},
	})
	calls := recordCalls(t, srv, "firewall", "set_wan_access")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_wan_access" "test" {
  https_enabled = false
  ssh_enabled   = true
  ping_enabled  = false
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("allow_list_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(wanAccessAddress, tfjsonpath.New("allow_list"), knownvalue.MapExact(map[string]knownvalue.Check{
						"198.51.100.7": knownvalue.StringExact("home"),
					})),
				},
				Check: calls.expect(`{"enable_https":false,"enable_ssh":true,"enable_ping":false,"enable_whitelist":true,` +
					`"whitelist":[{"ipaddr":"198.51.100.7","name":"home"}]}`),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_wan_access" "test" {
  https_enabled = false
  ssh_enabled   = true
  ping_enabled  = true
}
`,
				Check: calls.expect(`{"enable_https":false,"enable_ssh":true,"enable_ping":true,"enable_whitelist":true,` +
					`"whitelist":[{"ipaddr":"198.51.100.7","name":"home"}]}`),
			},
		},
	})
}

func TestWANAccessResourceInvalidConfig(t *testing.T) {
	parallel(t)
	for name, tc := range map[string]struct {
		source string
		err    string
	}{
		"prefix length": {`"203.0.113.0/24" = ""`, `must be an IPv4 or IPv6 address`},
		"host name":     {`"router.example.com" = ""`, `must be an IPv4 or IPv6 address`},
		"long name":     {fmt.Sprintf(`"203.0.113.10" = %q`, strings.Repeat("a", 65)), `string length must be at most 64`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: fakeRouterConfig(glinettest.NewServer(t)) + `
resource "glinet_wan_access" "test" {
  https_enabled     = true
  ssh_enabled       = false
  ping_enabled      = false
  allow_list = { ` + tc.source + ` }
}
`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

func TestWANAccessResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeRouterConfig(srv) + `resource "glinet_wan_access" "test" {
  https_enabled = false
  ssh_enabled   = false
  ping_enabled  = false
}`,
			ResourceName:  wanAccessAddress,
			ImportState:   true,
			ImportStateId: "remote",
			ExpectError:   regexp.MustCompile(`Invalid Import ID`),
		}},
	})
}
