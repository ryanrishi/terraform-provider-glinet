package provider

import (
	"encoding/json"
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

const switchButtonAddress = "glinet_switch_button.test"

func switchButtonConfig(function, subFunction string) string {
	sub := ""
	if subFunction != "" {
		sub = fmt.Sprintf("  sub_function = %q\n", subFunction)
	}
	return fmt.Sprintf(`
resource "glinet_switch_button" "test" {
  function = %q
%s}
`, function, sub)
}

func expectSwitchButtonState(function, subFunction string) []statecheck.StateCheck {
	var sub knownvalue.Check = knownvalue.Null()
	if subFunction != "" {
		sub = knownvalue.StringExact(subFunction)
	}
	return expectAttributes(switchButtonAddress, map[string]knownvalue.Check{
		"id":           knownvalue.StringExact("switch_button"),
		"function":     knownvalue.StringExact(function),
		"sub_function": sub,
	})
}

func expectRouterSwitchButton(srv *glinettest.Server, want glinettest.SwitchButtonConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.SwitchButton.Config(); got != want {
			return fmt.Errorf("router switch configuration is %+v, want %+v", got, want)
		}
		return nil
	}
}

func TestSwitchButtonResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "switch-button", "set_config")

	steps := []struct {
		function, subFunction string
		write                 string
	}{
		{"wifi", "guest_wifi", `{"func":"wifi","sub_func":"guest_wifi","sync":false}`},
		{"wifi", "main_wifi", `{"func":"wifi","sub_func":"main_wifi","sync":false}`},
		{"led", "", `{"func":"led","sync":false}`},
		{"vpn", "1", `{"func":"vpn","sub_func":"1","sync":false}`},
		{"none", "", `{"func":"none","sync":false}`},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + switchButtonConfig(s.function, s.subFunction),
			ConfigStateChecks: expectSwitchButtonState(s.function, s.subFunction),
			Check: resource.ComposeTestCheckFunc(
				expectRouterSwitchButton(srv, glinettest.SwitchButtonConfig{Func: s.function, SubFunc: s.subFunction}),
				writes.expect(s.write),
			),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(switchButtonAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, step)
	}
	last := testSteps[len(testSteps)-1].Config
	testSteps = append(testSteps,
		resource.TestStep{Config: last, ConfigPlanChecks: expectEmptyPlan},
		resource.TestStep{
			ResourceName:      switchButtonAddress,
			ImportState:       true,
			ImportStateId:     "switch_button",
			ImportStateVerify: true,
		},
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterSwitchButton(srv, glinettest.SwitchButtonConfig{Func: "none"}),
		Steps:                    testSteps,
	})
}

// TestSwitchButtonResourceFirmware470 uses the get_funcs result of firmware 4.7.0, which has strings.
func TestSwitchButtonResourceFirmware470(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.SwitchButton.SetFuncs(json.RawMessage(`{"funcs":["openvpn","wireguard","led"]}`))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + switchButtonConfig("wireguard", ""),
				ConfigStateChecks: expectSwitchButtonState("wireguard", ""),
				Check:             expectRouterSwitchButton(srv, glinettest.SwitchButtonConfig{Func: "wireguard"}),
			},
		},
	})
}

func TestSwitchButtonResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + switchButtonConfig("wifi", "main_wifi"),
			},
			{
				PreConfig:         func() { srv.SwitchButton.SetConfig(glinettest.SwitchButtonConfig{Func: "wifi", SubFunc: "guest_wifi"}) },
				Config:            fakeRouterConfig(srv) + switchButtonConfig("wifi", "main_wifi"),
				ConfigPlanChecks:  expectResourceAction(switchButtonAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSwitchButtonState("wifi", "main_wifi"),
				Check:             expectRouterSwitchButton(srv, glinettest.SwitchButtonConfig{Func: "wifi", SubFunc: "main_wifi"}),
			},
			{
				PreConfig:         func() { srv.SwitchButton.SetConfig(glinettest.SwitchButtonConfig{Func: "led"}) },
				Config:            fakeRouterConfig(srv) + switchButtonConfig("wifi", "main_wifi"),
				ConfigPlanChecks:  expectResourceAction(switchButtonAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSwitchButtonState("wifi", "main_wifi"),
				Check:             expectRouterSwitchButton(srv, glinettest.SwitchButtonConfig{Func: "wifi", SubFunc: "main_wifi"}),
			},
		},
	})
}

func TestSwitchButtonResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + switchButtonConfig("led", ""),
				ResourceName:  switchButtonAddress,
				ImportState:   true,
				ImportStateId: "switch",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestSwitchButtonResourceNotAvailable(t *testing.T) {
	parallel(t)
	tests := []struct {
		name                  string
		function, subFunction string
		error                 string
	}{
		{"unknown function", "openvpn", "",
			`Function "openvpn" is not available. The available functions are "none", "adguardhome", "led", "repeater", "tor", "wifi", "vpn" ("none" and the functions of switch-button.get_funcs).`},
		{"no sub-function", "wifi", "",
			`Function "wifi" needs sub_function. The sub-functions of "wifi" in switch-button.get_funcs are: "main_wifi", "guest_wifi".`},
		{"unknown sub-function", "vpn", "2",
			`Sub-function "2" is not available for function "vpn". The sub-functions of "vpn" in switch-button.get_funcs are: "1".`},
		{"sub-function of a function without", "led", "main_wifi",
			`Function "led" has no sub-functions in switch-button.get_funcs, so sub_function cannot be set.`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			writes := recordCalls(t, srv, "switch-button", "set_config")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + switchButtonConfig(tt.function, tt.subFunction),
						ExpectError: regexp.MustCompile(`(?s)Switch Function Not Available.*` + wrapped(tt.error+" The provider did not change the router.")),
					},
				},
			})

			if err := writes.expect()(nil); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestSwitchButtonResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "switch-button", "set_config")

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"sub-function of none", switchButtonConfig("none", "main_wifi"), `sub_function cannot be set when function is none`},
		{"empty function", switchButtonConfig("", ""), `Attribute function string length must be at least 1`},
		{"empty sub-function", `
resource "glinet_switch_button" "test" {
  function     = "wifi"
  sub_function = ""
}
`, `Attribute sub_function string length must be at least 1`},
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

	if err := writes.expect()(nil); err != nil {
		t.Error(err)
	}
}

func TestSwitchButtonResourceNoSwitch(t *testing.T) {
	parallel(t)
	t.Run("no switch", func(t *testing.T) {
		expectNoHardware(t, glinettest.NewServer(t), "switch_button", "", switchButtonConfig("led", ""), "switch-button", "set_config")
	})
	t.Run("system info error", func(t *testing.T) {
		expectSystemInfoError(t, glinettest.NewServer(t), switchButtonConfig("led", ""), "switch-button", "set_config")
	})
}

func TestSwitchButtonResourceRouterErrors(t *testing.T) {
	parallel(t)

	t.Run("get_funcs", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "switch-button", "get_funcs")()
		writes := recordCalls(t, srv, "switch-button", "set_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + switchButtonConfig("led", ""),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Switch Functions.*Method not found`),
				},
			},
		})

		if err := writes.expect()(nil); err != nil {
			t.Error(err)
		}
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "switch-button", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + switchButtonConfig("led", ""),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Switch Function.*Method not found`),
				},
			},
		})
	})

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "switch-button", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + switchButtonConfig("led", ""),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Switch Function.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + switchButtonConfig("led", ""),
					ConfigPlanChecks:  expectResourceAction(switchButtonAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectSwitchButtonState("led", ""),
				},
			},
		})
	})
}
