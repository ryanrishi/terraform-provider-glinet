package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	tailscaleAddress = "glinet_tailscale.test"
	exitNodeIP       = "100.64.0.1"
	otherExitNodeIP  = "100.64.0.2"
	ipv6ExitNodeIP   = "fd7a:115c:a1e0::1"
)

// tailscale is the configuration of a glinet_tailscale resource. Attributes with their zero value are not in the configuration.
type tailscale struct {
	enabled, lan, wan bool
	exitNode          string
}

func (c tailscale) config() string {
	res := fmt.Sprintf("\nresource \"glinet_tailscale\" \"test\" {\n  enabled = %t\n", c.enabled)
	if c.lan {
		res += "  lan_enabled = true\n"
	}
	if c.wan {
		res += "  wan_enabled = true\n"
	}
	if c.exitNode != "" {
		res += fmt.Sprintf("  exit_node_ip = %q\n", c.exitNode)
	}
	return res + "}\n"
}

// router returns the Tailscale configuration that a router with LAN subnet lanSubnet has after an apply of c.
func (c tailscale) router(lanSubnet string) glinet.TailscaleConfig {
	return glinet.TailscaleConfig{Enabled: c.enabled, LANEnabled: c.lan, WANEnabled: c.wan, ExitNodeIP: c.exitNode, LANIP: lanSubnet}
}

func expectTailscaleState(c tailscale, lanSubnet string) []statecheck.StateCheck {
	var exitNode knownvalue.Check = knownvalue.Null()
	if c.exitNode != "" {
		exitNode = knownvalue.StringExact(c.exitNode)
	}
	return expectAttributes(tailscaleAddress, map[string]knownvalue.Check{
		"id":           knownvalue.StringExact("tailscale"),
		"enabled":      knownvalue.Bool(c.enabled),
		"lan_enabled":  knownvalue.Bool(c.lan),
		"wan_enabled":  knownvalue.Bool(c.wan),
		"exit_node_ip": exitNode,
		"lan_subnet":   knownvalue.StringExact(lanSubnet),
	})
}

// expectRouterTailscale checks that the Tailscale configuration of the router is want.
func expectRouterTailscale(client *glinet.Client, want glinet.TailscaleConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.Tailscale.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if *got != want {
			return fmt.Errorf("router Tailscale configuration is %+v, want %+v", *got, want)
		}
		return nil
	}
}

const fakeLANSubnet = "192.168.8.0/24"

func setFakeTailscale(srv *glinettest.Server, c tailscale) {
	srv.Tailscale.SetConfig(glinettest.TailscaleConfig(c.router(fakeLANSubnet)))
}

// allowTailscaleWithoutAccess allows the reads of the tailscale module, and writes with lan_enabled and
// wan_enabled false and no exit node. It does not allow the login methods.
func allowTailscaleWithoutAccess(module, method string, params map[string]json.RawMessage) error {
	if module == "tailscale" && (method == "get_config" || method == "get_status") {
		return nil
	}
	enabled := string(params["enabled"])
	if module == "tailscale" && method == "set_config" && len(params) == 3 && (enabled == "true" || enabled == "false") &&
		string(params["lan_enabled"]) == "false" && string(params["wan_enabled"]) == "false" {
		return nil
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// tailscaleTestRouter returns the provider block and a client for the router of the test, and the initial
// Tailscale configuration. The real router is used only through guardedRealRouter with allowTailscaleWithoutAccess,
// and only when Tailscale is disabled with no access and no exit node. When the test ends, Tailscale is disabled
// again and the configuration is checked.
func tailscaleTestRouter(t *testing.T) (string, *glinet.Client, glinet.TailscaleConfig) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password), glinet.TailscaleConfig(srv.Tailscale.Config())
	}

	provider, client := guardedRealRouter(t, allowTailscaleWithoutAccess)
	initial, err := client.Tailscale.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *initial != (tailscale{}).router(initial.LANIP) {
		t.Fatalf("Tailscale configuration of the router is %+v; the test runs only when Tailscale is disabled with no access and no exit node", *initial)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.Tailscale.SetConfig(ctx, glinet.SetTailscaleConfigRequest{Enabled: false}); err != nil {
			t.Fatalf("disable Tailscale: %v", err)
		}

		restored, err := client.Tailscale.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored Tailscale configuration: %v", err)
		}
		if *restored != *initial {
			t.Errorf("restored Tailscale configuration is %+v, want %+v", *restored, *initial)
		}
	})

	return provider, client, *initial
}

