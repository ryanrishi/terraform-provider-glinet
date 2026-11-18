package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	lanAddress   = "glinet_lan.test"
	guestAddress = "glinet_lan.guest"
)

// lanNetwork is the configuration of a glinet_lan resource. Fields with their zero value are not in the configuration.
// The router value of wan_isolation and dhcp_lpr_servers is that of the configuration, or false and none when they
// are not in the configuration.
type lanNetwork struct {
	label, iface, ip, netmask string
	isolation, dhcp           bool
	wanIsolation              *bool
	start, end, lease         string
	gateway                   string
	dns, lpr                  []string
}

func (c lanNetwork) config() string {
	label := c.label
	if label == "" {
		label = "test"
	}
	res := fmt.Sprintf(`
resource "glinet_lan" %q {
  name         = %q
  ip           = %q
  netmask      = %q
  ap_isolation = %t
  dhcp_enabled = %t
`, label, c.iface, c.ip, c.netmask, c.isolation, c.dhcp)
	for attr, v := range map[string]string{"dhcp_start": c.start, "dhcp_end": c.end, "dhcp_lease_time": c.lease, "dhcp_gateway": c.gateway} {
		if v != "" {
			res += fmt.Sprintf("  %s = %q\n", attr, v)
		}
	}
	if c.dns != nil {
		res += fmt.Sprintf("  dhcp_dns = [%s]\n", quoteList(c.dns))
	}
	if c.lpr != nil {
		res += fmt.Sprintf("  dhcp_lpr_servers = [%s]\n", quoteList(c.lpr))
	}
	if c.wanIsolation != nil {
		res += fmt.Sprintf("  wan_isolation = %t\n", *c.wanIsolation)
	}
	return res + "}\n"
}

func quoteList(values []string) string {
	var quoted []string
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return strings.Join(quoted, ", ")
}

