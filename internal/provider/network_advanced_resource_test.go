package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// The tests of glinet_network_advanced always use the fake router: its write was not tested on a router.

const networkAdvancedAddress = "glinet_network_advanced.test"

func networkAdvancedConfig(attributes string) string {
	return `
resource "glinet_network_advanced" "test" {
` + attributes + `}
`
}

func expectNetworkAdvancedState(fullConeNAT, sipALG bool) []statecheck.StateCheck {
	return expectAttributes(networkAdvancedAddress, map[string]knownvalue.Check{
		"id":            knownvalue.StringExact("network_advanced"),
		"full_cone_nat": knownvalue.Bool(fullConeNAT),
		"sip_alg":       knownvalue.Bool(sipALG),
	})
}

func TestNetworkAdvancedResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "network", "set_advance_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterJSON(t, srv.Network.AdvanceConfig, `{"nat_enable":0,"sip_enable":1}`),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + networkAdvancedConfig("  full_cone_nat = true\n  sip_alg = true\n"),
				ConfigStateChecks: expectNetworkAdvancedState(true, true),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"nat_enable":1,"sip_enable":1}`),
					expectRouterJSON(t, srv.Network.AdvanceConfig, `{"nat_enable":1,"sip_enable":1}`),
				),
			},
			{
				ResourceName:      networkAdvancedAddress,
				ImportState:       true,
				ImportStateId:     "network_advanced",
				ImportStateVerify: true,
			},
			{
				Config:            fakeRouterConfig(srv) + networkAdvancedConfig("  full_cone_nat = false\n  sip_alg = true\n"),
				ConfigPlanChecks:  expectResourceAction(networkAdvancedAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectNetworkAdvancedState(false, true),
				Check:             calls.expect(`{"nat_enable":0,"sip_enable":1}`),
			},
			{
				Config:           fakeRouterConfig(srv) + networkAdvancedConfig("  full_cone_nat = false\n  sip_alg = true\n"),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestNetworkAdvancedResourceRouterValue changes the router before each write: the write sends back the router value
// of the attribute that is not in the configuration.
func TestNetworkAdvancedResourceRouterValue(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Network.SetAdvanceConfig(json.RawMessage(`{"nat_enable":0,"sip_enable":1}`))
	calls := recordCalls(t, srv, "network", "set_advance_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + networkAdvancedConfig("  full_cone_nat = true\n"),
				ConfigStateChecks: expectNetworkAdvancedState(true, true),
				Check:             calls.expect(`{"nat_enable":1,"sip_enable":1}`),
			},
			{
				PreConfig:         func() { srv.Network.SetAdvanceConfig(json.RawMessage(`{"nat_enable":0,"sip_enable":0}`)) },
				Config:            fakeRouterConfig(srv) + networkAdvancedConfig("  full_cone_nat = true\n"),
				ConfigPlanChecks:  expectResourceAction(networkAdvancedAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectNetworkAdvancedState(true, false),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"nat_enable":1,"sip_enable":0}`),
					expectRouterJSON(t, srv.Network.AdvanceConfig, `{"nat_enable":1,"sip_enable":0}`),
				),
			},
		},
	})
}

func TestNetworkAdvancedResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + networkAdvancedConfig(""),
				ResourceName:  networkAdvancedAddress,
				ImportState:   true,
				ImportStateId: "other",
				ExpectError:   regexp.MustCompile(`(?s)Invalid Import ID.*must be "network_advanced"`),
			},
		},
	})
}

func TestNetworkAdvancedResourceRouterErrors(t *testing.T) {
	parallel(t)

	for _, tt := range []struct{ method, err string }{
		{"get_advance_config", `(?s)Unable to Read Advanced Network Settings.*Method not found`},
		{"set_advance_config", `(?s)Unable to Set Advanced Network Settings.*Method not found`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			failRouterMethod(srv, "network", tt.method)()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + networkAdvancedConfig("  sip_alg = true\n"),
						ExpectError: regexp.MustCompile(tt.err),
					},
				},
			})
			expectJSONEqual(t, srv.Network.AdvanceConfig(), `{"nat_enable":0,"sip_enable":0}`)
		})
	}
}
