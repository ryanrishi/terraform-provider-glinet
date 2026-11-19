package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
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

// The tests of glinet_client_allow always use the fake router: allow-list mode can cut the access of all clients.

const (
	allowAddress      = "glinet_client_allow.test"
	allowMAC          = "02:00:00:00:00:c1"
	allowMACUpper     = "02:00:00:00:00:C1"
	otherAllowMAC     = "02:00:00:00:00:c2"
	otherAllowMACUp   = "02:00:00:00:00:C2"
	unrelatedAllowMAC = "02:00:00:00:00:D9"
)

func allowConfig(mac string) string {
	return fmt.Sprintf(`
resource "glinet_client_allow" "test" {
  mac = %q
}
`, mac)
}

func expectAllowMAC(mac string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(allowAddress, tfjsonpath.New("mac"), knownvalue.StringExact(mac)),
	}
}

// allowListRouter returns a fake router in allow-list mode with the allow list macs and the block list
// unrelatedBlockMAC, and fails the test on each write in block-list mode.
func allowListRouter(t *testing.T, macs ...string) *glinettest.Server {
	srv := glinettest.NewServer(t)
	srv.AccessControl.SetConfig(glinettest.AccessControlConfig{
		Mode:     glinet.AccessControlAllowList,
		BlackMAC: []string{unrelatedBlockMAC},
		WhiteMAC: macs,
	})
	for _, method := range []string{"set_single_mac", "set_config"} {
		next := srv.Handler("black_white_list", method)
		srv.Handle("black_white_list", method, func(params json.RawMessage) (any, error) {
			if mode := srv.AccessControl.Config().Mode; mode != glinet.AccessControlAllowList {
				t.Errorf("black_white_list.%s %s in mode %q", method, params, mode)
				return nil, glinettest.ErrMethodNotFound
			}
			return next(params)
		})
	}
	return srv
}

func setAccessMode(srv *glinettest.Server, mode string) {
	config := srv.AccessControl.Config()
	config.Mode = mode
	srv.AccessControl.SetConfig(config)
}

func expectAllowList(t *testing.T, srv *glinettest.Server, want ...string) {
	t.Helper()
	config := srv.AccessControl.Config()
	if !slices.Equal(config.WhiteMAC, want) {
		t.Errorf("allow list is %v, want %v", config.WhiteMAC, want)
	}
	if !slices.Equal(config.BlackMAC, []string{unrelatedBlockMAC}) {
		t.Errorf("block list is %v, want %v", config.BlackMAC, []string{unrelatedBlockMAC})
	}
}

func TestClientAllowResource(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t, unrelatedAllowMAC)
	calls := recordCalls(t, srv, "black_white_list", "set_single_mac")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + allowConfig(allowMAC),
				ConfigStateChecks: expectAllowMAC(allowMAC),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"mode":"white","operate":"add","mac":"`+allowMACUpper+`"}`),
					func(*terraform.State) error {
						expectAllowList(t, srv, unrelatedAllowMAC, allowMACUpper)
						return nil
					},
				),
			},
			{
				ResourceName:                         allowAddress,
				ImportState:                          true,
				ImportStateId:                        allowMACUpper,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "mac",
			},
			{
				Config:           fakeRouterConfig(srv) + allowConfig(allowMAC),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + allowConfig(otherAllowMAC),
				ConfigPlanChecks:  expectResourceAction(allowAddress, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectAllowMAC(otherAllowMAC),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(
						`{"mode":"white","operate":"del","mac":"`+allowMACUpper+`"}`,
						`{"mode":"white","operate":"add","mac":"`+otherAllowMACUp+`"}`,
					),
					func(*terraform.State) error {
						expectAllowList(t, srv, unrelatedAllowMAC, otherAllowMACUp)
						return nil
					},
				),
			},
		},
	})

	expectAllowList(t, srv, unrelatedAllowMAC)
}

func TestClientAllowResourceDrift(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t, unrelatedAllowMAC)
	config := fakeRouterConfig(srv) + allowConfig(allowMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					c := srv.AccessControl.Config()
					c.WhiteMAC = []string{unrelatedAllowMAC}
					srv.AccessControl.SetConfig(c)
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(allowAddress, plancheck.ResourceActionCreate),
				Check: func(*terraform.State) error {
					expectAllowList(t, srv, unrelatedAllowMAC, allowMACUpper)
					return nil
				},
			},
		},
	})

	expectAllowList(t, srv, unrelatedAllowMAC)
}

// TestClientAllowResourceMixedCase has the MAC address in two letter cases on the router.
// Create fails, import finds both entries, and destroy removes both.
func TestClientAllowResourceMixedCase(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t, allowMAC, unrelatedAllowMAC, allowMACUpper)
	config := fakeRouterConfig(srv) + allowConfig(allowMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Client Allow Already Exists.*allow list.*` + allowMAC + `,\s+` + allowMACUpper + `.*Import it with the ID\s+"` + allowMAC + `"`),
			},
			{
				Config:             config,
				ResourceName:       allowAddress,
				ImportState:        true,
				ImportStateId:      allowMACUpper,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectAllowList(t, srv, unrelatedAllowMAC)
}