// TestTailscaleResource enables Tailscale with no access and destroys the resource, which disables Tailscale.
// It also runs on the real router.
func TestTailscaleResource(t *testing.T) {
	parallel(t)
	provider, client, initial := tailscaleTestRouter(t)
	enabled := tailscale{enabled: true}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterTailscale(client, initial),
		Steps: []resource.TestStep{
			{
				Config:            provider + enabled.config(),
				ConfigStateChecks: expectTailscaleState(enabled, initial.LANIP),
				Check:             expectRouterTailscale(client, enabled.router(initial.LANIP)),
			},
			{
				ResourceName:      tailscaleAddress,
				ImportState:       true,
				ImportStateId:     "tailscale",
				ImportStateVerify: true,
			},
		},
	})
}

func TestTailscaleResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	configs := []tailscale{
		{},
		{enabled: true},
		{enabled: true, lan: true},
		{enabled: true, lan: true, wan: true},
		{enabled: true, lan: true, wan: true, exitNode: exitNodeIP},
		{enabled: true, lan: true, wan: true, exitNode: otherExitNodeIP},
		{enabled: true, wan: true, exitNode: otherExitNodeIP},
		{enabled: true, exitNode: otherExitNodeIP},
		{enabled: true, exitNode: ipv6ExitNodeIP},
		{enabled: true},
		{enabled: true, lan: true, exitNode: exitNodeIP},
		{},
		{enabled: true, wan: true},
	}

	var steps []resource.TestStep
	for i, c := range configs {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + c.config(),
			ConfigStateChecks: expectTailscaleState(c, fakeLANSubnet),
			Check:             expectRouterTailscale(client, c.router(fakeLANSubnet)),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(tailscaleAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}
	last := configs[len(configs)-1]
	steps = append(steps, resource.TestStep{Config: fakeRouterConfig(srv) + last.config(), ConfigPlanChecks: expectEmptyPlan})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterTailscale(client, tailscale{}.router(fakeLANSubnet)),
		Steps:                    steps,
	})
}

