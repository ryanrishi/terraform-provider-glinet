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
	zeroTierAddress = "glinet_zerotier.test"
	// testNetworkID is a made-up ZeroTier network ID. It is the only ID that a test writes to the real router.
	testNetworkID  = "0123456789abcdef"
	otherNetworkID = "fedcba9876543210"
	thirdNetworkID = "00000000000000aa"
)

// zeroTier is the configuration of a glinet_zerotier resource. lan_enabled and wan_enabled are in the configuration only when true.
type zeroTier struct {
	enabled   bool
	networkID string
	lan, wan  bool
}

func (c zeroTier) config() string {
	res := fmt.Sprintf("\nresource \"glinet_zerotier\" \"test\" {\n  enabled    = %t\n  network_id = %q\n", c.enabled, c.networkID)
	if c.lan {
		res += "  lan_enabled = true\n"
	}
	if c.wan {
		res += "  wan_enabled = true\n"
	}
	return res + "}\n"
}

func (c zeroTier) router() glinet.ZeroTierConfig {
	return glinet.ZeroTierConfig{Enabled: c.enabled, ID: c.networkID, LANEnabled: c.lan, WANEnabled: c.wan}
}

func expectZeroTierState(c zeroTier) []statecheck.StateCheck {
	return expectAttributes(zeroTierAddress, map[string]knownvalue.Check{
		"id":          knownvalue.StringExact("zerotier"),
		"enabled":     knownvalue.Bool(c.enabled),
		"network_id":  knownvalue.StringExact(c.networkID),
		"lan_enabled": knownvalue.Bool(c.lan),
		"wan_enabled": knownvalue.Bool(c.wan),
	})
}

// expectRouterZeroTier checks that the ZeroTier configuration of the router is want.
func expectRouterZeroTier(client *glinet.Client, want glinet.ZeroTierConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.ZeroTier.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if *got != want {
			return fmt.Errorf("router ZeroTier configuration is %+v, want %+v", *got, want)
		}
		return nil
	}
}

func setFakeZeroTier(srv *glinettest.Server, c zeroTier) {
	srv.ZeroTier.SetConfig(glinettest.ZeroTierConfig(c.router()))
}

// allowZeroTierTestNetwork allows the reads of the zerotier module, and writes with the network ID testNetworkID
// and lan_enabled and wan_enabled false.
func allowZeroTierTestNetwork(module, method string, params map[string]json.RawMessage) error {
	if module == "zerotier" && (method == "get_config" || method == "get_status") {
		return nil
	}
	enabled := string(params["enabled"])
	if module == "zerotier" && method == "set_config" && len(params) == 4 && (enabled == "true" || enabled == "false") &&
		string(params["id"]) == `"`+testNetworkID+`"` &&
		string(params["lan_enabled"]) == "false" && string(params["wan_enabled"]) == "false" {
		return nil
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// zeroTierTestRouter returns the provider block and a client for the router of the test. The router has ZeroTier
// disabled with the network ID testNetworkID and no access. The real router is used only through guardedRealRouter
// with allowZeroTierTestNetwork, and only when it has this configuration. When the test ends, ZeroTier is disabled
// again and the configuration is checked.
func zeroTierTestRouter(t *testing.T) (string, *glinet.Client) {
	initial := zeroTier{networkID: testNetworkID}.router()
	if !realRouter() {
		srv := glinettest.NewServer(t)
		srv.ZeroTier.SetConfig(glinettest.ZeroTierConfig(initial))
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	provider, client := guardedRealRouter(t, allowZeroTierTestNetwork)
	got, err := client.ZeroTier.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *got != initial {
		t.Fatalf("ZeroTier configuration of the router differs from %+v; the test runs only with this configuration", initial)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.ZeroTier.SetConfig(ctx, initial); err != nil {
			t.Fatalf("disable ZeroTier: %v", err)
		}

		restored, err := client.ZeroTier.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored ZeroTier configuration: %v", err)
		}
		if *restored != initial {
			t.Errorf("restored ZeroTier configuration differs from %+v (enabled %t, lan_enabled %t, wan_enabled %t, same ID %t)",
				initial, restored.Enabled, restored.LANEnabled, restored.WANEnabled, restored.ID == initial.ID)
		}
	})

	return provider, client
}

// TestZeroTierResource enables ZeroTier with the made-up network ID testNetworkID and no access, and destroys
// the resource, which disables ZeroTier. It also runs on the real router.
func TestZeroTierResource(t *testing.T) {
	parallel(t)
	provider, client := zeroTierTestRouter(t)
	enabled := zeroTier{enabled: true, networkID: testNetworkID}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterZeroTier(client, zeroTier{networkID: testNetworkID}.router()),
		Steps: []resource.TestStep{
			{
				Config:            provider + enabled.config(),
				ConfigStateChecks: expectZeroTierState(enabled),
				Check:             expectRouterZeroTier(client, enabled.router()),
			},
			{
				ResourceName:      zeroTierAddress,
				ImportState:       true,
				ImportStateId:     "zerotier",
				ImportStateVerify: true,
			},
		},
	})
}

