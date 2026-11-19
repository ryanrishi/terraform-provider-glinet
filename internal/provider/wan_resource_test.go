package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const wanAddress = "glinet_wan.test"

func wanConfig(body string) string {
	return "\nresource \"glinet_wan\" \"test\" {\n" + body + "\n}\n"
}

const (
	wanDHCP = `protocol = "dhcp"`

	wanStatic = `
protocol = "static"
ipv4 = {
  ip      = "203.0.113.2"
  netmask = "255.255.255.0"
  gateway = "203.0.113.1"
  dns     = ["1.1.1.1"]
}`
	wanStaticRouter = `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]}}`

	wanStaticFull = `
protocol = "static"
ipv4 = {
  ip      = "203.0.113.3"
  netmask = "255.255.255.128"
  gateway = "203.0.113.4"
  dns     = ["1.1.1.1", "8.8.8.8"]
}
ipv6 = {
  ip      = "2001:db8::2/64"
  gateway = "2001:db8::1"
  dns     = ["2001:4860:4860::8888", "2001:4860:4860::8844"]
}
vlan_id  = 10
ttl      = 64
ttl_ipv6 = 65
mtu      = 1500`
	wanStaticFullRouter = `{"protocol":"static",
"ipv4":{"ip":"203.0.113.3","netmask":"255.255.255.128","gateway":"203.0.113.4","dns":["1.1.1.1","8.8.8.8"]},
"ipv6":{"ip":"2001:db8::2/64","gateway":"2001:db8::1","dns":["2001:4860:4860::8888","2001:4860:4860::8844"]},
"vlanid":10,"ttl":64,"ttl_ipv6":65,"mtu":1500}`

	wanPPPoE = `
protocol = "pppoe"
username = "user@isp"
password = "secret"
mtu      = 1492`
	wanPPPoERouter = `{"protocol":"pppoe","username":"user@isp","password":"secret","mtu":1492}`
)

// withSecondWAN returns the params of a cable.set_config of the first WAN port for the router configuration router.
func withSecondWAN(t *testing.T, router string) string {
	return withPort(t, router, false)
}

// withPort returns the params of a cable.set_config for the router configuration router.
func withPort(t *testing.T, router string, secondWAN bool) string {
	var p map[string]any
	if err := json.Unmarshal([]byte(router), &p); err != nil {
		t.Fatal(err)
	}
	p["secondwan"] = secondWAN
	b, _ := json.Marshal(p)
	return string(b)
}

// addSecondWANPort makes the fake router report a second wired WAN port in system.get_info.
func addSecondWANPort(t *testing.T, srv *glinettest.Server) {
	var info map[string]any
	if err := json.Unmarshal(srv.System.Info(), &info); err != nil {
		t.Fatal(err)
	}
	features, ok := info["software_feature"].(map[string]any)
	if !ok {
		t.Fatal("system.get_info has no software_feature")
	}
	features["secondwan"] = true
	srv.System.SetInfo(json.RawMessage(mustJSON(t, info)))
}

func expectWANState(checks map[string]knownvalue.Check) []statecheck.StateCheck {
	all := map[string]knownvalue.Check{
		"id":       knownvalue.StringExact("wan"),
		"port":     knownvalue.StringExact("wan"),
		"protocol": knownvalue.NotNull(),
		"ipv4":     knownvalue.Null(),
		"ipv6":     knownvalue.Null(),
		"username": knownvalue.Null(),
		"password": knownvalue.Null(),
		"vlan_id":  knownvalue.Null(),
		"ttl":      knownvalue.Null(),
		"ttl_ipv6": knownvalue.Null(),
		"mtu":      knownvalue.Null(),
	}
	for k, v := range checks {
		all[k] = v
	}
	return expectAttributes(wanAddress, all)
}

func TestWANResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_config")

	type step struct {
		body, router string
		state        map[string]knownvalue.Check
	}
	steps := []step{
		{wanDHCP, `{"protocol":"dhcp"}`, map[string]knownvalue.Check{"protocol": knownvalue.StringExact("dhcp")}},
		{wanStatic, wanStaticRouter, map[string]knownvalue.Check{
			"ipv4": knownvalue.ObjectExact(map[string]knownvalue.Check{
				"ip":      knownvalue.StringExact("203.0.113.2"),
				"netmask": knownvalue.StringExact("255.255.255.0"),
				"gateway": knownvalue.StringExact("203.0.113.1"),
				"dns":     knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("1.1.1.1")}),
			}),
		}},
		{wanStaticFull, wanStaticFullRouter, map[string]knownvalue.Check{
			"ipv4":     knownvalue.NotNull(),
			"ipv6":     knownvalue.NotNull(),
			"vlan_id":  knownvalue.Int64Exact(10),
			"ttl":      knownvalue.Int64Exact(64),
			"ttl_ipv6": knownvalue.Int64Exact(65),
			"mtu":      knownvalue.Int64Exact(1500),
		}},
		{wanStatic, wanStaticRouter, map[string]knownvalue.Check{"ipv4": knownvalue.NotNull()}},
		{wanPPPoE, wanPPPoERouter, map[string]knownvalue.Check{
			"username": knownvalue.StringExact("user@isp"),
			"password": knownvalue.StringExact("secret"),
			"mtu":      knownvalue.Int64Exact(1492),
		}},
		{`protocol = "pppoe"`, `{"protocol":"pppoe","username":"","password":""}`, nil},
		{wanDHCP + "\nvlan_id = 4094\nttl = 1\nttl_ipv6 = 255\nmtu = 9200", `{"protocol":"dhcp","vlanid":4094,"ttl":1,"ttl_ipv6":255,"mtu":9200}`,
			map[string]knownvalue.Check{
				"vlan_id":  knownvalue.Int64Exact(4094),
				"ttl":      knownvalue.Int64Exact(1),
				"ttl_ipv6": knownvalue.Int64Exact(255),
				"mtu":      knownvalue.Int64Exact(9200),
			}},
		{wanDHCP + "\nmtu = 576", `{"protocol":"dhcp","mtu":576}`, map[string]knownvalue.Check{"mtu": knownvalue.Int64Exact(576)}},
		{wanDHCP, `{"protocol":"dhcp"}`, nil},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		ts := resource.TestStep{
			Config:            fakeRouterConfig(srv) + wanConfig(s.body),
			ConfigStateChecks: expectWANState(s.state),
			Check: resource.ComposeTestCheckFunc(
				calls.expect(withSecondWAN(t, s.router)),
				expectRouterJSON(t, srv.Cable.Config, s.router),
			),
		}
		if i > 0 {
			ts.ConfigPlanChecks = expectResourceAction(wanAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, ts)
	}
	testSteps = append(testSteps, resource.TestStep{
		ResourceName:      wanAddress,
		ImportState:       true,
		ImportStateId:     "wan",
		ImportStateVerify: true,
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			expectRouterJSON(t, srv.Cable.Config, `{"protocol":"dhcp"}`),
		),
		Steps: testSteps,
	})
}

// TestWANResourceDestroy destroys a static WAN port, which leaves the router unchanged.
func TestWANResourceDestroy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			expectRouterJSON(t, srv.Cable.Config, wanStaticFullRouter),
		),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wanConfig(wanStaticFull),
				Check:  calls.expect(withSecondWAN(t, wanStaticFullRouter)),
			},
		},
	})
}

