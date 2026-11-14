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

const ddnsAddress = "glinet_ddns.test"

func ddnsConfig(enabled bool) string {
	return fmt.Sprintf(`
resource "glinet_ddns" "test" {
  enabled = %t
}
`, enabled)
}

func expectDDNSState(enabled bool) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(ddnsAddress, tfjsonpath.New("id"), knownvalue.StringExact("ddns")),
		statecheck.ExpectKnownValue(ddnsAddress, tfjsonpath.New("enabled"), knownvalue.Bool(enabled)),
	}
}

// expectRouterDDNS checks that DDNS is enabled on the router when enabled is true, and disabled when it is false.
func expectRouterDDNS(client *glinet.Client, enabled bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.DDNS.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if config.Enabled != enabled {
			return fmt.Errorf("router DDNS enabled is %t, want %t", config.Enabled, enabled)
		}
		return nil
	}
}

func setFakeDDNS(srv *glinettest.Server, enabled bool) {
	config := srv.DDNS.Config()
	config.Enabled = enabled
	srv.DDNS.SetConfig(config)
}

// allowDDNS allows the reads of the ddns module and the writes that set only enable_ddns.
func allowDDNS(module, method string, params map[string]json.RawMessage) error {
	if module == "ddns" && (method == "get_config" || method == "get_status") {
		return nil
	}
	enabled := string(params["enable_ddns"])
	if module == "ddns" && method == "set_config" && len(params) == 1 && (enabled == "true" || enabled == "false") {
		return nil
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// ddnsTestRouter returns the provider block and a client for the router of the test.
// The real router is used only through guardedRealRouter with allowDDNS and only when DDNS is disabled on it.
// When the test ends, DDNS is disabled again and the device ID is checked.
func ddnsTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	provider, client := guardedRealRouter(t, allowDDNS)
	initial, err := client.DDNS.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Enabled {
		t.Fatalf("DDNS is enabled on the router; the test runs only when it is disabled")
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.DDNS.SetEnabled(ctx, false); err != nil {
			t.Fatalf("disable DDNS: %v", err)
		}

		restored, err := client.DDNS.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored DDNS configuration: %v", err)
		}
		if *restored != *initial {
			t.Errorf("restored DDNS configuration differs from the initial one (enabled %t, want %t)", restored.Enabled, initial.Enabled)
		}
	})

	return provider, client
}

// TestDDNSResource enables DDNS and destroys the resource, which disables DDNS. It also runs on the real router.
func TestDDNSResource(t *testing.T) {
	provider, client := ddnsTestRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDDNS(client, false),
		Steps: []resource.TestStep{
			{
				Config:            provider + ddnsConfig(true),
				ConfigStateChecks: expectDDNSState(true),
				Check:             expectRouterDDNS(client, true),
			},
			{
				ResourceName:      ddnsAddress,
				ImportState:       true,
				ImportStateId:     "ddns",
				ImportStateVerify: true,
			},
		},
	})
}

func TestDDNSResourceUpdate(t *testing.T) {
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	deviceID := srv.DDNS.Config().DeviceID

	var steps []resource.TestStep
	for i, enabled := range []bool{false, true, false, true} {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + ddnsConfig(enabled),
			ConfigStateChecks: expectDDNSState(enabled),
			Check:             expectRouterDDNS(client, enabled),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(ddnsAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}
	steps = append(steps, resource.TestStep{Config: fakeRouterConfig(srv) + ddnsConfig(true), ConfigPlanChecks: expectEmptyPlan})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDDNS(client, false),
		Steps:                    steps,
	})

	if got := srv.DDNS.Config().DeviceID; got != deviceID {
		t.Errorf("router DDNS device ID is %q, want %q", got, deviceID)
	}
}

func TestDDNSResourceDestroyDisabled(t *testing.T) {
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDDNS(client, false),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ddnsConfig(false),
				Check:  expectRouterDDNS(client, false),
			},
		},
	})
}

