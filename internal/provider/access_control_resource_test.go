package provider

import (
	"fmt"
	"reflect"
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

// The tests of glinet_access_control always use the fake router: allow-list mode can cut the access of all clients.

const accessControlAddress = "glinet_access_control.test"

func accessControlConfig(mode string) string {
	return fmt.Sprintf(`
resource "glinet_access_control" "test" {
  mode = %q
}
`, mode)
}

func expectAccessControlMode(mode string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(accessControlAddress, tfjsonpath.New("id"), knownvalue.StringExact("access_control")),
		statecheck.ExpectKnownValue(accessControlAddress, tfjsonpath.New("mode"), knownvalue.StringExact(mode)),
	}
}

func expectAccessControl(srv *glinettest.Server, want glinettest.AccessControlConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.AccessControl.Config(); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("access control configuration is %+v, want %+v", got, want)
		}
		return nil
	}
}

// TestAccessControlResource changes the mode in both directions. Each change sends the list of the new mode
// as the router has it, and the list of the other mode stays.
func TestAccessControlResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	initial := glinettest.AccessControlConfig{
		Mode:     glinet.AccessControlBlockList,
		BlackMAC: []string{unrelatedBlockMAC, blockMAC},
		WhiteMAC: []string{unrelatedAllowMAC, allowMAC},
	}
	srv.AccessControl.SetConfig(initial)
	allowListMode := initial
	allowListMode.Mode = glinet.AccessControlAllowList
	calls := recordCalls(t, srv, "black_white_list", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectAccessControl(srv, initial),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + accessControlConfig("block_list"),
				ConfigStateChecks: expectAccessControlMode("block_list"),
				Check:             resource.ComposeTestCheckFunc(calls.expect(), expectAccessControl(srv, initial)),
			},
			{
				ResourceName:      accessControlAddress,
				ImportState:       true,
				ImportStateId:     "access_control",
				ImportStateVerify: true,
			},
			{
				Config:            fakeRouterConfig(srv) + accessControlConfig("allow_list"),
				ConfigPlanChecks:  expectResourceAction(accessControlAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectAccessControlMode("allow_list"),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"mode":"white","mac":["`+unrelatedAllowMAC+`","`+allowMAC+`"]}`),
					expectAccessControl(srv, allowListMode),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + accessControlConfig("allow_list"),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + accessControlConfig("block_list"),
				ConfigPlanChecks:  expectResourceAction(accessControlAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectAccessControlMode("block_list"),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"mode":"black","mac":["`+unrelatedBlockMAC+`","`+blockMAC+`"]}`),
					expectAccessControl(srv, initial),
				),
			},
		},
	})
}

// TestAccessControlResourceCreateChangesMode changes the mode on create, and destroy leaves the mode on the router.
func TestAccessControlResourceCreateChangesMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.AccessControl.SetConfig(glinettest.AccessControlConfig{Mode: glinet.AccessControlAllowList, WhiteMAC: []string{allowMACUpper}})
	calls := recordCalls(t, srv, "black_white_list", "set_config")
	want := glinettest.AccessControlConfig{Mode: glinet.AccessControlBlockList, WhiteMAC: []string{allowMACUpper}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectAccessControl(srv, want),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + accessControlConfig("block_list"),
				ConfigStateChecks: expectAccessControlMode("block_list"),
				Check:             resource.ComposeTestCheckFunc(calls.expect(`{"mode":"black","mac":[]}`), expectAccessControl(srv, want)),
			},
		},
	})
}

func TestAccessControlResourceEmptyAllowList(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	initial := glinettest.AccessControlConfig{Mode: glinet.AccessControlBlockList, BlackMAC: []string{unrelatedBlockMAC}}
	srv.AccessControl.SetConfig(initial)
	calls := recordCalls(t, srv, "black_white_list", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + accessControlConfig("allow_list"),
				ExpectError: regexp.MustCompile(`Allow List Is Empty`),
			},
		},
	})

	if params := calls.take(); len(params) != 0 {
		t.Errorf("set_config calls %v, want none", params)
	}
	if got := srv.AccessControl.Config(); !reflect.DeepEqual(got, initial) {
		t.Errorf("access control configuration is %+v, want %+v", got, initial)
	}
}

func TestAccessControlResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + accessControlConfig("block_list")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					srv.AccessControl.SetConfig(glinettest.AccessControlConfig{Mode: glinet.AccessControlAllowList, WhiteMAC: []string{allowMACUpper}})
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(accessControlAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectAccessControlMode("block_list"),
			},
		},
	})
}

func TestAccessControlResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + accessControlConfig(glinet.AccessControlAllowList),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:        fakeRouterConfig(srv) + accessControlConfig("block_list"),
				ResourceName:  accessControlAddress,
				ImportState:   true,
				ImportStateId: "black",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccessControlResourceRouterErrors(t *testing.T) {
	parallel(t)
	tests := []struct {
		name   string
		method string
		error  string
	}{
		{"read", "get_config", `Unable to Read Access Control Configuration`},
		{"write", "set_config", `Unable to Set Access Control Mode`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.AccessControl.SetConfig(glinettest.AccessControlConfig{Mode: glinet.AccessControlBlockList, WhiteMAC: []string{allowMACUpper}})
			expectReadError(t, srv, accessControlConfig("allow_list"), "black_white_list", tt.method, tt.error)
		})
	}
}
