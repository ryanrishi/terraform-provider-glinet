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
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const adGuardHomeAddress = "glinet_adguardhome.test"

func adGuardHomeConfig(c glinet.AdGuardHomeConfig) string {
	return fmt.Sprintf(`
resource "glinet_adguardhome" "test" {
  enabled     = %t
  dns_enabled = %t
}
`, c.Enabled, c.DNSEnabled)
}

func expectAdGuardHomeState(c glinet.AdGuardHomeConfig) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(adGuardHomeAddress, tfjsonpath.New("id"), knownvalue.StringExact("adguardhome")),
		statecheck.ExpectKnownValue(adGuardHomeAddress, tfjsonpath.New("enabled"), knownvalue.Bool(c.Enabled)),
		statecheck.ExpectKnownValue(adGuardHomeAddress, tfjsonpath.New("dns_enabled"), knownvalue.Bool(c.DNSEnabled)),
	}
}

// expectRouterAdGuardHome checks that the AdGuard Home configuration of the router is want.
func expectRouterAdGuardHome(client *glinet.Client, want glinet.AdGuardHomeConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.AdGuardHome.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if *got != want {
			return fmt.Errorf("router AdGuard Home configuration is %+v, want %+v", *got, want)
		}
		return nil
	}
}

func setFakeAdGuardHome(srv *glinettest.Server, c glinet.AdGuardHomeConfig) {
	srv.AdGuardHome.SetConfig(glinettest.AdGuardHomeConfig(c))
}

// allowAdGuardHomeDisabled allows the read of the AdGuard Home configuration, and writes that keep it disabled.
func allowAdGuardHomeDisabled(module, method string, params map[string]json.RawMessage) error {
	if module == "adguardhome" && method == "get_config" {
		return nil
	}
	dnsEnabled := string(params["dns_enabled"])
	if module == "adguardhome" && method == "set_config" && len(params) == 2 &&
		string(params["enabled"]) == "false" && (dnsEnabled == "true" || dnsEnabled == "false") {
		return nil
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// adGuardHomeTestRouter returns the provider block and a client for the router of the test, and the initial
// AdGuard Home configuration. The real router is used only through guardedRealRouter with allowAdGuardHomeDisabled,
// only when AdGuard Home is disabled on it, and its configuration goes back to its initial value when the test ends.
func adGuardHomeTestRouter(t *testing.T) (string, *glinet.Client, glinet.AdGuardHomeConfig) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password), glinet.AdGuardHomeConfig(srv.AdGuardHome.Config())
	}

	provider, client := guardedRealRouter(t, allowAdGuardHomeDisabled)
	initial, err := client.AdGuardHome.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Enabled {
		t.Fatalf("AdGuard Home is enabled on the router; the test runs only when it is disabled")
	}

	t.Cleanup(func() {
		ctx := context.Background()
		err := client.AdGuardHome.SetConfig(ctx, glinet.AdGuardHomeConfig{Enabled: initial.Enabled, DNSEnabled: initial.DNSEnabled})
		if err != nil {
			t.Fatalf("restore AdGuard Home configuration: %v", err)
		}

		restored, err := client.AdGuardHome.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored AdGuard Home configuration: %v", err)
		}
		if *restored != *initial {
			t.Errorf("restored AdGuard Home configuration is %+v, want %+v", *restored, *initial)
		}
	})

	return provider, client, *initial
}