// TestTailscaleResourceExplicitDefaults configures lan_enabled and wan_enabled = false, which equals the defaults.
func TestTailscaleResourceExplicitDefaults(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	config := fakeRouterConfig(srv) + `
resource "glinet_tailscale" "test" {
  enabled     = false
  lan_enabled = false
  wan_enabled = false
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: expectTailscaleState(tailscale{}, fakeLANSubnet),
				Check:             expectRouterTailscale(client, tailscale{}.router(fakeLANSubnet)),
			},
			{
				Config:           fakeRouterConfig(srv) + tailscale{}.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestTailscaleResourceImport(t *testing.T) {
	parallel(t)
	for _, existing := range []tailscale{
		{},
		{enabled: true, lan: true, exitNode: exitNodeIP},
		{enabled: true, wan: true},
		{enabled: true, exitNode: ipv6ExitNodeIP},
	} {
		t.Run(fmt.Sprintf("%+v", existing), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeTailscale(srv, existing)
			config := fakeRouterConfig(srv) + existing.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       tailscaleAddress,
						ImportState:        true,
						ImportStateId:      "tailscale",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectTailscaleState(existing, fakeLANSubnet),
					},
				},
			})
		})
	}
}

func TestTailscaleResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + tailscale{enabled: true}.config(),
				ResourceName:  tailscaleAddress,
				ImportState:   true,
				ImportStateId: "tailscale0",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestTailscaleResourceDrift(t *testing.T) {
	parallel(t)
	tests := []struct {
		name         string
		want, router tailscale
	}{
		{"disabled", tailscale{enabled: true, lan: true, exitNode: exitNodeIP}, tailscale{}},
		{"enabled", tailscale{}, tailscale{enabled: true}},
		{"LAN access", tailscale{enabled: true}, tailscale{enabled: true, lan: true}},
		{"WAN access removed", tailscale{enabled: true, wan: true}, tailscale{enabled: true}},
		{"exit node changed", tailscale{enabled: true, exitNode: exitNodeIP}, tailscale{enabled: true, exitNode: otherExitNodeIP}},
		{"exit node removed", tailscale{enabled: true, exitNode: exitNodeIP}, tailscale{enabled: true}},
		{"exit node added", tailscale{enabled: true}, tailscale{enabled: true, exitNode: exitNodeIP}},
		{"access while disabled", tailscale{}, tailscale{lan: true, wan: true, exitNode: exitNodeIP}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			client := testClient(t, srv.URL, srv.Username, srv.Password)
			config := fakeRouterConfig(srv) + tt.want.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
					},
					{
						PreConfig:         func() { setFakeTailscale(srv, tt.router) },
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(tailscaleAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: expectTailscaleState(tt.want, fakeLANSubnet),
						Check:             expectRouterTailscale(client, tt.want.router(fakeLANSubnet)),
					},
				},
			})
		})
	}
}

// TestTailscaleResourceDisabledWithNullVariable uses a variable for exit_node_ip. Terraform validates the
// configuration before it knows the value of the variable.
func TestTailscaleResourceDisabledWithNullVariable(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
variable "exit_node_ip" {
  type    = string
  default = null
}

resource "glinet_tailscale" "test" {
  enabled      = false
  exit_node_ip = var.exit_node_ip
}
`,
				ConfigStateChecks: expectTailscaleState(tailscale{}, fakeLANSubnet),
				Check:             expectRouterTailscale(client, tailscale{}.router(fakeLANSubnet)),
			},
		},
	})
}

// TestTailscaleResourceLANSubnetChanged changes the LAN subnet of the router, which changes lan_subnet without a plan.
func TestTailscaleResourceLANSubnetChanged(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	c := tailscale{enabled: true}
	config := fakeRouterConfig(srv) + c.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					srv.Tailscale.SetConfig(glinettest.TailscaleConfig(c.router("192.168.9.0/24")))
				},
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectTailscaleState(c, "192.168.9.0/24"),
			},
		},
	})
}

// TestTailscaleResourceLANSubnetChangedInApply changes the LAN subnet of the router during an apply that updates
// the resource, as a change of the LAN configuration in the same apply can do.
func TestTailscaleResourceLANSubnetChangedInApply(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	enabled := tailscale{enabled: true}
	withLAN := tailscale{enabled: true, lan: true}
	set := srv.Handler("tailscale", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + enabled.config(),
			},
			{
				PreConfig: func() {
					srv.Handle("tailscale", "set_config", func(params json.RawMessage) (any, error) {
						config := srv.Tailscale.Config()
						config.LANIP = "192.168.9.0/24"
						srv.Tailscale.SetConfig(config)
						return set(params)
					})
				},
				Config:            fakeRouterConfig(srv) + withLAN.config(),
				ConfigPlanChecks:  expectResourceAction(tailscaleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectTailscaleState(withLAN, "192.168.9.0/24"),
			},
		},
	})
}

func TestTailscaleResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.Tailscale.Config()

	tests := []struct {
		config string
		error  string
	}{
		{tailscale{lan: true}.config(), `(?s)Invalid Tailscale Configuration.*lan_enabled = true, wan_enabled = true\s+and exit_node_ip are allowed only with enabled = true`},
		{tailscale{wan: true}.config(), `(?s)Invalid Tailscale Configuration.*wan_enabled`},
		{tailscale{exitNode: exitNodeIP}.config(), `(?s)Invalid Tailscale Configuration.*exit_node_ip`},
		{`resource "glinet_tailscale" "test" {
  lan_enabled = false
}`, `The argument "enabled" is required`},
		{`resource "glinet_tailscale" "test" {
  enabled    = true
  lan_subnet = "192.168.8.0/24"
}`, `Invalid Configuration for Read-Only Attribute`},
		{`resource "glinet_tailscale" "test" {
  enabled      = true
  exit_node_ip = ""
}`, `exit_node_ip must be an IPv4 or IPv6 address`},
	}
	for _, ip := range []string{" 100.64.0.1", "100.64.0.256", "100.64.0", "fd7a:115c:a1e0::1%tailscale0", "fd7a:115c:a1e0::/48", "exit-node", "100.64.0.0/10"} {
		tests = append(tests, struct {
			config string
			error  string
		}{tailscale{enabled: true, exitNode: ip}.config(), `exit_node_ip must be an IPv4 or IPv6 address`})
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

	if got := srv.Tailscale.Config(); got != before {
		t.Errorf("router Tailscale configuration is %+v, want %+v", got, before)
	}
}

func TestTailscaleResourceRouterErrors(t *testing.T) {
	parallel(t)
	c := tailscale{enabled: true, lan: true}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "tailscale", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Tailscale Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks:  expectResourceAction(tailscaleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectTailscaleState(c, fakeLANSubnet),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("tailscale", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + c.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "tailscale", "get_config"),
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Tailscale Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("tailscale", "get_config", get) },
					Config:           fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.Tailscale.Config()
		failRouterMethod(srv, "tailscale", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Tailscale Configuration.*Method not found`),
				},
			},
		})

		if got := srv.Tailscale.Config(); got != before {
			t.Errorf("router Tailscale configuration is %+v, want %+v", got, before)
		}
	})

	t.Run("set on delete", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		set := srv.Handler("tailscale", "set_config")
		want := glinettest.TailscaleConfig(c.router(fakeLANSubnet))

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + c.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "tailscale", "set_config"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable Tailscale.*Method not found`),
				},
				{
					PreConfig: func() {
						if got := srv.Tailscale.Config(); got != want {
							t.Errorf("router Tailscale configuration after the failed delete is %+v, want %+v", got, want)
						}
						srv.Handle("tailscale", "set_config", set)
					},
					Config: fakeRouterConfig(srv),
					Check:  expectRouterTailscale(testClient(t, srv.URL, srv.Username, srv.Password), tailscale{}.router(fakeLANSubnet)),
				},
			},
		})
	})
}

func TestAllowTailscaleWithoutAccess(t *testing.T) {
	parallel(t)
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"get_status", `{}`, true},
		{"set_config", `{"enabled":true,"lan_enabled":false,"wan_enabled":false}`, true},
		{"set_config", `{"enabled":false,"lan_enabled":false,"wan_enabled":false}`, true},
		{"set_config", `{"enabled":true,"lan_enabled":true,"wan_enabled":false}`, false},
		{"set_config", `{"enabled":true,"lan_enabled":false,"wan_enabled":true}`, false},
		{"set_config", `{"enabled":true,"lan_enabled":false,"wan_enabled":false,"exit_node_ip":"100.64.0.1"}`, false},
		{"set_config", `{"enabled":true}`, false},
		{"set_config", `{"enabled":1,"lan_enabled":false,"wan_enabled":false}`, false},
		{"get_auth_url", `{}`, false},
		{"logout", `{}`, false},
		{"get_exit_node_list", `{}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowTailscaleWithoutAccess("tailscale", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("tailscale.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
	if err := allowTailscaleWithoutAccess("system", "get_status", nil); err == nil {
		t.Error("system.get_status is allowed")
	}
}