func TestWANResourceImport(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, body, router string
	}{
		{"dhcp", wanDHCP, `{"protocol":"dhcp"}`},
		{"static", wanStaticFull, wanStaticFullRouter},
		{"pppoe", wanPPPoE, wanPPPoERouter},
		{"pppoe without account", `protocol = "pppoe"`, `{"protocol":"pppoe","username":"","password":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Cable.SetConfig(json.RawMessage(tt.router))
			config := fakeRouterConfig(srv) + wanConfig(tt.body)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       wanAddress,
						ImportState:        true,
						ImportStateId:      "wan",
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

func TestWANResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + wanConfig(wanDHCP),
				ResourceName:  wanAddress,
				ImportState:   true,
				ImportStateId: "cable",
				ExpectError:   regexp.MustCompile(`(?s)Invalid Import ID.*must be "wan" or "secondwan"`),
			},
		},
	})
}

// TestWANResourceOtherProtocolValues reads a router that keeps the values of another protocol, which gives no plan.
func TestWANResourceOtherProtocolValues(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, body, router string
	}{
		{"dhcp", wanDHCP, `{"protocol":"dhcp","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]},"username":"u","password":"p"}`},
		{"static", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]},"username":"u","password":"p"}`},
		{"pppoe", wanPPPoE, `{"protocol":"pppoe","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]},"ipv6":{"ip":"2001:db8::2/64","gateway":"2001:db8::1","dns":["2001:db8::1"]},"username":"user@isp","password":"secret","mtu":1492}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			config := fakeRouterConfig(srv) + wanConfig(tt.body)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
					},
					{
						PreConfig:        func() { srv.Cable.SetConfig(json.RawMessage(tt.router)) },
						Config:           config,
						ConfigPlanChecks: expectEmptyPlan,
					},
				},
			})
		})
	}
}