func stringOrNullCheck(s string) knownvalue.Check {
	if s == "" {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(s)
}

func stringListOrNullCheck(values []string) knownvalue.Check {
	if values == nil {
		return knownvalue.Null()
	}
	return stringListExact(values...)
}

// routerWANIsolation returns the wan_isolation of the router for c.
func (c lanNetwork) routerWANIsolation() bool {
	return c.wanIsolation != nil && *c.wanIsolation
}

func (c lanNetwork) state(address string) []statecheck.StateCheck {
	var wanIsolation knownvalue.Check = knownvalue.Null()
	if c.iface == "guest" {
		wanIsolation = knownvalue.Bool(c.routerWANIsolation())
	}
	return expectAttributes(address, map[string]knownvalue.Check{
		"name":             knownvalue.StringExact(c.iface),
		"ip":               knownvalue.StringExact(c.ip),
		"netmask":          knownvalue.StringExact(c.netmask),
		"ap_isolation":     knownvalue.Bool(c.isolation),
		"wan_isolation":    wanIsolation,
		"dhcp_enabled":     knownvalue.Bool(c.dhcp),
		"dhcp_start":       stringOrNullCheck(c.start),
		"dhcp_end":         stringOrNullCheck(c.end),
		"dhcp_lease_time":  stringOrNullCheck(c.lease),
		"dhcp_gateway":     stringOrNullCheck(c.gateway),
		"dhcp_dns":         stringListOrNullCheck(c.dns),
		"dhcp_lpr_servers": stringListOrNullCheck(c.lpr),
	})
}

// addressParams returns the params of the address write of c. Only the guest network has wan_isolate.
func (c lanNetwork) addressParams() string {
	if c.iface == "guest" {
		return fmt.Sprintf(`{"interface":%q,"ip":%q,"netmask":%q,"ap_isolate":%d,"wan_isolate":%d}`,
			c.iface, c.ip, c.netmask, boolInt(c.isolation), boolInt(c.routerWANIsolation()))
	}
	return fmt.Sprintf(`{"interface":%q,"ip":%q,"netmask":%q,"ap_isolate":%d}`, c.iface, c.ip, c.netmask, boolInt(c.isolation))
}

// dhcpParams returns the params of the DHCP write of c, with the lease time minutes.
func (c lanNetwork) dhcpParams(minutes string) string {
	if !c.dhcp {
		return fmt.Sprintf(`{"interface":%q,"enable":0}`, c.iface)
	}
	dns, _ := json.Marshal(append([]string{}, c.dns...))
	lpr := ""
	if c.lpr != nil {
		list, _ := json.Marshal(c.lpr)
		lpr = fmt.Sprintf(`,"lpr":%s`, list)
	}
	return fmt.Sprintf(`{"interface":%q,"enable":1,"start":%q,"end":%q,"leasetime":%q,"gateway":%q,"dns":%s%s}`,
		c.iface, c.start, c.end, minutes, c.gateway, dns, lpr)
}

// lanItem returns the item of the network iface in the get_config_list result of the fake router.
func lanItem(t *testing.T, srv *glinettest.Server, iface string) map[string]any {
	t.Helper()
	var list struct {
		Interfaces []map[string]any `json:"interfaces"`
	}
	if err := json.Unmarshal(srv.LAN.ConfigList(), &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Interfaces {
		if item["interface"] == iface {
			return item
		}
	}
	t.Fatalf("fake router has no network %q", iface)
	return nil
}

// setLANItem sets fields in the item of the network iface of the fake router.
func setLANItem(t *testing.T, srv *glinettest.Server, iface string, fields map[string]any) {
	t.Helper()
	var list struct {
		Interfaces []map[string]any `json:"interfaces"`
	}
	if err := json.Unmarshal(srv.LAN.ConfigList(), &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Interfaces {
		if item["interface"] == iface {
			maps.Copy(item, fields)
		}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	srv.LAN.SetConfigList(raw)
}

// expectLANItem checks that the item of the network iface of the fake router has the fields want.
func expectLANItem(t *testing.T, srv *glinettest.Server, iface string, want map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		item := lanItem(t, srv, iface)
		for k, v := range want {
			got, _ := json.Marshal(item[k])
			w, _ := json.Marshal(v)
			if string(got) != string(w) {
				return fmt.Errorf("router %s %s is %s, want %s", iface, k, got, w)
			}
		}
		return nil
	}
}

var (
	guestDisabled = lanNetwork{iface: "guest", ip: "192.168.9.1", netmask: "255.255.255.0", isolation: true}
	guestDHCP     = lanNetwork{iface: "guest", ip: "192.168.9.1", netmask: "255.255.255.0", isolation: true,
		dhcp: true, start: "192.168.9.100", end: "192.168.9.200", lease: "2h"}
	lanDefault = lanNetwork{iface: "lan", ip: "192.168.8.1", netmask: "255.255.255.0",
		dhcp: true, start: "192.168.8.100", end: "192.168.8.249", lease: "12h"}
)

// TestLANResource takes over the guest network, which has the configured values, and changes each
// attribute. Each step checks which of the two writes are sent, with which params.
func TestLANResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "lan", "set_config")

	withDNS := guestDHCP
	withDNS.gateway, withDNS.dns = "192.168.9.2", []string{"1.1.1.1", "9.9.9.9"}
	sameDuration := withDNS
	sameDuration.lease = "120m"
	noIsolation := sameDuration
	noIsolation.isolation = false
	moved := noIsolation
	moved.ip, moved.start, moved.end, moved.gateway = "192.168.10.1", "192.168.10.10", "192.168.10.20", "192.168.10.2"
	wideMask := moved
	wideMask.netmask = "255.255.0.0"
	oneDNS := wideMask
	oneDNS.dns, oneDNS.gateway, oneDNS.lease = []string{"1.1.1.1"}, "", "30m"
	blocked := oneDNS
	blocked.wanIsolation = new(true)
	printers := blocked
	printers.lpr = []string{"192.168.10.5", "192.168.10.6"}
	unblocked := printers
	unblocked.wanIsolation, unblocked.lpr = new(false), []string{"192.168.10.6"}
	disabled := unblocked
	disabled.dhcp, disabled.start, disabled.end, disabled.lease, disabled.dns, disabled.lpr = false, "", "", "", nil, nil

	steps := []struct {
		c           lanNetwork
		calls       []string
		importAfter bool
	}{
		{guestDisabled, nil, false},
		{guestDHCP, []string{guestDHCP.dhcpParams("120m")}, false},
		{withDNS, []string{withDNS.dhcpParams("120m")}, false},
		{sameDuration, nil, true},
		{noIsolation, []string{noIsolation.addressParams()}, false},
		{moved, []string{moved.addressParams(), moved.dhcpParams("120m")}, false},
		{wideMask, []string{wideMask.addressParams()}, false},
		{oneDNS, []string{oneDNS.dhcpParams("30m")}, false},
		{blocked, []string{blocked.addressParams()}, false},
		{printers, []string{printers.dhcpParams("30m")}, true},
		{unblocked, []string{unblocked.addressParams(), unblocked.dhcpParams("30m")}, false},
		{disabled, []string{disabled.dhcpParams("")}, false},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		ts := resource.TestStep{
			Config:            fakeRouterConfig(srv) + s.c.config(),
			ConfigStateChecks: s.c.state(lanAddress),
			Check: resource.ComposeTestCheckFunc(
				calls.expect(s.calls...),
				expectLANItem(t, srv, "guest", map[string]any{
					"ip": s.c.ip, "netmask": s.c.netmask, "ap_isolate": boolInt(s.c.isolation), "enable": boolInt(s.c.dhcp),
				}),
			),
		}
		if i > 0 {
			ts.ConfigPlanChecks = expectResourceAction(lanAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, ts)
		if s.importAfter {
			testSteps = append(testSteps, resource.TestStep{
				ResourceName:                         lanAddress,
				ImportState:                          true,
				ImportStateId:                        "guest",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			})
		}
	}

	var final map[string]any
	testSteps[len(testSteps)-1].Check = resource.ComposeTestCheckFunc(testSteps[len(testSteps)-1].Check, func(*terraform.State) error {
		final = lanItem(t, srv, "guest")
		return nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			calls.expect(),
			func(s *terraform.State) error { return expectLANItem(t, srv, "guest", final)(s) },
		),
		Steps: testSteps,
	})

	got := lanItem(t, srv, "guest")
	if got["leasetime"] != "30m" {
		t.Errorf("router guest lease time is %v, want 30m: a disabled DHCP write must not send it", got["leasetime"])
	}
	if lpr := mustJSON(t, got["lpr"]); lpr != `["192.168.10.6"]` {
		t.Errorf("router guest lpr is %s, want [\"192.168.10.6\"]: a disabled DHCP write must not send it", lpr)
	}
}

// TestLANResourceLeaseTime compares lease times by duration.
func TestLANResourceLeaseTime(t *testing.T) {
	parallel(t)
	t.Run("12h configured, 720m returned", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		calls := recordCalls(t, srv, "lan", "set_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					PreConfig:         func() { setLANItem(t, srv, "lan", map[string]any{"leasetime": "6h"}) },
					Config:            fakeRouterConfig(srv) + lanDefault.config(),
					ConfigStateChecks: lanDefault.state(lanAddress),
					Check: resource.ComposeTestCheckFunc(
						calls.expect(lanDefault.dhcpParams("720m")),
						expectLANItem(t, srv, "lan", map[string]any{"leasetime": "720m"}),
					),
				},
				{
					Config:           fakeRouterConfig(srv) + lanDefault.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
				{
					PreConfig:         func() { setLANItem(t, srv, "lan", map[string]any{"leasetime": "43200s"}) },
					Config:            fakeRouterConfig(srv) + lanDefault.config(),
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: lanDefault.state(lanAddress),
				},
				{
					PreConfig:        func() { setLANItem(t, srv, "lan", map[string]any{"leasetime": "6h"}) },
					Config:           fakeRouterConfig(srv) + lanDefault.config(),
					ConfigPlanChecks: expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
					Check:            calls.expect(lanDefault.dhcpParams("720m")),
				},
			},
		})
	})

	t.Run("720m configured, 12h returned", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		set := srv.Handler("lan", "set_config")
		srv.Handle("lan", "set_config", func(params json.RawMessage) (any, error) {
			return set(json.RawMessage(strings.ReplaceAll(string(params), `"720m"`, `"12h"`)))
		})
		c := lanDefault
		c.lease = "720m"

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					PreConfig:         func() { setLANItem(t, srv, "lan", map[string]any{"leasetime": "6h"}) },
					Config:            fakeRouterConfig(srv) + c.config(),
					ConfigStateChecks: c.state(lanAddress),
					Check:             expectLANItem(t, srv, "lan", map[string]any{"leasetime": "12h"}),
				},
				{
					Config:           fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("imported 12h, configured 720m", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		calls := recordCalls(t, srv, "lan", "set_config")
		c := lanDefault
		c.lease = "720m"

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             fakeRouterConfig(srv) + c.config(),
					ResourceName:       lanAddress,
					ImportState:        true,
					ImportStateId:      "lan",
					ImportStatePersist: true,
				},
				{
					Config:            fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks:  expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
					ConfigStateChecks: c.state(lanAddress),
					Check:             calls.expect(),
				},
			},
		})
	})
}

