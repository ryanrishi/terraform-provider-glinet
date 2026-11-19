package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const dnsAddress = "glinet_dns.test"

// dnsSettings is the configuration of a glinet_dns resource. An empty mode is not in the configuration.
type dnsSettings struct {
	mode                                    string
	forceDNS, rebindProtection, overrideVPN bool
}

func (s dnsSettings) config() string {
	var mode string
	if s.mode != "" {
		mode = fmt.Sprintf("  mode              = %q\n", s.mode)
	}
	return fmt.Sprintf(`
resource "glinet_dns" "test" {
%s  force_dns_enabled         = %t
  rebind_protection_enabled = %t
  override_vpn_enabled      = %t
}
`, mode, s.forceDNS, s.rebindProtection, s.overrideVPN)
}

// expectDNSState checks the state for the router configuration c in a mode other than secure.
func expectDNSState(c glinet.DNSConfig) []statecheck.StateCheck {
	expect := func(attribute string, value knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(dnsAddress, tfjsonpath.New(attribute), value)
	}
	servers := make([]knownvalue.Check, len(c.Server))
	for i, s := range c.Server {
		servers[i] = knownvalue.StringExact(s)
	}
	return []statecheck.StateCheck{
		expect("id", knownvalue.StringExact("dns")),
		expect("mode", knownvalue.StringExact(c.Mode)),
		expect("force_dns_enabled", knownvalue.Bool(c.ForceDNS)),
		expect("rebind_protection_enabled", knownvalue.Bool(c.RebindProtection)),
		expect("override_vpn_enabled", knownvalue.Bool(c.OverrideVPN)),
		expect("servers", knownvalue.ListExact(servers)),
		expect("protocol", knownvalue.Null()),
		expect("dot_provider", knownvalue.Null()),
		expect("dot_resolver_id", knownvalue.Null()),
	}
}

// expectRouterDNS checks that the DNS configuration of the router is want.
func expectRouterDNS(client *glinet.Client, want glinet.DNSConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.DNS.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(*got, want) {
			return fmt.Errorf("router DNS configuration is %+v, want %+v", *got, want)
		}
		return nil
	}
}

func setFakeDNS(srv *glinettest.Server, c glinet.DNSConfig) {
	srv.DNS.SetConfig(glinettest.DNSConfig(c))
}

// allowDNSRebindProtection returns a check for guardedRealRouter that allows the read of the DNS configuration, and
// writes that keep mode, force_dns and override_vpn of initial and have all four fields.
func allowDNSRebindProtection(initial glinet.DNSConfig) func(module, method string, params map[string]json.RawMessage) error {
	return func(module, method string, params map[string]json.RawMessage) error {
		if module == "dns" && method == "get_config" {
			return nil
		}
		mode, _ := json.Marshal(initial.Mode)
		if module == "dns" && method == "set_config" && len(params) == 4 &&
			string(params["mode"]) == string(mode) &&
			string(params["force_dns"]) == fmt.Sprint(initial.ForceDNS) &&
			string(params["override_vpn"]) == fmt.Sprint(initial.OverrideVPN) &&
			(string(params["rebind_protection"]) == "true" || string(params["rebind_protection"]) == "false") {
			return nil
		}
		return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
	}
}

// dnsTestRouter returns the provider block and a client for the router of the test, and the initial DNS configuration.
// The real router is used only through guardedRealRouter with allowDNSRebindProtection, and its DNS configuration
// goes back to its initial value when the test ends. The test fails when the server list is not the same then.
func dnsTestRouter(t *testing.T) (string, *glinet.Client, glinet.DNSConfig) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password), glinet.DNSConfig(srv.DNS.Config())
	}

	reader := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := reader.DNS.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != "auto" {
		t.Fatalf("the DNS mode of the router is %q; the test runs only in mode auto", initial.Mode)
	}
	provider, client := guardedRealRouter(t, allowDNSRebindProtection(*initial))

	t.Cleanup(func() {
		ctx := context.Background()
		err := client.DNS.SetConfig(ctx, glinet.SetDNSConfigRequest{
			Mode:             initial.Mode,
			ForceDNS:         initial.ForceDNS,
			RebindProtection: initial.RebindProtection,
			OverrideVPN:      initial.OverrideVPN,
		})
		if err != nil {
			t.Fatalf("restore DNS configuration: %v", err)
		}

		restored, err := client.DNS.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored DNS configuration: %v", err)
		}
		if !reflect.DeepEqual(*restored, *initial) {
			t.Errorf("restored DNS configuration is %+v, want %+v", *restored, *initial)
		}
	})

	return provider, client, *initial
}

