package provider

import (
	"context"
	"fmt"
	"os"
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

const ledAddress = "glinet_led.test"

func ledConfig(enabled bool) string {
	return fmt.Sprintf(`
resource "glinet_led" "test" {
  enabled = %t
}
`, enabled)
}

func expectLEDState(enabled bool) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(ledAddress, tfjsonpath.New("id"), knownvalue.StringExact("led")),
		statecheck.ExpectKnownValue(ledAddress, tfjsonpath.New("enabled"), knownvalue.Bool(enabled)),
	}
}

// expectRouterLED checks that the LED configuration of the router is want.
func expectRouterLED(client *glinet.Client, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.LED.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if got.Enabled != want {
			return fmt.Errorf("router led_enable is %t, want %t", got.Enabled, want)
		}
		return nil
	}
}

// ledTestRouter returns the provider block, a client and the initial LED configuration of the router of the test.
// On the real router, the LED configuration goes back to its initial value when the test ends.
func ledTestRouter(t *testing.T) (string, *glinet.Client, bool) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password), srv.LED.Config().Enabled
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.LED.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.LED.SetConfig(ctx, *initial); err != nil {
			t.Fatalf("restore LED configuration: %v", err)
		}

		restored, err := client.LED.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read restored LED configuration: %v", err)
		}
		if *restored != *initial {
			t.Errorf("restored LED configuration is %+v, want %+v", *restored, *initial)
		}
	})

	return `provider "glinet" {}`, client, initial.Enabled
}

func TestLEDResource(t *testing.T) {
	provider, client, initial := ledTestRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLED(client, initial),
		Steps: []resource.TestStep{
			{
				Config:            provider + ledConfig(!initial),
				ConfigStateChecks: expectLEDState(!initial),
				Check:             expectRouterLED(client, !initial),
			},
			{
				Config:            provider + ledConfig(initial),
				ConfigPlanChecks:  expectResourceAction(ledAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLEDState(initial),
				Check:             expectRouterLED(client, initial),
			},
			{
				ResourceName:      ledAddress,
				ImportState:       true,
				ImportStateId:     "led",
				ImportStateVerify: true,
			},
		},
	})
}

func TestLEDResourceImport(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled %t", enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.LED.SetConfig(glinettest.LEDConfig{Enabled: enabled})
			config := fakeRouterConfig(srv) + ledConfig(enabled)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       ledAddress,
						ImportState:        true,
						ImportStateId:      "led",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectLEDState(enabled),
					},
				},
			})
		})
	}
}

func TestLEDResourceImportInvalidID(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + ledConfig(true),
				ResourceName:  ledAddress,
				ImportState:   true,
				ImportStateId: "leds",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestLEDResourceDrift(t *testing.T) {
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + ledConfig(true)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { srv.LED.SetConfig(glinettest.LEDConfig{Enabled: false}) },
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(ledAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLEDState(true),
				Check:             expectRouterLED(testClient(t, srv.URL, srv.Username, srv.Password), true),
			},
		},
	})
}

func TestLEDResourceInvalidConfig(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + `resource "glinet_led" "test" {}`,
				ExpectError: regexp.MustCompile(`The argument "enabled" is required`),
			},
		},
	})
}

func TestLEDResourceRouterErrors(t *testing.T) {
	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "led", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ledConfig(false),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LED Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + ledConfig(false),
					ConfigPlanChecks:  expectResourceAction(ledAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectLEDState(false),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("led", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + ledConfig(false),
				},
				{
					PreConfig:   failRouterMethod(srv, "led", "get_config"),
					Config:      fakeRouterConfig(srv) + ledConfig(false),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LED Configuration.*Method not found`),
				},
				{
					PreConfig:         func() { srv.Handle("led", "get_config", get) },
					Config:            fakeRouterConfig(srv) + ledConfig(false),
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: expectLEDState(false),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "led", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ledConfig(false),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set LED Configuration.*Method not found`),
				},
			},
		})

		if got := srv.LED.Config(); !got.Enabled {
			t.Errorf("router LED configuration is %+v, want it unchanged", got)
		}
	})
}