// TestLANResourceCreate takes over a network and sends only the groups that differ from the router values.
func TestLANResourceCreate(t *testing.T) {
	parallel(t)
	sameDuration := lanDefault
	sameDuration.lease = "720m"
	isolated := lanDefault
	isolated.isolation = true
	guestBlocked := guestDisabled
	guestBlocked.wanIsolation = new(true)
	guestNotBlocked := guestDisabled
	guestNotBlocked.wanIsolation = new(false)
	lanPrinters := lanDefault
	lanPrinters.lpr = []string{"192.168.8.5"}

	tests := []struct {
		name  string
		c     lanNetwork
		calls []string
	}{
		{"lan equal", lanDefault, nil},
		{"lease time with the same duration", sameDuration, nil},
		{"guest equal", guestDisabled, nil},
		{"dhcp differs", guestDHCP, []string{guestDHCP.dhcpParams("120m")}},
		{"address differs", isolated, []string{isolated.addressParams()}},
		{"wan_isolation false, no wan_isolate on the router", guestNotBlocked, nil},
		{"wan_isolation differs", guestBlocked, []string{guestBlocked.addressParams()}},
		{"dhcp_lpr_servers differs", lanPrinters, []string{lanPrinters.dhcpParams("720m")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "lan", "set_config")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + tt.c.config(),
						ConfigStateChecks: tt.c.state(lanAddress),
						Check:             calls.expect(tt.calls...),
					},
				},
			})
		})
	}
}