func TestWANResourceDrift(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, body, router string
	}{
		{"protocol", wanStatic, `{"protocol":"dhcp"}`},
		{"ipv4 ip", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.9","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]}}`},
		{"ipv4 netmask", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.0.0","gateway":"203.0.113.1","dns":["1.1.1.1"]}}`},
		{"ipv4 gateway", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.9","dns":["1.1.1.1"]}}`},
		{"ipv4 dns", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1","9.9.9.9"]}}`},
		{"ipv4 removed", wanStatic, `{"protocol":"static"}`},
		{"ipv6 added", wanStatic, `{"protocol":"static","ipv4":{"ip":"203.0.113.2","netmask":"255.255.255.0","gateway":"203.0.113.1","dns":["1.1.1.1"]},"ipv6":{"ip":"2001:db8::2/64","gateway":"2001:db8::1","dns":["2001:db8::1"]}}`},
		{"ipv6 changed", wanStaticFull, `{"protocol":"static","ipv4":{"ip":"203.0.113.3","netmask":"255.255.255.128","gateway":"203.0.113.4","dns":["1.1.1.1","8.8.8.8"]},"ipv6":{"ip":"2001:db8::3/64","gateway":"2001:db8::1","dns":["2001:4860:4860::8888","2001:4860:4860::8844"]},"vlanid":10,"ttl":64,"ttl_ipv6":65,"mtu":1500}`},
		{"username", wanPPPoE, `{"protocol":"pppoe","username":"other","password":"secret","mtu":1492}`},
		{"password", wanPPPoE, `{"protocol":"pppoe","username":"user@isp","password":"other","mtu":1492}`},
		{"vlan_id", wanDHCP, `{"protocol":"dhcp","vlanid":5}`},
		{"ttl", wanDHCP, `{"protocol":"dhcp","ttl":5}`},
		{"ttl_ipv6", wanDHCP, `{"protocol":"dhcp","ttl_ipv6":5}`},
		{"mtu", wanPPPoE, `{"protocol":"pppoe","username":"user@isp","password":"secret"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			config := fakeRouterConfig(srv) + wanConfig(tt.body)
			applied := saveRouterJSON(srv.Cable.Config)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check:  applied.save,
					},
					{
						PreConfig:        func() { srv.Cable.SetConfig(json.RawMessage(tt.router)) },
						Config:           config,
						ConfigPlanChecks: expectResourceAction(wanAddress, plancheck.ResourceActionUpdate),
						Check:            applied.expect,
					},
				},
			})
		})
	}
}

func TestWANResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.Cable.Config()
	calls := recordCalls(t, srv, "cable", "set_config")

	const ipv4 = `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = ["1.1.1.1"] }`
	const ipv6 = `ipv6 = { ip = "2001:db8::2/64", gateway = "2001:db8::1", dns = ["2001:db8::1"] }`
	tests := []struct {
		body, error string
	}{
		{`protocol = "static"`, `(?s)Invalid Attribute Combination.*ipv4 is required when protocol is static`},
		{`protocol = "static"` + "\n" + ipv6, `ipv4 is required when protocol is static`},
		{`protocol = "dhcp"` + "\n" + ipv4, `ipv4 cannot be set when protocol is dhcp`},
		{`protocol = "pppoe"` + "\n" + ipv4, `ipv4 cannot be set when protocol is pppoe`},
		{`protocol = "dhcp"` + "\n" + ipv6, `ipv6 cannot be set when protocol is dhcp`},
		{`protocol = "pppoe"` + "\n" + ipv6, `ipv6 cannot be set when protocol is pppoe`},
		{`protocol = "dhcp"` + "\n" + `username = "u"`, `username cannot be set when protocol is dhcp`},
		{`protocol = "dhcp"` + "\n" + `password = "p"`, `password cannot be set when protocol is dhcp`},
		{`protocol = "static"` + "\n" + ipv4 + "\n" + `username = "u"`, `username cannot be set when protocol is static`},
		{`protocol = "static"` + "\n" + ipv4 + "\n" + `password = "p"`, `password cannot be set when protocol is static`},
		{`protocol = "l2tp"`, `protocol value must be one of`},
		{`protocol = "pppoe"` + "\n" + `username = ""`, `username string length must be between 1 and 256`},
		{`protocol = "pppoe"` + "\n" + `password = ""`, `password string length must be at least 1`},
		{`protocol = "dhcp"` + "\n" + `vlan_id = 0`, `vlan_id value must be between 1 and 4094`},
		{`protocol = "dhcp"` + "\n" + `vlan_id = 4095`, `vlan_id value must be between 1 and 4094`},
		{`protocol = "dhcp"` + "\n" + `ttl = 0`, `ttl value must be between 1 and 255`},
		{`protocol = "dhcp"` + "\n" + `ttl = 256`, `ttl value must be between 1 and 255`},
		{`protocol = "dhcp"` + "\n" + `ttl_ipv6 = 256`, `ttl_ipv6 value must be between 1 and 255`},
		{`protocol = "dhcp"` + "\n" + `mtu = 575`, `mtu value must be between 576 and 9200`},
		{`protocol = "dhcp"` + "\n" + `mtu = 9201`, `mtu value must be between 576 and 9200`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = ["1.1.1.1"] }`, `ipv4.ip must be an IPv4 address`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.0.255.0", gateway = "203.0.113.1", dns = ["1.1.1.1"] }`, `ipv4.netmask must be an IPv4 netmask`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "2001:db8::1", dns = ["1.1.1.1"] }`, `ipv4.gateway must be an IPv4 address`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = [] }`, `list must contain at least 1 elements`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = ["1.1.1.1", "1.0.0.1", "8.8.8.8"] }`, `at most 2\s+elements, got: 3`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = ["dns"] }`, `must be an IPv4 address`},
		{`protocol = "static"` + "\n" + `ipv4 = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1" }`, `attribute "dns" is required`},
		{`protocol = "static"` + "\n" + ipv4 + "\n" + `ipv6 = { ip = "2001:db8::2", gateway = "2001:db8::1", dns = ["2001:db8::1"] }`, `ipv6.ip must be an IPv6 address with a prefix length`},
		{`protocol = "static"` + "\n" + ipv4 + "\n" + `ipv6 = { ip = "2001:db8::2/64", gateway = "203.0.113.1", dns = ["2001:db8::1"] }`, `ipv6.gateway must be an IPv6 address`},
		{`protocol = "static"` + "\n" + ipv4 + "\n" + `ipv6 = { ip = "2001:db8::2/64", gateway = "2001:db8::1", dns = ["1.1.1.1"] }`, `must be an IPv6 address`},
		{`ipv4 = null`, `The argument "protocol" is required`},
		{`port = "wan2"` + "\n" + `protocol = "dhcp"`, `port value must be one of`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + wanConfig(tt.body),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := calls.take(); len(got) != 0 {
		t.Errorf("cable.set_config was called with %q", got)
	}
	if got := srv.Cable.Config(); string(got) != string(before) {
		t.Errorf("router WAN configuration is %s, want %s", got, before)
	}
}

// TestWANResourceUnknownValues sets attributes from variables, which are unknown while Terraform validates the configuration.
func TestWANResourceUnknownValues(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, variables, body, router string
	}{
		{
			"protocol",
			`variable "protocol" { default = "dhcp" }`,
			`protocol = var.protocol`,
			`{"protocol":"dhcp"}`,
		},
		{
			"null ipv4 with dhcp",
			`variable "ipv4" { default = null }`,
			`protocol = "dhcp"
ipv4 = var.ipv4`,
			`{"protocol":"dhcp"}`,
		},
		{
			"ipv4 with static",
			`variable "ipv4" { default = { ip = "203.0.113.2", netmask = "255.255.255.0", gateway = "203.0.113.1", dns = ["1.1.1.1"] } }`,
			`protocol = "static"
ipv4 = var.ipv4`,
			wanStaticRouter,
		},
		{
			"null account with dhcp",
			`variable "username" { default = null }
variable "password" { default = null }`,
			`protocol = "dhcp"
username = var.username
password = var.password`,
			`{"protocol":"dhcp"}`,
		},
		{
			"account with pppoe",
			`variable "username" { default = "user@isp" }
variable "password" {
  default   = "secret"
  sensitive = true
}`,
			`protocol = "pppoe"
username = var.username
password = var.password
mtu      = 1492`,
			wanPPPoERouter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: fakeRouterConfig(srv) + tt.variables + wanConfig(tt.body),
						Check:  expectRouterJSON(t, srv.Cable.Config, tt.router),
					},
				},
			})
		})
	}
}

