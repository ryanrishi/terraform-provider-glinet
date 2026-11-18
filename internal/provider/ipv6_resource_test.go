package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	ipv6Address = "glinet_ipv6.test"

	// ipv6Initial is the IPv6 configuration of the fake router when a test starts.
	ipv6Initial = `{"enable":false,"lan_dns_mode":true,"lan_mode":"nat6"}`

	googleDNS1 = "2001:4860:4860::8888"
	googleDNS2 = "2001:4860:4860::8844"
)

func ipv6Config(body string) string {
	return "\nresource \"glinet_ipv6\" \"test\" {\n" + body + "\n}\n"
}

func expectIPv6State(checks map[string]knownvalue.Check) []statecheck.StateCheck {
	all := map[string]knownvalue.Check{
		"id":           knownvalue.StringExact("ipv6"),
		"enabled":      knownvalue.NotNull(),
		"lan_mode":     knownvalue.Null(),
		"lan_dns_auto": knownvalue.Bool(true),
		"lan_ip":       knownvalue.Null(),
		"lan_dns":      knownvalue.Null(),
	}
	for k, v := range checks {
		all[k] = v
	}
	return expectAttributes(ipv6Address, all)
}

const ipv6StaticDisabled = `
enabled      = false
lan_mode     = "static"
lan_ip       = "fd00:1::1/64"
lan_dns_auto = false
lan_dns      = ["` + googleDNS1 + `"]`

// TestIPv6Resource creates the resource with IPv6 disabled and values, which writes only enable: false,
// enables IPv6 later and changes each attribute.
func TestIPv6Resource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "ipv6", "set_ipv6")

	staticState := map[string]knownvalue.Check{
		"enabled":      knownvalue.Bool(true),
		"lan_mode":     knownvalue.StringExact("static"),
		"lan_ip":       knownvalue.StringExact("fd00:1::1/64"),
		"lan_dns_auto": knownvalue.Bool(false),
		"lan_dns":      dnsServers(googleDNS1),
	}

	type step struct {
		body, params, router string
		state                map[string]knownvalue.Check
	}
	steps := []step{
		{
			ipv6StaticDisabled,
			`{"enable":false}`,
			ipv6Initial,
			map[string]knownvalue.Check{
				"enabled":      knownvalue.Bool(false),
				"lan_mode":     knownvalue.StringExact("static"),
				"lan_ip":       knownvalue.StringExact("fd00:1::1/64"),
				"lan_dns_auto": knownvalue.Bool(false),
				"lan_dns":      dnsServers(googleDNS1),
			},
		},
		{
			`
enabled      = true
lan_mode     = "static"
lan_ip       = "fd00:1::1/64"
lan_dns_auto = false
lan_dns      = ["` + googleDNS1 + `"]`,
			`{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:1::1/64","lan_dns1":"` + googleDNS1 + `"}`,
			`{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:1::1/64","lan_dns1":"` + googleDNS1 + `"}`,
			staticState,
		},
		{
			`
enabled      = true
lan_mode     = "static"
lan_ip       = "fd00:2::1/48"
lan_dns_auto = false
lan_dns      = ["` + googleDNS1 + `", "` + googleDNS2 + `"]`,
			`{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`,
			`{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{
				"enabled":      knownvalue.Bool(true),
				"lan_mode":     knownvalue.StringExact("static"),
				"lan_ip":       knownvalue.StringExact("fd00:2::1/48"),
				"lan_dns_auto": knownvalue.Bool(false),
				"lan_dns":      dnsServers(googleDNS1, googleDNS2),
			},
		},
		{
			`
enabled  = true
lan_mode = "nat6"`,
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`,
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":true,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "lan_mode": knownvalue.StringExact("nat6")},
		},
		{
			`
enabled      = true
lan_mode     = "native"
lan_dns_auto = false
lan_dns      = ["` + googleDNS2 + `"]`,
			`{"enable":true,"lan_mode":"native","lan_dns_mode":false,"lan_dns1":"` + googleDNS2 + `"}`,
			`{"enable":true,"lan_mode":"native","lan_dns_mode":false,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS2 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{
				"enabled":      knownvalue.Bool(true),
				"lan_mode":     knownvalue.StringExact("native"),
				"lan_dns_auto": knownvalue.Bool(false),
				"lan_dns":      dnsServers(googleDNS2),
			},
		},
		{
			`
enabled  = true
lan_mode = "relay"`,
			`{"enable":true,"lan_mode":"relay","lan_dns_mode":true}`,
			`{"enable":true,"lan_mode":"relay","lan_dns_mode":true,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS2 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "lan_mode": knownvalue.StringExact("relay")},
		},
		{
			`
enabled  = false
lan_mode = "relay"`,
			`{"enable":false}`,
			`{"enable":false,"lan_mode":"relay","lan_dns_mode":true,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS2 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{"enabled": knownvalue.Bool(false), "lan_mode": knownvalue.StringExact("relay")},
		},
		{
			`enabled = false`,
			`{"enable":false}`,
			`{"enable":false,"lan_mode":"relay","lan_dns_mode":true,"lan_ip":"fd00:2::1/48","lan_dns1":"` + googleDNS2 + `","lan_dns2":"` + googleDNS2 + `"}`,
			map[string]knownvalue.Check{"enabled": knownvalue.Bool(false)},
		},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		ts := resource.TestStep{
			Config:            fakeRouterConfig(srv) + ipv6Config(s.body),
			ConfigStateChecks: expectIPv6State(s.state),
			Check: resource.ComposeTestCheckFunc(
				calls.expect(s.params),
				expectRouterJSON(t, srv.IPv6.Config, s.router),
			),
		}
		if i > 0 {
			ts.ConfigPlanChecks = expectResourceAction(ipv6Address, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, ts)
		if i == 2 {
			testSteps = append(testSteps, resource.TestStep{
				ResourceName:      ipv6Address,
				ImportState:       true,
				ImportStateId:     "ipv6",
				ImportStateVerify: true,
			})
		}
	}
	last := steps[len(steps)-1]

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			expectRouterJSON(t, srv.IPv6.Config, last.router),
		),
		Steps: testSteps,
	})
}

// TestIPv6ResourceDestroy destroys an enabled configuration, which leaves the router unchanged.
func TestIPv6ResourceDestroy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "ipv6", "set_ipv6")
	const router = `{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			expectRouterJSON(t, srv.IPv6.Config, router),
		),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ipv6Config("enabled = true\nlan_mode = \"nat6\""),
				Check:  calls.expect(router),
			},
		},
	})
}