// TestLANResourceBothNetworks applies the lan and the guest network in one apply.
func TestLANResourceBothNetworks(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "lan", "set_config")
	guest := guestDHCP
	guest.label = "guest"
	lan := lanDefault
	lan.lease = "1h"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + lan.config() + guest.config(),
				ConfigStateChecks: append(lan.state(lanAddress), guest.state(guestAddress)...),
				Check: resource.ComposeTestCheckFunc(
					calls.expectUnordered(lan.dhcpParams("60m"), guest.dhcpParams("120m")),
					expectLANItem(t, srv, "lan", map[string]any{"leasetime": "60m", "enable": 1}),
					expectLANItem(t, srv, "guest", map[string]any{"leasetime": "120m", "enable": 1, "start": "192.168.9.100", "end": "192.168.9.200"}),
				),
			},
		},
	})
}

// TestLANResourceLANAddressChange changes the address of the lan network. On a real router, the provider
// then loses its connection; the fake router stays at its URL, so the test proves only the calls.
func TestLANResourceLANAddressChange(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "lan", "set_config")
	moved := lanDefault
	moved.ip, moved.netmask, moved.start, moved.end = "10.0.0.1", "255.255.255.0", "10.0.0.100", "10.0.0.200"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + lanDefault.config(),
				Check:  calls.expect(),
			},
			{
				Config:            fakeRouterConfig(srv) + moved.config(),
				ConfigPlanChecks:  expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: moved.state(lanAddress),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(moved.addressParams(), moved.dhcpParams("720m")),
					expectLANItem(t, srv, "lan", map[string]any{"ip": "10.0.0.1", "start": "10.0.0.100", "end": "10.0.0.200"}),
				),
			},
		},
	})
}

