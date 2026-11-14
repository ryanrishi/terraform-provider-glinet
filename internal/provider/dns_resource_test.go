package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
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
%s  force_dns         = %t
  rebind_protection = %t
  override_vpn      = %t
}
`, mode, s.forceDNS, s.rebindProtection, s.overrideVPN)
}

// expectDNSState checks the state for the router configuration c.
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
		expect("force_dns", knownvalue.Bool(c.ForceDNS)),
		expect("rebind_protection", knownvalue.Bool(c.RebindProtection)),
		expect("override_vpn", knownvalue.Bool(c.OverrideVPN)),
		expect("servers", knownvalue.ListExact(servers)),
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

// TestDNSResource changes only rebind_protection, because it also runs on the real router.
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
		c.Server = []string{"wwan 192.168.4.253"}
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
		withServers("wwan 192.168.4.253", "wg 192.168.5.1"),
		withServers("wan 192.168.1.1"),
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
		{"mode manual", dnsSettings{"manual", false, false, true}.config(), `mode value must be one of: \["auto"\]`},
		{"mode secure", dnsSettings{"secure", false, false, true}.config(), `mode value must be one of: \["auto"\]`},
		{"mode proxy", dnsSettings{"proxy", false, false, true}.config(), `mode value must be one of: \["auto"\]`},
		{"mode empty", `resource "glinet_dns" "test" {
  mode              = ""
  force_dns         = false
  rebind_protection = false
  override_vpn      = true
}`, `mode value must be one of: \["auto"\]`},
		{"no force_dns", `resource "glinet_dns" "test" {
  rebind_protection = false
  override_vpn      = true
}`, `The argument "force_dns" is required`},
		{"no rebind_protection", `resource "glinet_dns" "test" {
  force_dns    = false
  override_vpn = true
}`, `The argument "rebind_protection" is required`},
		{"no override_vpn", `resource "glinet_dns" "test" {
  force_dns         = false
  rebind_protection = false
}`, `The argument "override_vpn" is required`},
		{"servers", `resource "glinet_dns" "test" {
  force_dns         = false
  rebind_protection = false
  override_vpn      = true
  servers           = ["wan 192.168.1.1"]
}`, `Invalid Configuration for Read-Only Attribute`},
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
						statecheck.ExpectKnownValue(dnsAddress, tfjsonpath.New("rebind_protection"), knownvalue.Bool(true)),
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