func TestWANResourceRouterErrors(t *testing.T) {
	parallel(t)
	config := wanConfig(wanStatic)

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "cable", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read WAN Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + config,
					ConfigPlanChecks:  expectResourceAction(wanAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectWANState(map[string]knownvalue.Check{"ipv4": knownvalue.NotNull()}),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("cable", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig:   failRouterMethod(srv, "cable", "get_config"),
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read WAN Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("cable", "get_config", get) },
					Config:           fakeRouterConfig(srv) + config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.Cable.Config()
		failRouterMethod(srv, "cable", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Set WAN Configuration.*Method not found`),
				},
			},
		})

		if got := srv.Cable.Config(); string(got) != string(before) {
			t.Errorf("router WAN configuration is %s, want %s", got, before)
		}
	})
}

func TestWANResourceSecondPort(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	addSecondWANPort(t, srv)
	first := srv.Cable.Config()
	calls := recordCalls(t, srv, "cable", "set_config")
	secondPort := func(checks map[string]knownvalue.Check) []statecheck.StateCheck {
		checks["id"] = knownvalue.StringExact("secondwan")
		checks["port"] = knownvalue.StringExact("secondwan")
		return expectWANState(checks)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			expectRouterJSON(t, srv.Cable.Config, string(first)),
			expectRouterJSON(t, srv.Cable.SecondConfig, wanPPPoERouter),
		),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanStatic),
				ConfigStateChecks: secondPort(map[string]knownvalue.Check{"ipv4": knownvalue.NotNull()}),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(withPort(t, wanStaticRouter, true)),
					expectRouterJSON(t, srv.Cable.SecondConfig, wanStaticRouter),
					expectRouterJSON(t, srv.Cable.Config, string(first)),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanPPPoE),
				ConfigPlanChecks: expectResourceAction(wanAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: secondPort(map[string]knownvalue.Check{
					"username": knownvalue.StringExact("user@isp"),
					"password": knownvalue.StringExact("secret"),
					"mtu":      knownvalue.Int64Exact(1492),
				}),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(withPort(t, wanPPPoERouter, true)),
					expectRouterJSON(t, srv.Cable.SecondConfig, wanPPPoERouter),
					expectRouterJSON(t, srv.Cable.Config, string(first)),
				),
			},
			{
				ResourceName:      wanAddress,
				ImportState:       true,
				ImportStateId:     "secondwan",
				ImportStateVerify: true,
			},
		},
	})
}

// TestWANResourceBothPorts manages the two WAN ports with two resources.
func TestWANResourceBothPorts(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	addSecondWANPort(t, srv)
	calls := recordCalls(t, srv, "cable", "set_config")
	config := fakeRouterConfig(srv) + `
resource "glinet_wan" "first" {
  protocol = "dhcp"
  mtu      = 1500
}

resource "glinet_wan" "second" {
  port     = "secondwan"
  protocol = "pppoe"
  username = "user@isp"
  password = "secret"
  mtu      = 1492
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("glinet_wan.first", tfjsonpath.New("id"), knownvalue.StringExact("wan")),
					statecheck.ExpectKnownValue("glinet_wan.second", tfjsonpath.New("id"), knownvalue.StringExact("secondwan")),
				},
				Check: resource.ComposeTestCheckFunc(
					calls.expectUnordered(withPort(t, `{"protocol":"dhcp","mtu":1500}`, false), withPort(t, wanPPPoERouter, true)),
					expectRouterJSON(t, srv.Cable.Config, `{"protocol":"dhcp","mtu":1500}`),
					expectRouterJSON(t, srv.Cable.SecondConfig, wanPPPoERouter),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWANResourceChangePort(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	addSecondWANPort(t, srv)
	calls := recordCalls(t, srv, "cable", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wanConfig(wanStatic),
				Check:  calls.expect(withPort(t, wanStaticRouter, false)),
			},
			{
				Config:            fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanStatic),
				ConfigPlanChecks:  expectResourceAction(wanAddress, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: expectWANState(map[string]knownvalue.Check{"id": knownvalue.StringExact("secondwan"), "port": knownvalue.StringExact("secondwan"), "ipv4": knownvalue.NotNull()}),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(withPort(t, wanStaticRouter, true)),
					expectRouterJSON(t, srv.Cable.Config, wanStaticRouter),
				),
			},
		},
	})
}