func TestIPv6ResourceImport(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, router, body string
	}{
		{"disabled", ipv6Initial, "enabled = false\nlan_mode = \"nat6\""},
		{"static", `{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:1::1/64","lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`,
			"enabled = true\nlan_mode = \"static\"\nlan_ip = \"fd00:1::1/64\"\nlan_dns_auto = false\nlan_dns = [\"" + googleDNS1 + "\", \"" + googleDNS2 + "\"]"},
		{"nat6 with values of other modes", `{"enable":true,"lan_mode":"nat6","lan_dns_mode":true,"lan_ip":"fd00:1::1/64","lan_dns1":"` + googleDNS1 + `"}`,
			"enabled = true\nlan_mode = \"nat6\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.IPv6.SetConfig(json.RawMessage(tt.router))
			config := fakeRouterConfig(srv) + ipv6Config(tt.body)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       ipv6Address,
						ImportState:        true,
						ImportStateId:      "ipv6",
						ImportStatePersist: true,
					},
					{
						Config:           config,
						ConfigPlanChecks: expectEmptyPlan,
					},
				},
			})
		})
	}
}

// TestIPv6ResourceImportDisabledWithoutMode imports a disabled configuration into a configuration without lan_mode:
// one update writes enable: false, and then the plan is empty.
func TestIPv6ResourceImportDisabledWithoutMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "ipv6", "set_ipv6")
	config := fakeRouterConfig(srv) + ipv6Config("enabled = false")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       ipv6Address,
				ImportState:        true,
				ImportStateId:      "ipv6",
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(ipv6Address, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectIPv6State(map[string]knownvalue.Check{"enabled": knownvalue.Bool(false)}),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"enable":false}`),
					expectRouterJSON(t, srv.IPv6.Config, ipv6Initial),
				),
			},
		},
	})
}

func TestIPv6ResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + ipv6Config("enabled = false"),
				ResourceName:  ipv6Address,
				ImportState:   true,
				ImportStateId: "ipv4",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

const ipv6StaticEnabled = `
enabled      = true
lan_mode     = "static"
lan_ip       = "fd00:1::1/64"
lan_dns_auto = false
lan_dns      = ["` + googleDNS1 + `", "` + googleDNS2 + `"]`

func TestIPv6ResourceDrift(t *testing.T) {
	parallel(t)
	const static = `"lan_mode":"static","lan_ip":"fd00:1::1/64","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"`
	tests := []struct {
		name, body, router string
		plan               bool
	}{
		{"disabled", ipv6StaticEnabled, `{"enable":false,` + static + `}`, true},
		{"enabled", ipv6StaticDisabled, `{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`, true},
		{"lan_mode", ipv6StaticEnabled, `{"enable":true,"lan_mode":"native","lan_ip":"fd00:1::1/64","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`, true},
		{"lan_ip", ipv6StaticEnabled, `{"enable":true,"lan_mode":"static","lan_ip":"fd00:9::1/64","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`, true},
		{"lan_dns_auto", ipv6StaticEnabled, `{"enable":true,"lan_mode":"static","lan_ip":"fd00:1::1/64","lan_dns_mode":true,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"` + googleDNS2 + `"}`, true},
		{"first DNS server", ipv6StaticEnabled, `{"enable":true,"lan_mode":"static","lan_ip":"fd00:1::1/64","lan_dns_mode":false,"lan_dns1":"2001:db8::53","lan_dns2":"` + googleDNS2 + `"}`, true},
		{"second DNS server", ipv6StaticEnabled, `{"enable":true,"lan_mode":"static","lan_ip":"fd00:1::1/64","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"2001:db8::53"}`, true},
		{"second DNS server removed", ipv6StaticEnabled, `{"enable":true,"lan_mode":"static","lan_ip":"fd00:1::1/64","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `"}`, true},
		{"second DNS server not configured", "enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false\nlan_dns = [\"" + googleDNS1 + "\"]", `{"enable":true,"lan_mode":"nat6","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `","lan_dns2":"2001:db8::53"}`, false},
		{"values while disabled", ipv6StaticDisabled, `{"enable":false,"lan_mode":"relay","lan_dns_mode":true}`, false},
		{"values of other modes", "enabled = true\nlan_mode = \"nat6\"", `{"enable":true,"lan_mode":"nat6","lan_dns_mode":true,"lan_ip":"fd00:1::1/64","lan_dns1":"` + googleDNS1 + `"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			config := fakeRouterConfig(srv) + ipv6Config(tt.body)
			applied := saveRouterJSON(srv.IPv6.Config)
			drift := resource.TestStep{
				PreConfig:        func() { srv.IPv6.SetConfig(json.RawMessage(tt.router)) },
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			}
			if tt.plan {
				drift.ConfigPlanChecks = expectResourceAction(ipv6Address, plancheck.ResourceActionUpdate)
				drift.Check = applied.expect
			}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check:  applied.save,
					},
					drift,
				},
			})
		})
	}
}

func TestIPv6ResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "ipv6", "set_ipv6")

	tests := []struct {
		body, error string
	}{
		{`enabled = true`, `(?s)Invalid Attribute Combination.*lan_mode is required when enabled is true`},
		{"enabled = true\nlan_mode = \"static\"", `lan_ip is required when lan_mode is static`},
		{"enabled = false\nlan_mode = \"static\"", `lan_ip is required when lan_mode is static`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_ip = \"fd00:1::1/64\"", `lan_ip cannot be set when lan_mode is not static`},
		{"enabled = false\nlan_ip = \"fd00:1::1/64\"", `lan_ip cannot be set when lan_mode is not static`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns = [\"" + googleDNS1 + "\"]", `lan_dns cannot be set when lan_dns_auto is true`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = true\nlan_dns = [\"" + googleDNS1 + "\"]", `lan_dns cannot be set when lan_dns_auto is true`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false", `lan_dns is required when lan_dns_auto is false`},
		{"enabled = true\nlan_mode = \"relay\"\nlan_dns_auto = false\nlan_dns = [\"" + googleDNS1 + "\"]", `lan_dns_auto must be true when lan_mode is relay`},
		{"enabled = true\nlan_mode = \"dhcpv6\"", `lan_mode value must be one of`},
		{"enabled = true\nlan_mode = \"static\"\nlan_ip = \"fd00:1::1\"", `lan_ip must be an IPv6 address with a prefix length from 48 to 64`},
		{"enabled = true\nlan_mode = \"static\"\nlan_ip = \"fd00:1::1/47\"", `lan_ip must be an IPv6 address with a prefix length from 48 to 64`},
		{"enabled = true\nlan_mode = \"static\"\nlan_ip = \"fd00:1::1/65\"", `lan_ip must be an IPv6 address with a prefix length from 48 to 64`},
		{"enabled = true\nlan_mode = \"static\"\nlan_ip = \"192.168.8.1/24\"", `lan_ip must be an IPv6 address with a prefix length from 48 to 64`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false\nlan_dns = [\"8.8.8.8\"]", `lan_dns\[0\] must be an IPv6 address`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false\nlan_dns = [\"" + googleDNS1 + "\", \"dns\"]", `lan_dns\[1\] must be an IPv6 address`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false\nlan_dns = []", `lan_dns list must contain at least 1 elements`},
		{"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = false\nlan_dns = [\"" + googleDNS1 + "\", \"" + googleDNS2 + "\", \"2001:db8::53\"]", `lan_dns list must contain at least 1 elements and at most 2`},
		{`lan_mode = "nat6"`, `The argument "enabled" is required`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ipv6Config(tt.body),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := calls.take(); len(got) != 0 {
		t.Errorf("ipv6.set_ipv6 was called with %q", got)
	}
	expectJSONEqual(t, srv.IPv6.Config(), ipv6Initial)
}

// TestIPv6ResourceUnknownValues sets attributes from variables, which are unknown while Terraform validates the configuration.
func TestIPv6ResourceUnknownValues(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, variables, body, params string
	}{
		{
			"enabled",
			`variable "enabled" { default = true }`,
			"enabled = var.enabled\nlan_mode = \"nat6\"",
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`,
		},
		{
			"lan_mode",
			`variable "mode" { default = "relay" }`,
			"enabled = true\nlan_mode = var.mode",
			`{"enable":true,"lan_mode":"relay","lan_dns_mode":true}`,
		},
		{
			"null lan_ip",
			`variable "ip" { default = null }`,
			"enabled = true\nlan_mode = \"nat6\"\nlan_ip = var.ip",
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`,
		},
		{
			"lan_ip",
			`variable "ip" { default = "fd00:1::1/64" }`,
			"enabled = true\nlan_mode = \"static\"\nlan_ip = var.ip",
			`{"enable":true,"lan_mode":"static","lan_dns_mode":true,"lan_ip":"fd00:1::1/64"}`,
		},
		{
			"lan_dns_auto",
			`variable "auto" { default = false }`,
			"enabled = true\nlan_mode = \"nat6\"\nlan_dns_auto = var.auto\nlan_dns = [\"" + googleDNS1 + "\"]",
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":false,"lan_dns1":"` + googleDNS1 + `"}`,
		},
		{
			"null DNS servers",
			`variable "dns" { default = null }`,
			"enabled = true\nlan_mode = \"nat6\"\nlan_dns = var.dns",
			`{"enable":true,"lan_mode":"nat6","lan_dns_mode":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "ipv6", "set_ipv6")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: fakeRouterConfig(srv) + tt.variables + ipv6Config(tt.body),
						Check:  calls.expect(tt.params),
					},
				},
			})
		})
	}
}

func TestIPv6ResourceRouterErrors(t *testing.T) {
	parallel(t)
	config := ipv6Config(ipv6StaticEnabled)

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "ipv6", "get_ipv6")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read IPv6 Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + config,
					ConfigPlanChecks:  expectResourceAction(ipv6Address, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectIPv6State(map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "lan_mode": knownvalue.StringExact("static"), "lan_ip": knownvalue.NotNull(), "lan_dns_auto": knownvalue.Bool(false), "lan_dns": dnsServers(googleDNS1, googleDNS2)}),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("ipv6", "get_ipv6")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig:   failRouterMethod(srv, "ipv6", "get_ipv6"),
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read IPv6 Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("ipv6", "get_ipv6", get) },
					Config:           fakeRouterConfig(srv) + config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "ipv6", "set_ipv6")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Set IPv6 Configuration.*Method not found`),
				},
			},
		})

		expectJSONEqual(t, srv.IPv6.Config(), ipv6Initial)
	})
}