func TestClientAllowResourceBlockListModeCreate(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t, unrelatedAllowMAC)
	setAccessMode(srv, glinet.AccessControlBlockList)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + allowConfig(allowMAC),
				ExpectError: regexp.MustCompile(`(?s)Router Is Not in Allow-List Mode.*"block_list",\s+not\s+"allow_list".*"allow_list"\s+first`),
			},
		},
	})

	want := glinettest.AccessControlConfig{Mode: glinet.AccessControlBlockList, BlackMAC: []string{unrelatedBlockMAC}, WhiteMAC: []string{unrelatedAllowMAC}}
	if got := srv.AccessControl.Config(); !reflect.DeepEqual(got, want) {
		t.Errorf("access control configuration is %+v, want %+v", got, want)
	}
}

// TestClientAllowResourceBlockListModeReadAndDelete keeps the resource in the state while the router is in block-list mode,
// and fails the destroy without a write until the router is in allow-list mode again.
func TestClientAllowResourceBlockListModeReadAndDelete(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t, unrelatedAllowMAC)
	config := fakeRouterConfig(srv) + allowConfig(allowMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					setAccessMode(srv, glinet.AccessControlBlockList)
				},
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectAllowMAC(allowMAC),
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Router Is Not in Allow-List Mode.*terraform\s+state\s+rm.*Do\s+not\s+set\s+allow-list\s+mode`),
			},
			{
				PreConfig: func() {
					setAccessMode(srv, glinet.AccessControlAllowList)
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectAllowList(t, srv, unrelatedAllowMAC)
}

// TestClientAllowResourceLastEntry fails the destroy of the last allow-list entry in allow-list mode without a write.
func TestClientAllowResourceLastEntry(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t)
	config := fakeRouterConfig(srv) + allowConfig(allowMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Last Allow-List Entry.*` + allowMAC + `.*Keep\s+another\s+entry.*terraform\s+state\s+rm`),
			},
			{
				PreConfig: func() {
					c := srv.AccessControl.Config()
					c.WhiteMAC = append(c.WhiteMAC, unrelatedAllowMAC)
					srv.AccessControl.SetConfig(c)
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectAllowList(t, srv, unrelatedAllowMAC)
}

func TestClientAllowResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + allowConfig(allowMAC),
				ResourceName:  allowAddress,
				ImportState:   true,
				ImportStateId: "02-00-00-00-00-c1",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestClientAllowResourceRouterErrors(t *testing.T) {
	parallel(t)
	tests := []struct {
		name   string
		method string
		error  string
	}{
		{"read", "get_config", `Unable to Read Allow List`},
		{"write", "set_single_mac", `Unable to Create Client Allow`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := allowListRouter(t, unrelatedAllowMAC)
			expectReadError(t, srv, allowConfig(allowMAC), "black_white_list", tt.method, tt.error)
			expectAllowList(t, srv, unrelatedAllowMAC)
		})
	}
}
