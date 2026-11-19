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

// The tests of glinet_igmp_snooping always use the fake router: its write was not tested on a router.

const igmpSnoopingAddress = "glinet_igmp_snooping.test"

func igmpSnoopingConfig(attributes string) string {
	return `
resource "glinet_igmp_snooping" "test" {
` + attributes + `}
`
}

func expectIGMPSnoopingState(enabled bool, version int64) []statecheck.StateCheck {
	return expectAttributes(igmpSnoopingAddress, map[string]knownvalue.Check{
		"id":      knownvalue.StringExact("igmp_snooping"),
		"enabled": knownvalue.Bool(enabled),
		"version": knownvalue.Int64Exact(version),
	})
}

func expectIGMPConfig(srv *glinettest.Server, want glinettest.IGMPConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.IGMP.Config(); got != want {
			return fmt.Errorf("IGMP configuration is %+v, want %+v", got, want)
		}
		return nil
	}
}

func TestIGMPSnoopingResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "igmp", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectIGMPConfig(srv, glinettest.IGMPConfig{Enabled: true, Version: 2}),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n"),
				ConfigStateChecks: expectIGMPSnoopingState(true, 3),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"enable":true,"version":3}`),
					expectIGMPConfig(srv, glinettest.IGMPConfig{Enabled: true, Version: 3}),
				),
			},
			{
				ResourceName:      igmpSnoopingAddress,
				ImportState:       true,
				ImportStateId:     "igmp_snooping",
				ImportStateVerify: true,
			},
			{
				Config:            fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n  version = 2\n"),
				ConfigPlanChecks:  expectResourceAction(igmpSnoopingAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectIGMPSnoopingState(true, 2),
				Check:             calls.expect(`{"enable":true,"version":2}`),
			},
			{
				Config:           fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n  version = 2\n"),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestIGMPSnoopingResourceRouterVersion changes the version on the router before a write: the write sends back the
// router version, because version is not in the configuration.
func TestIGMPSnoopingResourceRouterVersion(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "igmp", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n"),
				Check:  calls.expect(`{"enable":true,"version":3}`),
			},
			{
				PreConfig:         func() { srv.IGMP.SetConfig(glinettest.IGMPConfig{Enabled: false, Version: 1}) },
				Config:            fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n"),
				ConfigPlanChecks:  expectResourceAction(igmpSnoopingAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectIGMPSnoopingState(true, 1),
				Check:             calls.expect(`{"enable":true,"version":1}`),
			},
		},
	})
}

func TestIGMPSnoopingResourceInvalidValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n  version = 4\n"),
				ExpectError: regexp.MustCompile(`version value must be one of`),
			},
			{
				Config:        fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n"),
				ResourceName:  igmpSnoopingAddress,
				ImportState:   true,
				ImportStateId: "other",
				ExpectError:   regexp.MustCompile(`(?s)Invalid Import ID.*must be "igmp_snooping"`),
			},
		},
	})
}

func TestIGMPSnoopingResourceRouterErrors(t *testing.T) {
	parallel(t)

	for _, tt := range []struct{ method, err string }{
		{"get_config", `(?s)Unable to Read IGMP Snooping Configuration.*Method not found`},
		{"set_config", `(?s)Unable to Set IGMP Snooping Configuration.*Method not found`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			failRouterMethod(srv, "igmp", tt.method)()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + igmpSnoopingConfig("  enabled = true\n"),
						ExpectError: regexp.MustCompile(tt.err),
					},
				},
			})
			if got, want := srv.IGMP.Config(), (glinettest.IGMPConfig{Version: 3}); got != want {
				t.Errorf("IGMP configuration is %+v, want %+v", got, want)
			}
		})
	}
}