// TestWANResourceNoSecondPort uses the second WAN port on a router without one, which gives an error and no cable call.
func TestWANResourceNoSecondPort(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	gets := recordCalls(t, srv, "cable", "get_config")
	sets := recordCalls(t, srv, "cable", "set_config")
	config := fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanDHCP)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)No Second WAN Port.*no second wired WAN port`),
			},
			{
				Config:        config,
				ResourceName:  wanAddress,
				ImportState:   true,
				ImportStateId: "secondwan",
				ExpectError:   regexp.MustCompile(`No Second WAN Port`),
			},
		},
	})

	if got := append(gets.take(), sets.take()...); len(got) != 0 {
		t.Errorf("cable was called with %q", got)
	}
}

// TestWANResourceSecondPortRemoved reads the second WAN port after the router stops reporting it.
func TestWANResourceSecondPortRemoved(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	info := srv.System.Info()
	addSecondWANPort(t, srv)
	config := fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanDHCP)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:   func() { srv.System.SetInfo(info) },
				Config:      config,
				ExpectError: regexp.MustCompile(`No Second WAN Port`),
			},
			{
				PreConfig:        func() { addSecondWANPort(t, srv) },
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWANResourceSecondPortSystemInfoError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failRouterMethod(srv, "system", "get_info")()
	calls := recordCalls(t, srv, "cable", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + wanConfig(`port = "secondwan"`+"\n"+wanDHCP),
				ExpectError: regexp.MustCompile(`(?s)Unable to Read System Information.*Method not found`),
			},
		},
	})

	if got := calls.take(); len(got) != 0 {
		t.Errorf("cable.set_config was called with %q", got)
	}
}