// TestZeroTierResourceUpdate changes each attribute. While ZeroTier is disabled, the router keeps the values of
// the last enabled write, and the state keeps the configured ones.
func TestZeroTierResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	steps := []struct {
		config zeroTier
		router glinet.ZeroTierConfig
	}{
		{zeroTier{enabled: true, networkID: testNetworkID}, zeroTier{enabled: true, networkID: testNetworkID}.router()},
		{zeroTier{enabled: true, networkID: testNetworkID, lan: true}, zeroTier{enabled: true, networkID: testNetworkID, lan: true}.router()},
		{zeroTier{enabled: true, networkID: testNetworkID, lan: true, wan: true}, zeroTier{enabled: true, networkID: testNetworkID, lan: true, wan: true}.router()},
		{zeroTier{enabled: true, networkID: otherNetworkID, lan: true, wan: true}, zeroTier{enabled: true, networkID: otherNetworkID, lan: true, wan: true}.router()},
		{zeroTier{enabled: true, networkID: otherNetworkID, wan: true}, zeroTier{enabled: true, networkID: otherNetworkID, wan: true}.router()},
		{zeroTier{enabled: false, networkID: otherNetworkID, wan: true}, zeroTier{networkID: otherNetworkID, wan: true}.router()},
		{zeroTier{enabled: false, networkID: thirdNetworkID, wan: true}, zeroTier{networkID: otherNetworkID, wan: true}.router()},
		{zeroTier{enabled: false, networkID: thirdNetworkID, lan: true}, zeroTier{networkID: otherNetworkID, wan: true}.router()},
		{zeroTier{enabled: true, networkID: thirdNetworkID, lan: true}, zeroTier{enabled: true, networkID: thirdNetworkID, lan: true}.router()},
		{zeroTier{enabled: true, networkID: testNetworkID}, zeroTier{enabled: true, networkID: testNetworkID}.router()},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + s.config.config(),
			ConfigStateChecks: expectZeroTierState(s.config),
			Check:             expectRouterZeroTier(client, s.router),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, step, resource.TestStep{Config: step.Config, ConfigPlanChecks: expectEmptyPlan})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterZeroTier(client, zeroTier{networkID: testNetworkID}.router()),
		Steps:                    testSteps,
	})
}

// TestZeroTierResourceDisabledCreate creates a disabled resource on a router that has another network ID.
// The router keeps its ID and access until ZeroTier is enabled.
func TestZeroTierResourceDisabledCreate(t *testing.T) {
	parallel(t)
	for _, routerEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("router enabled %t", routerEnabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			client := testClient(t, srv.URL, srv.Username, srv.Password)
			setFakeZeroTier(srv, zeroTier{enabled: routerEnabled, networkID: otherNetworkID, lan: true})
			disabled := zeroTier{networkID: testNetworkID, wan: true}
			enabled := zeroTier{enabled: true, networkID: testNetworkID, wan: true}
			kept := zeroTier{networkID: otherNetworkID, lan: true}.router()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectRouterZeroTier(client, zeroTier{networkID: testNetworkID, wan: true}.router()),
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + disabled.config(),
						ConfigStateChecks: expectZeroTierState(disabled),
						Check:             expectRouterZeroTier(client, kept),
					},
					{
						Config:           fakeRouterConfig(srv) + disabled.config(),
						ConfigPlanChecks: expectEmptyPlan,
					},
					{
						Config:            fakeRouterConfig(srv) + enabled.config(),
						ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: expectZeroTierState(enabled),
						Check:             expectRouterZeroTier(client, enabled.router()),
					},
				},
			})
		})
	}
}

func TestZeroTierResourceImport(t *testing.T) {
	parallel(t)
	for _, existing := range []zeroTier{
		{enabled: true, networkID: testNetworkID, lan: true},
		{enabled: false, networkID: testNetworkID, wan: true},
	} {
		t.Run(fmt.Sprintf("enabled %t", existing.enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeZeroTier(srv, existing)
			config := fakeRouterConfig(srv) + existing.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       zeroTierAddress,
						ImportState:        true,
						ImportStateId:      "zerotier",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectZeroTierState(existing),
					},
				},
			})
		})
	}
}