func TestDDNSResourceImport(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled %t", enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeDDNS(srv, enabled)
			config := fakeRouterConfig(srv) + ddnsConfig(enabled)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       ddnsAddress,
						ImportState:        true,
						ImportStateId:      "ddns",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectDDNSState(enabled),
					},
				},
			})
		})
	}
}

func TestDDNSResourceImportInvalidID(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + ddnsConfig(true),
				ResourceName:  ddnsAddress,
				ImportState:   true,
				ImportStateId: "dynamic_dns",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestDDNSResourceDrift(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled %t", enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			client := testClient(t, srv.URL, srv.Username, srv.Password)
			config := fakeRouterConfig(srv) + ddnsConfig(enabled)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
					},
					{
						PreConfig:         func() { setFakeDDNS(srv, !enabled) },
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(ddnsAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: expectDDNSState(enabled),
						Check:             expectRouterDDNS(client, enabled),
					},
				},
			})
		})
	}
}

func TestDDNSResourceInvalidConfig(t *testing.T) {
	srv := glinettest.NewServer(t)
	before := srv.DDNS.Config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + `resource "glinet_ddns" "test" {}`,
				ExpectError: regexp.MustCompile(`The argument "enabled" is required`),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_ddns" "test" {
  enabled   = true
  device_id = "x"
}
`,
				ExpectError: regexp.MustCompile(`An argument named "device_id" is not expected here`),
			},
		},
	})

	if got := srv.DDNS.Config(); got != before {
		t.Errorf("router DDNS configuration is %+v, want %+v", got, before)
	}
}

func TestDDNSResourceRouterErrors(t *testing.T) {
	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "ddns", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ddnsConfig(true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read DDNS Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + ddnsConfig(true),
					ConfigPlanChecks:  expectResourceAction(ddnsAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectDDNSState(true),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("ddns", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + ddnsConfig(true),
				},
				{
					PreConfig:   failRouterMethod(srv, "ddns", "get_config"),
					Config:      fakeRouterConfig(srv) + ddnsConfig(true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read DDNS Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("ddns", "get_config", get) },
					Config:           fakeRouterConfig(srv) + ddnsConfig(true),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("get on import", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "ddns", "get_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + ddnsConfig(true),
					ResourceName:  ddnsAddress,
					ImportState:   true,
					ImportStateId: "ddns",
					ExpectError:   regexp.MustCompile(`(?s)Unable to Read DDNS Configuration.*Method not found`),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.DDNS.Config()
		failRouterMethod(srv, "ddns", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ddnsConfig(true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set DDNS.*Method not found`),
				},
			},
		})

		if got := srv.DDNS.Config(); got != before {
			t.Errorf("router DDNS configuration is %+v, want %+v", got, before)
		}
	})

	t.Run("set on delete", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		set := srv.Handler("ddns", "set_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + ddnsConfig(true),
				},
				{
					PreConfig:   failRouterMethod(srv, "ddns", "set_config"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable DDNS.*Method not found`),
				},
				{
					PreConfig: func() {
						if !srv.DDNS.Config().Enabled {
							t.Error("router DDNS is disabled after the failed delete")
						}
						srv.Handle("ddns", "set_config", set)
					},
					Config: fakeRouterConfig(srv),
					Check: func(*terraform.State) error {
						if srv.DDNS.Config().Enabled {
							return fmt.Errorf("router DDNS is enabled after the delete")
						}
						return nil
					},
				},
			},
		})
	})
}

func TestAllowDDNS(t *testing.T) {
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"get_status", `{}`, true},
		{"set_config", `{"enable_ddns":true}`, true},
		{"set_config", `{"enable_ddns":false}`, true},
		{"set_config", `{}`, false},
		{"set_config", `{"enable_ddns":1}`, false},
		{"set_config", `{"enable_ddns":true,"device_id":"x"}`, false},
		{"get_auth_url", `{}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowDDNS("ddns", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("ddns.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
	if err := allowDDNS("system", "get_status", nil); err == nil {
		t.Error("system.get_status is allowed")
	}
}