// TestLANResourceNotConfigured sends back the wan_isolate and lpr values of the router when wan_isolation and
// dhcp_lpr_servers are not in the configuration, also after the router values change.
func TestLANResourceNotConfigured(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setLANItem(t, srv, "guest", map[string]any{"wan_isolate": 1, "lpr": []string{"192.168.9.5"}})
	calls := recordCalls(t, srv, "lan", "set_config")
	wideMask := guestDHCP
	wideMask.netmask = "255.255.0.0"
	longLease := wideMask
	longLease.lease = "3h"
	disabled := guestDisabled
	disabled.netmask = wideMask.netmask
	twoServers := []string{"192.168.9.6", "192.168.9.7"}
	routerValues := func(c lanNetwork, lpr []string) []statecheck.StateCheck {
		c.wanIsolation, c.lpr = new(true), lpr
		return c.state(lanAddress)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + guestDHCP.config(),
				ConfigStateChecks: routerValues(guestDHCP, []string{"192.168.9.5"}),
				Check: calls.expect(
					`{"interface":"guest","enable":1,"start":"192.168.9.100","end":"192.168.9.200","leasetime":"120m","gateway":"","dns":[],"lpr":["192.168.9.5"]}`),
			},
			{
				PreConfig:         func() { setLANItem(t, srv, "guest", map[string]any{"lpr": twoServers}) },
				Config:            fakeRouterConfig(srv) + guestDHCP.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: routerValues(guestDHCP, twoServers),
			},
			{
				Config:           fakeRouterConfig(srv) + wideMask.config(),
				ConfigPlanChecks: expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
				Check:            calls.expect(`{"interface":"guest","ip":"192.168.9.1","netmask":"255.255.0.0","ap_isolate":1,"wan_isolate":1}`),
			},
			{
				Config:           fakeRouterConfig(srv) + longLease.config(),
				ConfigPlanChecks: expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
				Check: calls.expect(
					`{"interface":"guest","enable":1,"start":"192.168.9.100","end":"192.168.9.200","leasetime":"180m","gateway":"","dns":[],"lpr":["192.168.9.6","192.168.9.7"]}`),
			},
			{
				Config:            fakeRouterConfig(srv) + disabled.config(),
				ConfigStateChecks: routerValues(disabled, nil),
				Check:             calls.expect(`{"interface":"guest","enable":0}`),
			},
			{
				Config:            fakeRouterConfig(srv) + longLease.config(),
				ConfigStateChecks: routerValues(longLease, twoServers),
				Check: calls.expect(
					`{"interface":"guest","enable":1,"start":"192.168.9.100","end":"192.168.9.200","leasetime":"180m","gateway":"","dns":[],"lpr":["192.168.9.6","192.168.9.7"]}`),
			},
		},
	})
}

func TestLANResourceImport(t *testing.T) {
	parallel(t)
	for _, c := range []lanNetwork{lanDefault, guestDisabled} {
		t.Run(c.iface, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "lan", "set_config")
			config := fakeRouterConfig(srv) + c.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       lanAddress,
						ImportState:        true,
						ImportStateId:      c.iface,
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: c.state(lanAddress),
						Check:             calls.expect(),
					},
				},
			})
		})
	}
}

func TestLANResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, id := range []string{"wan", "LAN", "guest5g"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + lanDefault.config(),
					ResourceName:  lanAddress,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid Import ID`),
				},
			},
		})
	}
}

// withoutGuest makes the fake router have only the lan network.
func withoutGuest(t *testing.T, srv *glinettest.Server) {
	srv.LAN.SetConfigList(json.RawMessage(`{"interfaces":[` + mustJSON(t, lanItem(t, srv, "lan")) + `]}`))
}

func mustJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLANResourceUnknownInterface(t *testing.T) {
	parallel(t)
	t.Run("import", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		withoutGuest(t, srv)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + guestDisabled.config(),
					ResourceName:  lanAddress,
					ImportState:   true,
					ImportStateId: "guest",
					ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
				},
			},
		})
	})

	t.Run("create", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		withoutGuest(t, srv)
		calls := recordCalls(t, srv, "lan", "set_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + guestDisabled.config(),
					ExpectError: regexp.MustCompile(`(?s)LAN Interface Not Found.*The router has no network "guest"`),
				},
			},
		})
		if got := calls.take(); len(got) != 0 {
			t.Errorf("lan.set_config was called with %q", got)
		}
	})

	t.Run("removed on the router", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + guestDisabled.config(),
				},
				{
					PreConfig:          func() { withoutGuest(t, srv) },
					RefreshState:       true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})
}

func TestLANResourceDrift(t *testing.T) {
	parallel(t)
	withDNS := guestDHCP
	withDNS.gateway, withDNS.dns = "192.168.9.2", []string{"1.1.1.1"}
	blocked := guestDHCP
	blocked.wanIsolation = new(true)
	notBlocked := guestDHCP
	notBlocked.wanIsolation = new(false)
	printers := guestDHCP
	printers.lpr = []string{"192.168.9.5", "192.168.9.6"}

	tests := []struct {
		name   string
		c      lanNetwork
		fields map[string]any
		calls  func(c lanNetwork) []string
	}{
		{"ip", guestDHCP, map[string]any{"ip": "192.168.9.2"}, addressCall},
		{"netmask", guestDHCP, map[string]any{"netmask": "255.255.0.0"}, addressCall},
		{"ap_isolation", guestDHCP, map[string]any{"ap_isolate": 0}, addressCall},
		{"dhcp disabled", guestDHCP, map[string]any{"enable": 0}, dhcpCall},
		{"dhcp enabled", guestDisabled, map[string]any{"enable": 1}, dhcpCall},
		{"dhcp_start", guestDHCP, map[string]any{"start": "192.168.9.50"}, dhcpCall},
		{"dhcp_end", guestDHCP, map[string]any{"end": "192.168.9.250"}, dhcpCall},
		{"dhcp_lease_time", guestDHCP, map[string]any{"leasetime": "3h"}, dhcpCall},
		{"dhcp_gateway", withDNS, map[string]any{"gateway": ""}, dhcpCall},
		{"dhcp_dns", withDNS, map[string]any{"dns": []string{"1.1.1.1", "8.8.8.8"}}, dhcpCall},
		{"dhcp_dns removed", withDNS, map[string]any{"dns": []string{}}, dhcpCall},
		{"wan_isolation false", blocked, map[string]any{"wan_isolate": 0}, addressCall},
		{"wan_isolation true", notBlocked, map[string]any{"wan_isolate": 1}, addressCall},
		{"dhcp_lpr_servers", printers, map[string]any{"lpr": []string{"192.168.9.6", "192.168.9.5"}}, dhcpCall},
		{"dhcp_lpr_servers removed", printers, map[string]any{"lpr": []string{}}, dhcpCall},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "lan", "set_config")
			config := fakeRouterConfig(srv) + tt.c.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
					},
					{
						PreConfig: func() {
							calls.take()
							setLANItem(t, srv, "guest", tt.fields)
						},
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: tt.c.state(lanAddress),
						Check:             calls.expect(tt.calls(tt.c)...),
					},
				},
			})
		})
	}

	t.Run("dhcp values while disabled", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		config := fakeRouterConfig(srv) + guestDisabled.config()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					PreConfig: func() {
						setLANItem(t, srv, "guest", map[string]any{"start": "192.168.9.2", "leasetime": "1h", "dns": []string{"1.1.1.1"}})
					},
					Config:           config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})
}

func addressCall(c lanNetwork) []string {
	return []string{c.addressParams()}
}

func dhcpCall(c lanNetwork) []string {
	minutes, _ := leaseMinutes(c.lease)
	return []string{c.dhcpParams(fmt.Sprintf("%dm", minutes))}
}

// TestLANResourceDestroy destroys the resource, which leaves the router unchanged.
func TestLANResourceDestroy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "lan", "set_config")
	applied := saveRouterJSON(srv.LAN.ConfigList)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(calls.expect(), applied.expect),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + guestDHCP.config(),
				Check:  resource.ComposeTestCheckFunc(calls.expect(guestDHCP.dhcpParams("120m")), applied.save),
			},
		},
	})
}

func TestLANResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.LAN.ConfigList()
	calls := recordCalls(t, srv, "lan", "set_config")

	with := func(change func(*lanNetwork)) string {
		c := guestDHCP
		change(&c)
		return c.config()
	}
	disabledWith := func(attr, value string) string {
		return strings.Replace(guestDisabled.config(), "}\n", attr+" = "+value+"\n}\n", 1)
	}

	tests := []struct {
		config, error string
	}{
		{with(func(c *lanNetwork) { c.iface = "wan" }), `name value must be one of`},
		{with(func(c *lanNetwork) { c.ip = "203.0.113.1" }), `ip must be a private IPv4 address`},
		{with(func(c *lanNetwork) { c.ip = "192.168.9" }), `ip must be a private IPv4 address`},
		{with(func(c *lanNetwork) { c.ip = "fd00::1" }), `ip must be a private IPv4 address`},
		{with(func(c *lanNetwork) { c.ip = "192.168.9.0" }), `ip cannot be the network or the broadcast address of 192.168.9.0/24`},
		{with(func(c *lanNetwork) { c.ip = "192.168.9.255" }), `ip cannot be the network or the broadcast address`},
		{with(func(c *lanNetwork) { c.netmask = "255.0.255.0" }), `netmask must be an IPv4 netmask`},
		{with(func(c *lanNetwork) { c.netmask = "255.255.255.254" }), `prefix length of netmask must be 16 to 30 for the address 192.168.9.1,\s+got 31`},
		{with(func(c *lanNetwork) { c.netmask = "255.254.0.0" }), `prefix length of netmask must be 16 to 30`},
		{with(func(c *lanNetwork) {
			c.ip, c.netmask, c.start, c.end = "172.16.0.1", "255.224.0.0", "172.16.0.10", "172.16.0.20"
		}), `prefix length of netmask must be 12 to 30`},
		{with(func(c *lanNetwork) {
			c.ip, c.netmask, c.start, c.end = "10.0.0.1", "254.0.0.0", "10.0.0.10", "10.0.0.20"
		}), `prefix length of netmask must be 8 to 30`},
		{with(func(c *lanNetwork) { c.start = "192.168.8.100" }), `dhcp_start must be in the network 192.168.9.0/24`},
		{with(func(c *lanNetwork) { c.end = "192.168.10.200" }), `dhcp_end must be in the network 192.168.9.0/24`},
		{with(func(c *lanNetwork) { c.start, c.end = "192.168.9.200", "192.168.9.100" }), `dhcp_start must be less than dhcp_end`},
		{with(func(c *lanNetwork) { c.start, c.end = "192.168.9.100", "192.168.9.100" }), `dhcp_start must be less than dhcp_end`},
		{with(func(c *lanNetwork) { c.start = "8.8.8.8" }), `dhcp_start must be a private IPv4 address`},
		{with(func(c *lanNetwork) { c.start = "" }), `dhcp_start is required when dhcp_enabled is true`},
		{with(func(c *lanNetwork) { c.end = "" }), `dhcp_end is required when dhcp_enabled is true`},
		{with(func(c *lanNetwork) { c.lease = "" }), `dhcp_lease_time is required when dhcp_enabled is true`},
		{with(func(c *lanNetwork) { c.lease = "1m" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.lease = "71582789m" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.lease = "1193047h" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.lease = "12" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.lease = "43200s" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.lease = "1d" }), `dhcp_lease_time must be a number of hours or minutes`},
		{with(func(c *lanNetwork) { c.gateway = "gateway" }), `dhcp_gateway must be an IPv4 address`},
		{with(func(c *lanNetwork) { c.dns = []string{} }), `dhcp_dns list must contain at least 1 elements`},
		{with(func(c *lanNetwork) { c.dns = []string{"1.1.1.1", "1.0.0.1", "8.8.8.8"} }), `at most 2\s+elements`},
		{with(func(c *lanNetwork) { c.dns = []string{"2001:db8::1"} }), `must be an IPv4 address`},
		{disabledWith("dhcp_start", `"192.168.9.100"`), `dhcp_start cannot be set when dhcp_enabled is false`},
		{disabledWith("dhcp_end", `"192.168.9.200"`), `dhcp_end cannot be set when dhcp_enabled is false`},
		{disabledWith("dhcp_lease_time", `"12h"`), `dhcp_lease_time cannot be set when dhcp_enabled is false`},
		{disabledWith("dhcp_gateway", `"192.168.9.2"`), `dhcp_gateway cannot be set when dhcp_enabled is false`},
		{disabledWith("dhcp_dns", `["1.1.1.1"]`), `dhcp_dns cannot be set when dhcp_enabled is false`},
		{disabledWith("dhcp_lpr_servers", `["192.168.9.5"]`), `dhcp_lpr_servers cannot be set when dhcp_enabled is false`},
		{with(func(c *lanNetwork) { c.lpr = []string{} }), `dhcp_lpr_servers list must contain at least 1 elements`},
		{with(func(c *lanNetwork) {
			c.lpr = []string{"192.168.9.5", "192.168.9.6", "192.168.9.7", "192.168.9.8", "192.168.9.9", "192.168.9.10"}
		}), `at most\s+5\s+elements, got: 6`},
		{with(func(c *lanNetwork) { c.lpr = []string{"192.168.9.5", "192.168.9.5"} }), `(?s)Duplicate List Value.*dhcp_lpr_servers.*duplicate values of: "192.168.9.5"`},
		{with(func(c *lanNetwork) { c.lpr = []string{"printer.example.com"} }), `must be an IPv4 address`},
		{with(func(c *lanNetwork) { c.lpr = []string{"2001:db8::5"} }), `must be an IPv4 address`},
		{with(func(c *lanNetwork) {
			c.iface, c.ip, c.start, c.end, c.wanIsolation = "lan", "192.168.8.1", "192.168.8.100", "192.168.8.200", new(false)
		}), `wan_isolation cannot be set for the lan network`},
		{strings.Replace(guestDisabled.config(), "  ap_isolation = true\n", "", 1), `The argument "ap_isolation" is required`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.config,
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := calls.take(); len(got) != 0 {
		t.Errorf("lan.set_config was called with %q", got)
	}
	expectJSONEqual(t, srv.LAN.ConfigList(), string(before))
}

// TestLANResourceUnknownValues sets attributes from variables, which are unknown while Terraform validates the configuration.
func TestLANResourceUnknownValues(t *testing.T) {
	parallel(t)
	const resourceBlock = `
resource "glinet_lan" "test" {
  name            = "guest"
  ip              = var.ip
  netmask         = var.netmask
  ap_isolation    = true
  dhcp_enabled    = var.dhcp
  dhcp_start      = var.start
  dhcp_end        = var.end
  dhcp_lease_time = var.lease
  dhcp_gateway    = var.gateway
  dhcp_dns        = var.dns
}
`
	tests := []struct {
		name, variables string
		c               lanNetwork
	}{
		{
			"enabled",
			`
variable "ip" { default = "192.168.9.1" }
variable "netmask" { default = "255.255.255.0" }
variable "dhcp" { default = true }
variable "start" { default = "192.168.9.100" }
variable "end" { default = "192.168.9.200" }
variable "lease" { default = "2h" }
variable "gateway" { default = "192.168.9.2" }
variable "dns" { default = ["1.1.1.1"] }
`,
			lanNetwork{iface: "guest", ip: "192.168.9.1", netmask: "255.255.255.0", isolation: true, dhcp: true,
				start: "192.168.9.100", end: "192.168.9.200", lease: "2h", gateway: "192.168.9.2", dns: []string{"1.1.1.1"}},
		},
		{
			"disabled",
			`
variable "ip" { default = "192.168.9.1" }
variable "netmask" { default = "255.255.255.0" }
variable "dhcp" { default = false }
variable "start" { default = null }
variable "end" { default = null }
variable "lease" { default = null }
variable "gateway" { default = null }
variable "dns" { default = null }
`,
			guestDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + tt.variables + resourceBlock,
						ConfigStateChecks: tt.c.state(lanAddress),
					},
				},
			})
		})
	}

	t.Run("dhcp disabled with null DHCP values", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + `
variable "start" { default = null }
` + strings.Replace(guestDisabled.config(), "}\n", "dhcp_start = var.start\n}\n", 1),
					ConfigStateChecks: guestDisabled.state(lanAddress),
				},
			},
		})
	})
}

func TestLANResourceRouterErrors(t *testing.T) {
	parallel(t)
	config := guestDHCP.config()

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("lan", "get_config_list")
		var n atomic.Int32
		srv.Handle("lan", "get_config_list", func(params json.RawMessage) (any, error) {
			if n.Add(1) == 2 {
				return nil, glinettest.ErrMethodNotFound
			}
			return get(params)
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LAN Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + config,
					ConfigPlanChecks:  expectResourceAction(lanAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: guestDHCP.state(lanAddress),
				},
			},
		})
	})

	t.Run("get before set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		calls := recordCalls(t, srv, "lan", "set_config")
		failRouterMethod(srv, "lan", "get_config_list")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LAN Configuration.*Method not found`),
				},
			},
		})
		if got := calls.take(); len(got) != 0 {
			t.Errorf("lan.set_config was called with %q", got)
		}
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("lan", "get_config_list")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig:   failRouterMethod(srv, "lan", "get_config_list"),
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LAN Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("lan", "get_config_list", get) },
					Config:           fakeRouterConfig(srv) + config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set address", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.LAN.ConfigList()
		failRouterMethod(srv, "lan", "set_config")()
		wideMask := guestDHCP
		wideMask.netmask = "255.255.0.0"

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + wideMask.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set LAN Address.*Method not found`),
				},
			},
		})
		expectJSONEqual(t, srv.LAN.ConfigList(), string(before))
	})

	t.Run("set DHCP after address", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		set := srv.Handler("lan", "set_config")
		moved := guestDHCP
		moved.netmask, moved.lease = "255.255.0.0", "3h"
		addressOnly := guestDHCP
		addressOnly.netmask = moved.netmask

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig: func() {
						srv.Handle("lan", "set_config", func(params json.RawMessage) (any, error) {
							if strings.Contains(string(params), `"enable"`) {
								return nil, glinettest.ErrMethodNotFound
							}
							return set(params)
						})
					},
					Config:      fakeRouterConfig(srv) + moved.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set LAN DHCP Server.*Method not found`),
				},
				{
					PreConfig:         func() { srv.Handle("lan", "set_config", set) },
					Config:            fakeRouterConfig(srv) + addressOnly.config(),
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: addressOnly.state(lanAddress),
				},
				{
					Config:            fakeRouterConfig(srv) + moved.config(),
					ConfigPlanChecks:  expectResourceAction(lanAddress, plancheck.ResourceActionUpdate),
					ConfigStateChecks: moved.state(lanAddress),
				},
			},
		})
	})
}