// TestZeroTierResourceImportNoNetworkID imports from a router that never had a network ID.
// The next apply writes the configured values to the state, and the router keeps no ID while ZeroTier is disabled.
func TestZeroTierResourceImportNoNetworkID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	c := zeroTier{networkID: testNetworkID}
	config := fakeRouterConfig(srv) + c.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       zeroTierAddress,
				ImportState:        true,
				ImportStateId:      "zerotier",
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectZeroTierState(c),
				Check:             expectRouterZeroTier(client, glinet.ZeroTierConfig{}),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestZeroTierResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + zeroTier{networkID: testNetworkID}.config(),
				ResourceName:  zeroTierAddress,
				ImportState:   true,
				ImportStateId: testNetworkID,
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestZeroTierResourceDrift(t *testing.T) {
	parallel(t)
	enabled := zeroTier{enabled: true, networkID: testNetworkID, lan: true}
	disabled := zeroTier{networkID: testNetworkID, lan: true}

	tests := []struct {
		name         string
		want, router zeroTier
		action       plancheck.ResourceActionType
		routerAfter  glinet.ZeroTierConfig
	}{
		{"disabled", enabled, zeroTier{networkID: testNetworkID, lan: true}, plancheck.ResourceActionUpdate, enabled.router()},
		{"network ID changed", enabled, zeroTier{enabled: true, networkID: otherNetworkID, lan: true}, plancheck.ResourceActionUpdate, enabled.router()},
		{"LAN access removed", enabled, zeroTier{enabled: true, networkID: testNetworkID}, plancheck.ResourceActionUpdate, enabled.router()},
		{"WAN access added", enabled, zeroTier{enabled: true, networkID: testNetworkID, lan: true, wan: true}, plancheck.ResourceActionUpdate, enabled.router()},
		{"enabled", disabled, zeroTier{enabled: true, networkID: otherNetworkID, wan: true}, plancheck.ResourceActionUpdate, zeroTier{networkID: otherNetworkID, wan: true}.router()},
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
						PreConfig:         func() { setFakeZeroTier(srv, tt.router) },
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(zeroTierAddress, tt.action),
						ConfigStateChecks: expectZeroTierState(tt.want),
						Check:             expectRouterZeroTier(client, tt.routerAfter),
					},
				},
			})
		})
	}
}

// TestZeroTierResourceDisabledDriftNotCompared changes the stored values of a router with ZeroTier disabled.
// The provider does not compare them while ZeroTier is disabled, and writes the configured ones at the next enable.
func TestZeroTierResourceDisabledDriftNotCompared(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	disabled := zeroTier{networkID: testNetworkID}
	enabled := zeroTier{enabled: true, networkID: testNetworkID}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + disabled.config(),
			},
			{
				PreConfig:         func() { setFakeZeroTier(srv, zeroTier{networkID: otherNetworkID, lan: true}) },
				Config:            fakeRouterConfig(srv) + disabled.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectZeroTierState(disabled),
			},
			{
				Config:            fakeRouterConfig(srv) + enabled.config(),
				ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectZeroTierState(enabled),
				Check:             expectRouterZeroTier(client, enabled.router()),
			},
		},
	})
}

func TestZeroTierResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.ZeroTier.Config()

	tests := []struct {
		config string
		error  string
	}{
		{`resource "glinet_zerotier" "test" {
  network_id = "0123456789abcdef"
}`, `The argument "enabled" is required`},
		{`resource "glinet_zerotier" "test" {
  enabled = false
}`, `The argument "network_id" is required`},
	}
	for _, id := range []string{"", "0123456789abcde", "0123456789abcdef0", "0123456789abcdeg", " 0123456789abcdef", "0x23456789abcdef"} {
		for _, enabled := range []bool{true, false} {
			tests = append(tests, struct {
				config string
				error  string
			}{zeroTier{enabled: enabled, networkID: id}.config(), `network_id must be 16 hexadecimal digits`})
		}
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

	if got := srv.ZeroTier.Config(); got != before {
		t.Errorf("router ZeroTier configuration is %+v, want %+v", got, before)
	}
}