// TestIPv6ResourceRemoveSecondDNS removes the second DNS server from the configuration. The provider sends only lan_dns1,
// as the web UI does; the fake router keeps lan_dns2 (not known for the real router), and the state has one server without a plan.
func TestIPv6ResourceRemoveSecondDNS(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "ipv6", "set_ipv6")
	oneServer := ipv6Config(`
enabled      = true
lan_mode     = "static"
lan_ip       = "fd00:1::1/64"
lan_dns_auto = false
lan_dns      = ["` + googleDNS1 + `"]`)
	oneServerState := expectIPv6State(map[string]knownvalue.Check{
		"enabled":      knownvalue.Bool(true),
		"lan_mode":     knownvalue.StringExact("static"),
		"lan_ip":       knownvalue.StringExact("fd00:1::1/64"),
		"lan_dns_auto": knownvalue.Bool(false),
		"lan_dns":      dnsServers(googleDNS1),
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ipv6Config(ipv6StaticEnabled),
			},
			{
				PreConfig:         func() { calls.take() },
				Config:            fakeRouterConfig(srv) + oneServer,
				ConfigPlanChecks:  expectResourceAction(ipv6Address, plancheck.ResourceActionUpdate),
				ConfigStateChecks: oneServerState,
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:1::1/64","lan_dns1":"`+googleDNS1+`"}`),
					expectRouterJSON(t, srv.IPv6.Config, `{"enable":true,"lan_mode":"static","lan_dns_mode":false,"lan_ip":"fd00:1::1/64","lan_dns1":"`+googleDNS1+`","lan_dns2":"`+googleDNS2+`"}`),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + oneServer,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: oneServerState,
			},
		},
	})
}

func dnsServers(servers ...string) knownvalue.Check {
	var checks []knownvalue.Check
	for _, server := range servers {
		checks = append(checks, knownvalue.StringExact(server))
	}
	return knownvalue.ListExact(checks)
}