// TestDNSResource changes only rebind_protection_enabled, because it also runs on the real router.
func TestDNSResource(t *testing.T) {
	parallel(t)
	provider, client, initial := dnsTestRouter(t)
	settings := dnsSettings{mode: initial.Mode, forceDNS: initial.ForceDNS, rebindProtection: !initial.RebindProtection, overrideVPN: initial.OverrideVPN}
	changed := initial
	changed.RebindProtection = !initial.RebindProtection
	restored := settings
	restored.rebindProtection = initial.RebindProtection

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDNS(client, initial),
		Steps: []resource.TestStep{
			{
				Config:            provider + settings.config(),
				ConfigStateChecks: expectDNSState(changed),
				Check:             expectRouterDNS(client, changed),
			},
			{
				Config:            provider + restored.config(),
				ConfigPlanChecks:  expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectDNSState(initial),
				Check:             expectRouterDNS(client, initial),
			},
			{
				ResourceName:      dnsAddress,
				ImportState:       true,
				ImportStateId:     "dns",
				ImportStateVerify: true,
			},
		},
	})
}

func TestDNSResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	initial := glinet.DNSConfig(srv.DNS.Config())
	router := func(s dnsSettings) glinet.DNSConfig {
		c := initial
		c.ForceDNS, c.RebindProtection, c.OverrideVPN = s.forceDNS, s.rebindProtection, s.overrideVPN
		return c
	}

	settings := []dnsSettings{
		{mode: "auto", forceDNS: false, rebindProtection: false, overrideVPN: false},
		{mode: "auto", forceDNS: true, rebindProtection: false, overrideVPN: false},
		{mode: "auto", forceDNS: true, rebindProtection: true, overrideVPN: false},
		{mode: "auto", forceDNS: true, rebindProtection: true, overrideVPN: true},
		{mode: "auto", forceDNS: false, rebindProtection: true, overrideVPN: true},
		{mode: "auto", forceDNS: false, rebindProtection: false, overrideVPN: true},
		{mode: "auto", forceDNS: false, rebindProtection: false, overrideVPN: false},
	}

	var steps []resource.TestStep
	for i, s := range settings {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + s.config(),
			ConfigStateChecks: expectDNSState(router(s)),
			Check:             expectRouterDNS(client, router(s)),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDNS(client, router(settings[len(settings)-1])),
		Steps:                    steps,
	})
}