func TestZeroTierResourceRouterErrors(t *testing.T) {
	parallel(t)
	c := zeroTier{enabled: true, networkID: testNetworkID, lan: true}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "zerotier", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read ZeroTier Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectZeroTierState(c),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("zerotier", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + c.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "zerotier", "get_config"),
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read ZeroTier Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("zerotier", "get_config", get) },
					Config:           fakeRouterConfig(srv) + c.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.ZeroTier.Config()
		failRouterMethod(srv, "zerotier", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + c.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set ZeroTier Configuration.*Method not found`),
				},
			},
		})

		if got := srv.ZeroTier.Config(); got != before {
			t.Errorf("router ZeroTier configuration is %+v, want %+v", got, before)
		}
	})

	t.Run("set on delete", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		set := srv.Handler("zerotier", "set_config")
		want := glinettest.ZeroTierConfig(c.router())

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + c.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "zerotier", "set_config"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable ZeroTier.*Method not found`),
				},
				{
					PreConfig: func() {
						if got := srv.ZeroTier.Config(); got != want {
							t.Errorf("router ZeroTier configuration after the failed delete is %+v, want %+v", got, want)
						}
						srv.Handle("zerotier", "set_config", set)
					},
					Config: fakeRouterConfig(srv),
					Check: expectRouterZeroTier(testClient(t, srv.URL, srv.Username, srv.Password),
						zeroTier{networkID: testNetworkID, lan: true}.router()),
				},
			},
		})
	})
}

func TestAllowZeroTierTestNetwork(t *testing.T) {
	parallel(t)
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"get_status", `{}`, true},
		{"set_config", `{"enabled":true,"id":"0123456789abcdef","lan_enabled":false,"wan_enabled":false}`, true},
		{"set_config", `{"enabled":false,"id":"0123456789abcdef","lan_enabled":false,"wan_enabled":false}`, true},
		{"set_config", `{"enabled":true,"id":"fedcba9876543210","lan_enabled":false,"wan_enabled":false}`, false},
		{"set_config", `{"enabled":false,"id":"","lan_enabled":false,"wan_enabled":false}`, false},
		{"set_config", `{"enabled":true,"id":"0123456789abcdef","lan_enabled":true,"wan_enabled":false}`, false},
		{"set_config", `{"enabled":true,"id":"0123456789abcdef","lan_enabled":false,"wan_enabled":true}`, false},
		{"set_config", `{"enabled":true,"id":"0123456789abcdef"}`, false},
		{"set_config", `{"enabled":false}`, false},
		{"set_config", `{"enabled":1,"id":"0123456789abcdef","lan_enabled":false,"wan_enabled":false}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowZeroTierTestNetwork("zerotier", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("zerotier.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
	if err := allowZeroTierTestNetwork("system", "get_status", nil); err == nil {
		t.Error("system.get_status is allowed")
	}
}

const (
	upperCaseNetworkID = "0123456789ABCDEF"
	mixedCaseNetworkID = "0123456789AbCdEf"
)

// TestZeroTierResourceLetterCase configures the network ID in upper case and in mixed case.
// The router keeps the ID as written, and the state keeps the configured letter case,
// also when the router has the ID in another letter case.
func TestZeroTierResourceLetterCase(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	upper := zeroTier{enabled: true, networkID: upperCaseNetworkID}
	mixed := zeroTier{enabled: true, networkID: mixedCaseNetworkID, lan: true}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterZeroTier(client, zeroTier{networkID: upperCaseNetworkID, lan: true}.router()),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + upper.config(),
				ConfigStateChecks: expectZeroTierState(upper),
				Check:             expectRouterZeroTier(client, upper.router()),
			},
			{
				PreConfig:         func() { setFakeZeroTier(srv, zeroTier{enabled: true, networkID: testNetworkID}) },
				Config:            fakeRouterConfig(srv) + upper.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectZeroTierState(upper),
			},
			{
				Config:            fakeRouterConfig(srv) + mixed.config(),
				ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectZeroTierState(mixed),
				Check:             expectRouterZeroTier(client, mixed.router()),
			},
			{
				PreConfig:         func() { setFakeZeroTier(srv, zeroTier{enabled: true, networkID: upperCaseNetworkID, lan: true}) },
				Config:            fakeRouterConfig(srv) + mixed.config(),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectZeroTierState(mixed),
			},
		},
	})
}

// TestZeroTierResourceImportLetterCase imports a network ID in lower case for a configuration in upper case.
// The import sets the ID of the router, so the next apply writes the configured letter case.
func TestZeroTierResourceImportLetterCase(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	setFakeZeroTier(srv, zeroTier{enabled: true, networkID: testNetworkID})
	upper := zeroTier{enabled: true, networkID: upperCaseNetworkID}
	config := fakeRouterConfig(srv) + upper.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       zeroTierAddress,
				ImportState:        true,
				ImportStateId:      "zerotier",
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(zeroTierAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectZeroTierState(upper),
				Check:             expectRouterZeroTier(client, upper.router()),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}