// TestAdGuardHomeResource keeps AdGuard Home disabled and changes only dns_enabled, because it also runs on the real router.
func TestAdGuardHomeResource(t *testing.T) {
	parallel(t)
	provider, client, initial := adGuardHomeTestRouter(t)
	changed := glinet.AdGuardHomeConfig{Enabled: false, DNSEnabled: !initial.DNSEnabled}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterAdGuardHome(client, initial),
		Steps: []resource.TestStep{
			{
				Config:            provider + adGuardHomeConfig(changed),
				ConfigStateChecks: expectAdGuardHomeState(changed),
				Check:             expectRouterAdGuardHome(client, changed),
			},
			{
				Config:            provider + adGuardHomeConfig(initial),
				ConfigPlanChecks:  expectResourceAction(adGuardHomeAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectAdGuardHomeState(initial),
				Check:             expectRouterAdGuardHome(client, initial),
			},
			{
				ResourceName:      adGuardHomeAddress,
				ImportState:       true,
				ImportStateId:     "adguardhome",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAdGuardHomeResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	configs := []glinet.AdGuardHomeConfig{
		{Enabled: true, DNSEnabled: true},
		{Enabled: true, DNSEnabled: false},
		{Enabled: false, DNSEnabled: false},
		{Enabled: false, DNSEnabled: true},
		{Enabled: true, DNSEnabled: true},
	}

	var steps []resource.TestStep
	for i, c := range configs {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + adGuardHomeConfig(c),
			ConfigStateChecks: expectAdGuardHomeState(c),
			Check:             expectRouterAdGuardHome(client, c),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(adGuardHomeAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterAdGuardHome(client, configs[len(configs)-1]),
		Steps:                    steps,
	})
}

func TestAdGuardHomeResourceImport(t *testing.T) {
	parallel(t)
	for _, existing := range []glinet.AdGuardHomeConfig{
		{Enabled: true, DNSEnabled: false},
		{Enabled: false, DNSEnabled: true},
	} {
		t.Run(fmt.Sprintf("%+v", existing), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeAdGuardHome(srv, existing)
			config := fakeRouterConfig(srv) + adGuardHomeConfig(existing)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       adGuardHomeAddress,
						ImportState:        true,
						ImportStateId:      "adguardhome",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectAdGuardHomeState(existing),
					},
				},
			})
		})
	}
}

func TestAdGuardHomeResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + adGuardHomeConfig(glinet.AdGuardHomeConfig{}),
				ResourceName:  adGuardHomeAddress,
				ImportState:   true,
				ImportStateId: "adguard",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAdGuardHomeResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	want := glinet.AdGuardHomeConfig{Enabled: true, DNSEnabled: true}
	config := fakeRouterConfig(srv) + adGuardHomeConfig(want)

	steps := []resource.TestStep{{Config: config}}
	for _, router := range []glinet.AdGuardHomeConfig{
		{Enabled: false, DNSEnabled: true},
		{Enabled: true, DNSEnabled: false},
	} {
		steps = append(steps, resource.TestStep{
			PreConfig:         func() { setFakeAdGuardHome(srv, router) },
			Config:            config,
			ConfigPlanChecks:  expectResourceAction(adGuardHomeAddress, plancheck.ResourceActionUpdate),
			ConfigStateChecks: expectAdGuardHomeState(want),
			Check:             expectRouterAdGuardHome(client, want),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func TestAdGuardHomeResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.AdGuardHome.Config()

	for _, tt := range []struct {
		config string
		error  string
	}{
		{`resource "glinet_adguardhome" "test" {
  dns_enabled = true
}`, `The argument "enabled" is required`},
		{`resource "glinet_adguardhome" "test" {
  enabled = false
}`, `The argument "dns_enabled" is required`},
	} {
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

	if got := srv.AdGuardHome.Config(); got != before {
		t.Errorf("router AdGuard Home configuration is %+v, want %+v", got, before)
	}
}

func TestAdGuardHomeResourceRouterErrors(t *testing.T) {
	parallel(t)
	c := glinet.AdGuardHomeConfig{Enabled: true, DNSEnabled: false}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "adguardhome", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + adGuardHomeConfig(c),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read AdGuard Home Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + adGuardHomeConfig(c),
					ConfigPlanChecks:  expectResourceAction(adGuardHomeAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectAdGuardHomeState(c),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("adguardhome", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + adGuardHomeConfig(c),
				},
				{
					PreConfig:   failRouterMethod(srv, "adguardhome", "get_config"),
					Config:      fakeRouterConfig(srv) + adGuardHomeConfig(c),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read AdGuard Home Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("adguardhome", "get_config", get) },
					Config:           fakeRouterConfig(srv) + adGuardHomeConfig(c),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.AdGuardHome.Config()
		failRouterMethod(srv, "adguardhome", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + adGuardHomeConfig(c),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set AdGuard Home Configuration.*Method not found`),
				},
			},
		})

		if got := srv.AdGuardHome.Config(); got != before {
			t.Errorf("router AdGuard Home configuration is %+v, want %+v", got, before)
		}
	})
}

func TestAllowAdGuardHomeDisabled(t *testing.T) {
	parallel(t)
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"set_config", `{"enabled":false,"dns_enabled":true}`, true},
		{"set_config", `{"enabled":false,"dns_enabled":false}`, true},
		{"set_config", `{"enabled":true,"dns_enabled":true}`, false},
		{"set_config", `{"enabled":0,"dns_enabled":true}`, false},
		{"set_config", `{"dns_enabled":true}`, false},
		{"set_config", `{"enabled":false}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowAdGuardHomeDisabled("adguardhome", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("adguardhome.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
}