// TestDNSResourceWrites checks that each write has the three flags, and the mode only when it is configured.
func TestDNSResourceWrites(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	var (
		mu     sync.Mutex
		writes []string
	)
	set := srv.Handler("dns", "set_config")
	srv.Handle("dns", "set_config", func(params json.RawMessage) (any, error) {
		mu.Lock()
		writes = append(writes, string(params))
		mu.Unlock()
		return set(params)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: fakeRouterConfig(srv) + dnsSettings{"", false, false, false}.config()},
			{Config: fakeRouterConfig(srv) + dnsSettings{"auto", false, true, true}.config()},
			{Config: fakeRouterConfig(srv) + dnsSettings{"", true, true, true}.config()},
		},
	})

	want := []string{
		`{"force_dns":false,"rebind_protection":false,"override_vpn":false}`,
		`{"mode":"auto","force_dns":false,"rebind_protection":true,"override_vpn":true}`,
		`{"force_dns":true,"rebind_protection":true,"override_vpn":true}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(writes, want) {
		t.Errorf("set_config writes are %q, want %q", writes, want)
	}
}

func TestDNSResourceMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	manual := glinet.DNSConfig(srv.DNS.Config())
	manual.Mode = "manual"
	setFakeDNS(srv, manual)
	auto := manual
	auto.Mode = "auto"
	noMode := dnsSettings{overrideVPN: manual.OverrideVPN}
	withMode := noMode
	withMode.mode = "auto"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + noMode.config(),
				ConfigStateChecks: expectDNSState(manual),
				Check:             expectRouterDNS(client, manual),
			},
			{
				Config:           fakeRouterConfig(srv) + noMode.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + withMode.config(),
				ConfigPlanChecks:  expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectDNSState(auto),
				Check:             expectRouterDNS(client, auto),
			},
			{
				Config:            fakeRouterConfig(srv) + noMode.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectDNSState(auto),
			},
			{
				PreConfig:         func() { setFakeDNS(srv, manual) },
				Config:            fakeRouterConfig(srv) + noMode.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectDNSState(manual),
			},
			{
				Config:            fakeRouterConfig(srv) + withMode.config(),
				ConfigPlanChecks:  expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectDNSState(auto),
				Check:             expectRouterDNS(client, auto),
			},
		},
	})
}

func TestDNSResourceImport(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	existing := glinet.DNSConfig(srv.DNS.Config())
	existing.ForceDNS = true
	setFakeDNS(srv, existing)
	config := fakeRouterConfig(srv) + dnsSettings{"auto", true, existing.RebindProtection, existing.OverrideVPN}.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       dnsAddress,
				ImportState:        true,
				ImportStateId:      "dns",
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectDNSState(existing),
			},
		},
	})

	if got := glinet.DNSConfig(srv.DNS.Config()); !reflect.DeepEqual(got, existing) {
		t.Errorf("router DNS configuration is %+v, want %+v", got, existing)
	}
}

func TestDNSResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + dnsSettings{"auto", false, false, true}.config(),
				ResourceName:  dnsAddress,
				ImportState:   true,
				ImportStateId: "auto",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestDNSResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	want := glinet.DNSConfig(srv.DNS.Config())
	config := fakeRouterConfig(srv) + dnsSettings{"auto", want.ForceDNS, want.RebindProtection, want.OverrideVPN}.config()
	with := func(change func(*glinet.DNSConfig)) glinet.DNSConfig {
		c := want
		c.Server = []string{"wwan 192.168.100.53"}
		change(&c)
		return c
	}
	withServers := func(servers ...string) glinet.DNSConfig {
		c := want
		c.Server = servers
		return c
	}

	steps := []resource.TestStep{{Config: config}}
	for _, router := range []glinet.DNSConfig{
		with(func(c *glinet.DNSConfig) { c.Mode = "secure" }),
		with(func(c *glinet.DNSConfig) { c.ForceDNS = !c.ForceDNS }),
		with(func(c *glinet.DNSConfig) { c.RebindProtection = !c.RebindProtection }),
		with(func(c *glinet.DNSConfig) { c.OverrideVPN = !c.OverrideVPN }),
	} {
		fixed := router
		if router.Mode != want.Mode {
			fixed.Server = want.Server
		}
		fixed.Mode = want.Mode
		fixed.ForceDNS, fixed.RebindProtection, fixed.OverrideVPN = want.ForceDNS, want.RebindProtection, want.OverrideVPN
		steps = append(steps, resource.TestStep{
			PreConfig:         func() { setFakeDNS(srv, router) },
			Config:            config,
			ConfigPlanChecks:  expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
			ConfigStateChecks: expectDNSState(fixed),
			Check:             expectRouterDNS(client, fixed),
		})
	}
	for _, router := range []glinet.DNSConfig{
		withServers("wwan 192.168.100.53", "wg 10.66.0.1"),
		withServers("wan 192.168.100.1"),
		withServers([]string{}...),
	} {
		steps = append(steps, resource.TestStep{
			PreConfig:         func() { setFakeDNS(srv, router) },
			Config:            config,
			ConfigPlanChecks:  expectEmptyPlan,
			ConfigStateChecks: expectDNSState(router),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func TestDNSResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.DNS.Config()

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"mode empty", `resource "glinet_dns" "test" {
  mode              = ""
  force_dns_enabled         = false
  rebind_protection_enabled = false
  override_vpn_enabled      = true
}`, `mode value must be one of: \["auto" "manual" "proxy" "secure"\]`},
		{"mode unknown", dnsSettings{"custom", false, false, true}.config(), `mode value must be one of`},
		{"no force_dns_enabled", `resource "glinet_dns" "test" {
  rebind_protection_enabled = false
  override_vpn_enabled      = true
}`, `The argument "force_dns_enabled" is required`},
		{"no rebind_protection_enabled", `resource "glinet_dns" "test" {
  force_dns_enabled    = false
  override_vpn_enabled = true
}`, `The argument "rebind_protection_enabled" is required`},
		{"no override_vpn_enabled", `resource "glinet_dns" "test" {
  force_dns_enabled         = false
  rebind_protection_enabled = false
}`, `The argument "override_vpn_enabled" is required`},
		{"servers without mode", dnsModeConfig(`servers = ["192.0.2.1"]`), wrapped("servers cannot be set when mode is not set.")},
		{"protocol without mode", dnsModeConfig(`protocol = "DoH"`), wrapped("protocol cannot be set when mode is not set.")},
		{"servers in auto", dnsModeConfig(`mode = "auto"`, `servers = ["wan 192.168.100.1"]`), wrapped("servers cannot be set when mode is auto.")},
		{"protocol in auto", dnsModeConfig(`mode = "auto"`, `protocol = "DoT"`), wrapped("protocol cannot be set when mode is auto.")},
		{"dot_provider in auto", dnsModeConfig(`mode = "auto"`, `dot_provider = "cloudflare"`), wrapped("dot_provider cannot be set when mode is auto.")},
		{"dot_resolver_id in auto", dnsModeConfig(`mode = "auto"`, `dot_resolver_id = "p0"`), wrapped("dot_resolver_id cannot be set when mode is auto.")},
		{"manual without servers", dnsModeConfig(`mode = "manual"`), wrapped("servers is required when mode is manual.")},
		{"manual empty servers", dnsModeConfig(`mode = "manual"`, `servers = []`), `servers list must contain at least 1 elements`},
		{"manual 5 servers", dnsModeConfig(`mode = "manual"`, `servers = ["192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5"]`),
			wrapped("servers must have 1 to 4 items when mode is manual, got: 5.")},
		{"manual no IPv4", dnsModeConfig(`mode = "manual"`, `servers = ["2001:db8::1"]`), wrapped("servers must have at least one IPv4 address when mode is manual.")},
		{"manual not an address", dnsModeConfig(`mode = "manual"`, `servers = ["192.0.2.1", "dns.example.com"]`),
			wrapped(`Each item of servers must be an IPv4 or IPv6 address when mode is manual, got: "dns.example.com".`)},
		{"manual with port", dnsModeConfig(`mode = "manual"`, `servers = ["192.0.2.1#53"]`), wrapped(`must be an IPv4 or IPv6 address when mode is manual`)},
		{"manual empty item", dnsModeConfig(`mode = "manual"`, `servers = ["192.0.2.1", ""]`), `string length must be at least 1`},
		{"manual protocol", dnsModeConfig(`mode = "manual"`, `servers = ["192.0.2.1"]`, `protocol = "DoH"`), wrapped("protocol cannot be set when mode is manual.")},
		{"proxy without servers", dnsModeConfig(`mode = "proxy"`), wrapped("servers is required when mode is proxy.")},
		{"proxy 2 servers", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1#53", "192.0.2.2#53"]`), wrapped("servers must have exactly 1 item when mode is proxy, got: 2.")},
		{"proxy no port", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1"]`), wrapped(`must be <ip>#<port> with a port from 1 to 65535 when mode is proxy`)},
		{"proxy port 0", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1#0"]`), wrapped(`must be <ip>#<port>`)},
		{"proxy port 65536", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1#65536"]`), wrapped(`must be <ip>#<port>`)},
		{"proxy port leading zero", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1#053"]`), wrapped(`must be <ip>#<port>`)},
		{"proxy host name", dnsModeConfig(`mode = "proxy"`, `servers = ["dns.example.com#53"]`), wrapped(`must be <ip>#<port>`)},
		{"proxy dot_provider", dnsModeConfig(`mode = "proxy"`, `servers = ["192.0.2.1#53"]`, `dot_provider = "nextdns"`),
			wrapped("dot_provider cannot be set when mode is proxy.")},
		{"secure without protocol", dnsModeConfig(`mode = "secure"`), wrapped("protocol is required when mode is secure.")},
		{"secure protocol unknown", dnsModeConfig(`mode = "secure"`, `protocol = "doh"`), `protocol value must be one of`},
		{"DoH without servers", dnsModeConfig(`mode = "secure"`, `protocol = "DoH"`), wrapped("servers is required when protocol is DoH.")},
		{"DNSCrypt dot_provider", dnsModeConfig(`mode = "secure"`, `protocol = "DNSCrypt"`, `servers = ["example-dnscrypt"]`, `dot_provider = "cloudflare"`),
			wrapped("dot_provider cannot be set when protocol is DNSCrypt.")},
		{"oDoH dot_resolver_id", dnsModeConfig(`mode = "secure"`, `protocol = "oDoH"`, `servers = ["example-odoh"]`, `dot_resolver_id = "p0"`),
			wrapped("dot_resolver_id cannot be set when protocol is oDoH.")},
		{"DoT servers", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "cloudflare"`, `servers = ["example-doh"]`),
			wrapped("servers cannot be set when protocol is DoT.")},
		{"DoT without dot_provider", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`), wrapped("dot_provider is required when protocol is DoT.")},
		{"dot_provider unknown", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "2"`), `dot_provider value must be one of`},
		{"cloudflare dot_resolver_id", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "cloudflare"`, `dot_resolver_id = "abc123"`),
			wrapped("dot_resolver_id cannot be set when dot_provider is cloudflare.")},
		{"controld without dot_resolver_id", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld"`),
			wrapped("dot_resolver_id is required when dot_provider is controld.")},
		{"controld_free without dot_resolver_id", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld_free"`),
			wrapped("dot_resolver_id is required when dot_provider is controld_free.")},
		{"nextdns empty dot_resolver_id", dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "nextdns"`, `dot_resolver_id = ""`),
			`string length must be at least 1`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + tt.config,
						ExpectError: regexp.MustCompile(tt.error),
					},
				},
			})
		})
	}

	if got := srv.DNS.Config(); !reflect.DeepEqual(got, before) {
		t.Errorf("router DNS configuration is %+v, want %+v", got, before)
	}
}

func TestDNSResourceRouterErrors(t *testing.T) {
	parallel(t)
	s := dnsSettings{"auto", false, true, true}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "dns", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read DNS Configuration.*Method not found`),
				},
				{
					Config:           fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks: expectResourceAction(dnsAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(dnsAddress, tfjsonpath.New("rebind_protection_enabled"), knownvalue.Bool(true)),
					},
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("dns", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + s.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "dns", "get_config"),
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read DNS Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("dns", "get_config", get) },
					Config:           fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.DNS.Config()
		failRouterMethod(srv, "dns", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set DNS Configuration.*Method not found`),
				},
			},
		})

		if got := srv.DNS.Config(); !reflect.DeepEqual(got, before) {
			t.Errorf("router DNS configuration is %+v, want %+v", got, before)
		}
	})
}

func TestAllowDNSRebindProtection(t *testing.T) {
	parallel(t)
	allow := allowDNSRebindProtection(glinet.DNSConfig{Mode: "auto", ForceDNS: false, OverrideVPN: true})

	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"set_config", `{"mode":"auto","force_dns":false,"rebind_protection":true,"override_vpn":true}`, true},
		{"set_config", `{"mode":"auto","force_dns":false,"rebind_protection":false,"override_vpn":true}`, true},
		{"set_config", `{"mode":"auto","force_dns":false,"rebind_protection":true,"override_vpn":false}`, false},
		{"set_config", `{"mode":"auto","force_dns":true,"rebind_protection":true,"override_vpn":true}`, false},
		{"set_config", `{"mode":"manual","force_dns":false,"rebind_protection":true,"override_vpn":true}`, false},
		{"set_config", `{"force_dns":false,"rebind_protection":true,"override_vpn":true}`, false},
		{"set_config", `{"mode":"auto","force_dns":false,"rebind_protection":0,"override_vpn":true}`, false},
		{"set_host", `{"content":""}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allow("dns", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("dns.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
}

// dnsModeConfig returns a glinet_dns configuration with force_dns false, rebind_protection false, override_vpn true
// and the attribute lines.
func dnsModeConfig(lines ...string) string {
	return fmt.Sprintf(`
resource "glinet_dns" "test" {
  force_dns_enabled         = false
  rebind_protection_enabled = false
  override_vpn_enabled      = true
  %s
}
`, strings.Join(lines, "\n  "))
}

// dnsWrite returns the set_config params of a dnsModeConfig configuration with the JSON fields of the mode.
func dnsWrite(fields string) string {
	return `{"force_dns":false,"rebind_protection":false,"override_vpn":true` + fields + `}`
}

func stringsExact(values []string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = knownvalue.StringExact(v)
	}
	return knownvalue.ListExact(checks)
}

// expectDNSModeState checks the state of a dnsModeConfig configuration. servers is not checked when it is nil.
// An empty protocol, provider or resolverID must be null.
func expectDNSModeState(mode string, servers []string, protocol, provider, resolverID string) []statecheck.StateCheck {
	stringOrNull := func(s string) knownvalue.Check {
		if s == "" {
			return knownvalue.Null()
		}
		return knownvalue.StringExact(s)
	}
	checks := map[string]knownvalue.Check{
		"id":                        knownvalue.StringExact("dns"),
		"mode":                      knownvalue.StringExact(mode),
		"force_dns_enabled":         knownvalue.Bool(false),
		"rebind_protection_enabled": knownvalue.Bool(false),
		"override_vpn_enabled":      knownvalue.Bool(true),
		"protocol":                  stringOrNull(protocol),
		"dot_provider":              stringOrNull(provider),
		"dot_resolver_id":           stringOrNull(resolverID),
	}
	if servers != nil {
		checks["servers"] = stringsExact(servers)
	}
	return expectAttributes(dnsAddress, checks)
}

// TestDNSResourceModes goes through each mode and each DoT provider, and imports after each step.
func TestDNSResourceModes(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "dns", "set_config")
	upstream := srv.DNS.Config().Server

	tests := []struct {
		config []string
		write  string
		state  []statecheck.StateCheck
	}{
		{
			[]string{`mode = "auto"`},
			`,"mode":"auto"`,
			expectDNSModeState("auto", upstream, "", "", ""),
		},
		{
			[]string{`mode = "manual"`, `servers = ["192.0.2.1", "2001:db8::1"]`},
			`,"mode":"manual","server":["192.0.2.1","2001:db8::1"]`,
			expectDNSModeState("manual", []string{"192.0.2.1", "2001:db8::1"}, "", "", ""),
		},
		{
			[]string{`mode = "manual"`, `servers = ["2001:db8::2", "198.51.100.1", "192.0.2.1", "192.0.2.2"]`},
			`,"mode":"manual","server":["2001:db8::2","198.51.100.1","192.0.2.1","192.0.2.2"]`,
			expectDNSModeState("manual", []string{"2001:db8::2", "198.51.100.1", "192.0.2.1", "192.0.2.2"}, "", "", ""),
		},
		{
			[]string{`mode = "proxy"`, `servers = ["192.0.2.10#5353"]`},
			`,"mode":"proxy","server":["192.0.2.10#5353"]`,
			expectDNSModeState("proxy", []string{"192.0.2.10#5353"}, "", "", ""),
		},
		{
			[]string{`mode = "proxy"`, `servers = ["2001:db8::53#65535"]`},
			`,"mode":"proxy","server":["2001:db8::53#65535"]`,
			expectDNSModeState("proxy", []string{"2001:db8::53#65535"}, "", "", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoH"`, `servers = ["example-doh", "example-doh-ipv6"]`},
			`,"mode":"secure","proto":"DoH","server":["example-doh","example-doh-ipv6"]`,
			expectDNSModeState("secure", []string{"example-doh", "example-doh-ipv6"}, "DoH", "", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DNSCrypt"`, `servers = ["example-dnscrypt"]`},
			`,"mode":"secure","proto":"DNSCrypt","server":["example-dnscrypt"]`,
			expectDNSModeState("secure", []string{"example-dnscrypt"}, "DNSCrypt", "", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "oDoH"`, `servers = ["example-odoh"]`},
			`,"mode":"secure","proto":"oDoH","server":["example-odoh"]`,
			expectDNSModeState("secure", []string{"example-odoh"}, "oDoH", "", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "cloudflare"`},
			`,"mode":"secure","proto":"DoT","dot_provider":"0"`,
			expectDNSModeState("secure", nil, "DoT", "cloudflare", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "nextdns"`},
			`,"mode":"secure","proto":"DoT","dot_provider":"1","nextdns_id":""`,
			expectDNSModeState("secure", nil, "DoT", "nextdns", ""),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "nextdns"`, `dot_resolver_id = "abc123"`},
			`,"mode":"secure","proto":"DoT","dot_provider":"1","nextdns_id":"abc123"`,
			expectDNSModeState("secure", nil, "DoT", "nextdns", "abc123"),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld"`, `dot_resolver_id = "a1b2c3d4"`},
			`,"mode":"secure","proto":"DoT","dot_provider":"2","controld_id":"a1b2c3d4"`,
			expectDNSModeState("secure", nil, "DoT", "controld", "a1b2c3d4"),
		},
		{
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld_free"`, `dot_resolver_id = "p0"`},
			`,"mode":"secure","proto":"DoT","dot_provider":"3","free_controld_id":"p0"`,
			expectDNSModeState("secure", nil, "DoT", "controld_free", "p0"),
		},
		{
			[]string{`mode = "manual"`, `servers = ["192.0.2.1"]`},
			`,"mode":"manual","server":["192.0.2.1"]`,
			expectDNSModeState("manual", []string{"192.0.2.1"}, "", "", ""),
		},
		{
			[]string{`mode = "auto"`},
			`,"mode":"auto"`,
			expectDNSModeState("auto", upstream, "", "", ""),
		},
	}

	var steps []resource.TestStep
	for _, tt := range tests {
		steps = append(steps,
			resource.TestStep{
				Config:            fakeRouterConfig(srv) + dnsModeConfig(tt.config...),
				ConfigStateChecks: tt.state,
				Check:             writes.expect(dnsWrite(tt.write)),
			},
			resource.TestStep{
				ResourceName:      dnsAddress,
				ImportState:       true,
				ImportStateId:     "dns",
				ImportStateVerify: true,
			},
		)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func TestDNSResourceServersDrift(t *testing.T) {
	parallel(t)

	tests := []struct {
		name   string
		config []string
		router []string
		write  string
	}{
		{"manual", []string{`mode = "manual"`, `servers = ["192.0.2.1", "192.0.2.2"]`}, []string{"192.0.2.1"},
			`,"mode":"manual","server":["192.0.2.1","192.0.2.2"]`},
		{"manual order", []string{`mode = "manual"`, `servers = ["192.0.2.1", "192.0.2.2"]`}, []string{"192.0.2.2", "192.0.2.1"},
			`,"mode":"manual","server":["192.0.2.1","192.0.2.2"]`},
		{"proxy", []string{`mode = "proxy"`, `servers = ["192.0.2.10#5353"]`}, []string{"192.0.2.10#53"},
			`,"mode":"proxy","server":["192.0.2.10#5353"]`},
		{"DoH", []string{`mode = "secure"`, `protocol = "DoH"`, `servers = ["example-doh"]`}, []string{"example-doh", "example-doh-ipv6"},
			`,"mode":"secure","proto":"DoH","server":["example-doh"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			writes := recordCalls(t, srv, "dns", "set_config")
			config := fakeRouterConfig(srv) + dnsModeConfig(tt.config...)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check:  writes.expect(dnsWrite(tt.write)),
					},
					{
						PreConfig: func() {
							c := srv.DNS.Config()
							c.Server = tt.router
							srv.DNS.SetConfig(c)
						},
						Config:           config,
						ConfigPlanChecks: expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
						Check:            writes.expect(dnsWrite(tt.write)),
					},
				},
			})
		})
	}
}

func TestDNSResourceSecureDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "dns", "set_config")
	config := fakeRouterConfig(srv) + dnsModeConfig(`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "nextdns"`)
	write := dnsWrite(`,"mode":"secure","proto":"DoT","dot_provider":"1","nextdns_id":""`)
	state := expectDNSModeState("secure", nil, "DoT", "nextdns", "")
	change := func(change func(*glinettest.DNSConfig)) func() {
		return func() {
			c := srv.DNS.Config()
			change(&c)
			srv.DNS.SetConfig(c)
		}
	}
	drift := func(router func(*glinettest.DNSConfig)) resource.TestStep {
		return resource.TestStep{
			PreConfig:         change(router),
			Config:            config,
			ConfigPlanChecks:  expectResourceAction(dnsAddress, plancheck.ResourceActionUpdate),
			ConfigStateChecks: state,
			Check:             writes.expect(write),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: state,
				Check:             writes.expect(write),
			},
			drift(func(c *glinettest.DNSConfig) { c.NextDNSID = "abc123" }),
			drift(func(c *glinettest.DNSConfig) { c.DoTProvider = "0" }),
			drift(func(c *glinettest.DNSConfig) { c.DoTProvider = "" }),
			drift(func(c *glinettest.DNSConfig) { c.DoTProvider = "7" }),
			drift(func(c *glinettest.DNSConfig) { c.Proto = "DoH" }),
			drift(func(c *glinettest.DNSConfig) { c.Proto = "" }),
			drift(func(c *glinettest.DNSConfig) { c.Mode, c.Proto, c.Server = "manual", "", []string{"192.0.2.1"} }),
			{
				PreConfig: change(func(c *glinettest.DNSConfig) {
					c.ControlDID, c.FreeControlDID, c.Server = "a1b2c3d4", "p0", []string{"203.0.113.53"}
				}),
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: append(state, statecheck.ExpectKnownValue(dnsAddress, tfjsonpath.New("servers"), stringsExact([]string{"203.0.113.53"}))),
				Check:             writes.expect(),
			},
		},
	})
}

// TestDNSResourceNoModeSecure checks that without mode, the secure attributes of the router are in the state and
// do not make a plan diff.
func TestDNSResourceNoModeSecure(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "dns", "set_config")
	router := srv.DNS.Config()
	router.Mode, router.Proto, router.DoTProvider, router.ControlDID, router.Server = "secure", "DoT", "2", "a1b2c3d4", []string{}
	srv.DNS.SetConfig(router)
	config := fakeRouterConfig(srv) + dnsModeConfig()
	state := expectDNSModeState("secure", []string{}, "DoT", "controld", "a1b2c3d4")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: state,
				Check:             writes.expect(dnsWrite("")),
			},
			{
				PreConfig: func() {
					c := srv.DNS.Config()
					c.ControlDID, c.Server = "e5f6a7b8", []string{"203.0.113.53"}
					srv.DNS.SetConfig(c)
				},
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectDNSModeState("secure", []string{"203.0.113.53"}, "DoT", "controld", "e5f6a7b8"),
				Check:             writes.expect(),
			},
			{
				ResourceName:      dnsAddress,
				ImportState:       true,
				ImportStateId:     "dns",
				ImportStateVerify: true,
			},
		},
	})
}

func TestDNSResourceImportModes(t *testing.T) {
	parallel(t)

	tests := []struct {
		name   string
		router glinettest.DNSConfig
		config []string
		state  []statecheck.StateCheck
	}{
		{
			"manual",
			glinettest.DNSConfig{Mode: "manual", OverrideVPN: true, Server: []string{"192.0.2.1", "2001:db8::1"}, DoTProvider: "0"},
			[]string{`mode = "manual"`, `servers = ["192.0.2.1", "2001:db8::1"]`},
			expectDNSModeState("manual", []string{"192.0.2.1", "2001:db8::1"}, "", "", ""),
		},
		{
			"proxy",
			glinettest.DNSConfig{Mode: "proxy", OverrideVPN: true, Server: []string{"192.0.2.10#5353"}, DoTProvider: "0"},
			[]string{`mode = "proxy"`, `servers = ["192.0.2.10#5353"]`},
			expectDNSModeState("proxy", []string{"192.0.2.10#5353"}, "", "", ""),
		},
		{
			"DoH",
			glinettest.DNSConfig{Mode: "secure", OverrideVPN: true, Proto: "DoH", Server: []string{"example-doh"}, DoTProvider: "0"},
			[]string{`mode = "secure"`, `protocol = "DoH"`, `servers = ["example-doh"]`},
			expectDNSModeState("secure", []string{"example-doh"}, "DoH", "", ""),
		},
		{
			"cloudflare",
			glinettest.DNSConfig{Mode: "secure", OverrideVPN: true, Proto: "DoT", Server: []string{}, DoTProvider: "0", NextDNSID: "abc123"},
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "cloudflare"`},
			expectDNSModeState("secure", []string{}, "DoT", "cloudflare", ""),
		},
		{
			"nextdns",
			glinettest.DNSConfig{Mode: "secure", OverrideVPN: true, Proto: "DoT", Server: []string{}, DoTProvider: "1", NextDNSID: "abc123", FreeControlDID: "p0"},
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "nextdns"`, `dot_resolver_id = "abc123"`},
			expectDNSModeState("secure", []string{}, "DoT", "nextdns", "abc123"),
		},
		{
			"controld",
			glinettest.DNSConfig{Mode: "secure", OverrideVPN: true, Proto: "DoT", Server: []string{}, DoTProvider: "2", ControlDID: "a1b2c3d4"},
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld"`, `dot_resolver_id = "a1b2c3d4"`},
			expectDNSModeState("secure", []string{}, "DoT", "controld", "a1b2c3d4"),
		},
		{
			"controld_free",
			glinettest.DNSConfig{Mode: "secure", OverrideVPN: true, Proto: "DoT", Server: []string{}, DoTProvider: "3", ControlDID: "a1b2c3d4", FreeControlDID: "p0"},
			[]string{`mode = "secure"`, `protocol = "DoT"`, `dot_provider = "controld_free"`, `dot_resolver_id = "p0"`},
			expectDNSModeState("secure", []string{}, "DoT", "controld_free", "p0"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.DNS.SetConfig(tt.router)
			writes := recordCalls(t, srv, "dns", "set_config")
			config := fakeRouterConfig(srv) + dnsModeConfig(tt.config...)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       dnsAddress,
						ImportState:        true,
						ImportStateId:      "dns",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: tt.state,
						Check:             writes.expect(),
					},
				},
			})
		})
	}
}
