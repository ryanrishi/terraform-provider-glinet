package provider

import (
	"encoding/json"
	"fmt"
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
// unrelatedBlockMAC, and fails the test on each write that is not to the allow list.
// Like the tested router, the fake router writes to the allow list also in block-list mode and keeps the mode.
func allowListRouter(t *testing.T, macs ...string) *glinettest.Server {
	srv := glinettest.NewServer(t)
	srv.AccessControl.SetConfig(glinettest.AccessControlConfig{
		Mode:     glinet.AccessControlAllowList,
		BlackMAC: []string{unrelatedBlockMAC},
		WhiteMAC: macs,
	})
	setSingleMAC := srv.Handler("black_white_list", "set_single_mac")
	srv.Handle("black_white_list", "set_single_mac", func(params json.RawMessage) (any, error) {
		var p struct{ Mode string }
		if err := json.Unmarshal(params, &p); err != nil || p.Mode != glinet.AccessControlAllowList {
			t.Errorf("black_white_list.set_single_mac %s, want mode %q", params, glinet.AccessControlAllowList)
			return nil, glinettest.ErrInvalidParams
		}
		return setSingleMAC(params)
	})
	srv.Handle("black_white_list", "set_config", func(params json.RawMessage) (any, error) {
		t.Errorf("black_white_list.set_config %s", params)
		return nil, glinettest.ErrMethodNotFound
	})
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

// TestClientAllowResourceBlockListMode creates, replaces, refreshes and destroys entries while the router is in block-list mode.
// The mode stays, and destroy also removes the last entry.
func TestClientAllowResourceBlockListMode(t *testing.T) {
	parallel(t)
	srv := allowListRouter(t)
	setAccessMode(srv, glinet.AccessControlBlockList)
	calls := recordCalls(t, srv, "black_white_list", "set_single_mac")
	expectBlockListMode := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if mode := srv.AccessControl.Config().Mode; mode != glinet.AccessControlBlockList {
				t.Errorf("mode is %q, want %q", mode, glinet.AccessControlBlockList)
			}
			expectAllowList(t, srv, want...)
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + allowConfig(allowMAC),
				ConfigStateChecks: expectAllowMAC(allowMAC),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"mode":"white","operate":"add","mac":"`+allowMACUpper+`"}`),
					expectBlockListMode(allowMACUpper),
				),
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
					expectBlockListMode(otherAllowMACUp),
				),
			},
			{
				PreConfig: func() {
					c := srv.AccessControl.Config()
					c.WhiteMAC = nil
					srv.AccessControl.SetConfig(c)
				},
				Config:           fakeRouterConfig(srv) + allowConfig(otherAllowMAC),
				ConfigPlanChecks: expectResourceAction(allowAddress, plancheck.ResourceActionCreate),
				Check:            expectBlockListMode(otherAllowMACUp),
			},
		},
	})

	if mode := srv.AccessControl.Config().Mode; mode != glinet.AccessControlBlockList {
		t.Errorf("mode is %q, want %q", mode, glinet.AccessControlBlockList)
	}
	expectAllowList(t, srv)
}

// TestClientAllowResourceThenAllowListMode adds the allow-list entries in block-list mode and then starts allow-list mode
// with glinet_access_control, as the example of glinet_client_allow does.
func TestClientAllowResourceThenAllowListMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.AccessControl.SetConfig(glinettest.AccessControlConfig{Mode: glinet.AccessControlBlockList, BlackMAC: []string{unrelatedBlockMAC}})
	setConfigCalls := recordCalls(t, srv, "black_white_list", "set_config")
	config := fakeRouterConfig(srv) + fmt.Sprintf(`
resource "glinet_client_allow" "test" {
  mac = %q
}

resource "glinet_access_control" "test" {
  mode = "allow_list"

  depends_on = [glinet_client_allow.test]
}
`, allowMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: expectAccessControlMode("allow_list"),
				Check: resource.ComposeTestCheckFunc(
					setConfigCalls.expect(`{"mode":"white","mac":["`+allowMACUpper+`"]}`),
					func(*terraform.State) error {
						expectAllowList(t, srv, allowMACUpper)
						return nil
					},
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Last Allow-List Entry`),
			},
			{
				PreConfig: func() {
					setAccessMode(srv, glinet.AccessControlBlockList)
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})

	expectAllowList(t, srv)
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
