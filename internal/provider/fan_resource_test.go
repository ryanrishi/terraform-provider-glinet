package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const fanAddress = "glinet_fan.test"

func fanConfig(temperature int) string {
	return fmt.Sprintf(`
resource "glinet_fan" "test" {
  temperature = %d
}
`, temperature)
}

func expectFanState(temperature, warnTemperature int) []statecheck.StateCheck {
	return expectAttributes(fanAddress, map[string]knownvalue.Check{
		"id":               knownvalue.StringExact("fan"),
		"temperature":      knownvalue.Int64Exact(int64(temperature)),
		"warn_temperature": knownvalue.Int64Exact(int64(warnTemperature)),
	})
}

func expectRouterFan(srv *glinettest.Server, want glinettest.FanConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Fan.Config(); got != want {
			return fmt.Errorf("router fan configuration is %+v, want %+v", got, want)
		}
		return nil
	}
}

func TestFanResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "fan", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterFan(srv, glinettest.FanConfig{Temperature: 60, WarnTemperature: 75}),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + fanConfig(85),
				ConfigStateChecks: expectFanState(85, 75),
				Check: resource.ComposeTestCheckFunc(
					expectRouterFan(srv, glinettest.FanConfig{Temperature: 85, WarnTemperature: 75}),
					writes.expect(`{"temperature":85}`),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + fanConfig(60),
				ConfigPlanChecks:  expectResourceAction(fanAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectFanState(60, 75),
				Check: resource.ComposeTestCheckFunc(
					expectRouterFan(srv, glinettest.FanConfig{Temperature: 60, WarnTemperature: 75}),
					writes.expect(`{"temperature":60}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + fanConfig(60),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				ResourceName:      fanAddress,
				ImportState:       true,
				ImportStateId:     "fan",
				ImportStateVerify: true,
			},
		},
	})
}

func TestFanResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fanConfig(80),
			},
			{
				PreConfig:         func() { srv.Fan.SetConfig(glinettest.FanConfig{Temperature: 70, WarnTemperature: 72}) },
				Config:            fakeRouterConfig(srv) + fanConfig(80),
				ConfigPlanChecks:  expectResourceAction(fanAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectFanState(80, 72),
				Check:             expectRouterFan(srv, glinettest.FanConfig{Temperature: 80, WarnTemperature: 72}),
			},
		},
	})
}

func TestFanResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + fanConfig(80),
				ResourceName:  fanAddress,
				ImportState:   true,
				ImportStateId: "fan_status",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestFanResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "fan", "set_config")

	for _, temperature := range []int{-1, 91} {
		t.Run(fmt.Sprint(temperature), func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + fanConfig(temperature),
						ExpectError: regexp.MustCompile(fmt.Sprintf(`temperature value must be between 0 and 90, got: %d`, temperature)),
					},
				},
			})
		})
	}

	if err := writes.expect()(nil); err != nil {
		t.Error(err)
	}
}

func TestFanResourceNoFan(t *testing.T) {
	parallel(t)
	t.Run("no fan", func(t *testing.T) {
		expectNoHardware(t, glinettest.NewServer(t), "fan", false, fanConfig(80), "fan", "set_config")
	})
	t.Run("system info error", func(t *testing.T) {
		expectSystemInfoError(t, glinettest.NewServer(t), fanConfig(80), "fan", "set_config")
	})
}

func TestFanResourceRouterErrors(t *testing.T) {
	parallel(t)

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "fan", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + fanConfig(80),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Fan Configuration.*Method not found`),
				},
			},
		})
	})

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "fan", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + fanConfig(80),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Fan Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + fanConfig(80),
					ConfigPlanChecks:  expectResourceAction(fanAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectFanState(80, 75),
				},
			},
		})
	})
}
