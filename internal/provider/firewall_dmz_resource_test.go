package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

const (
	dmzAddress = "glinet_firewall_dmz.test"
	dmzIP      = "192.168.8.250"
	otherDMZIP = "192.168.8.251"
)

func dmzConfig(internalIP string, enabled bool) string {
	return fmt.Sprintf(`
resource "glinet_firewall_dmz" "test" {
  enabled     = %t
  internal_ip = %q
}
`, enabled, internalIP)
}

func expectDMZ(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(dmzAddress, tfjsonpath.New(attribute), value)
}

func expectDMZState(dmz glinet.DMZ) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		expectDMZ("id", knownvalue.StringExact("firewall_dmz")),
		expectDMZ("internal_ip", knownvalue.StringExact(dmz.DestIP)),
		expectDMZ("enabled", knownvalue.Bool(dmz.Enabled)),
	}
}

func expectDMZAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(dmzAddress, action),
		},
	}
}

// expectRouterDMZ checks that the DMZ configuration of the router is want.
func expectRouterDMZ(client *glinet.Client, want glinet.DMZ) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.Firewall.GetDMZ(context.Background())
		if err != nil {
			return err
		}
		if *got != want {
			return fmt.Errorf("router DMZ is %+v, want %+v", *got, want)
		}
		return nil
	}
}

// expectFakeDMZ checks that the DMZ configuration of srv is want.
func expectFakeDMZ(srv *glinettest.Server, want glinettest.DMZ) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Firewall.DMZ(); got != want {
			return fmt.Errorf("router DMZ is %+v, want %+v", got, want)
		}
		return nil
	}
}

// dmzTestRouter returns the provider block and a client for the router of the test.
// On the real router, the DMZ configuration goes back to its initial value when the test ends.
func dmzTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.Firewall.GetDMZ(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.Firewall.SetDMZ(ctx, *initial); err != nil {
			t.Fatalf("restore DMZ: %v", err)
		}

		restored, err := client.Firewall.GetDMZ(ctx)
		if err != nil {
			t.Fatalf("read restored DMZ: %v", err)
		}
		if *restored != *initial {
			t.Errorf("restored DMZ is %+v, want %+v", *restored, *initial)
		}
	})

	return `provider "glinet" {}`, client
}

func TestFirewallDMZResource(t *testing.T) {
	parallel(t)
	provider, client := dmzTestRouter(t)
	enabled := glinet.DMZ{Enabled: true, DestIP: dmzIP}
	disabled := glinet.DMZ{Enabled: false, DestIP: dmzIP}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterDMZ(client, glinet.DMZ{}),
		Steps: []resource.TestStep{
			{
				Config:            provider + dmzConfig(dmzIP, true),
				ConfigStateChecks: expectDMZState(enabled),
				Check:             expectRouterDMZ(client, enabled),
			},
			{
				Config:            provider + dmzConfig(dmzIP, false),
				ConfigPlanChecks:  expectDMZAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectDMZState(disabled),
				Check:             expectRouterDMZ(client, disabled),
			},
			{
				ResourceName:      dmzAddress,
				ImportState:       true,
				ImportStateId:     "firewall_dmz",
				ImportStateVerify: true,
			},
		},
	})
}

func TestFirewallDMZResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	var steps []resource.TestStep
	for i, dmz := range []glinettest.DMZ{
		{Enabled: true, DestIP: dmzIP},
		{Enabled: true, DestIP: otherDMZIP},
		{Enabled: false, DestIP: otherDMZIP},
		{Enabled: false, DestIP: dmzIP},
		{Enabled: true, DestIP: dmzIP},
	} {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + dmzConfig(dmz.DestIP, dmz.Enabled),
			ConfigStateChecks: expectDMZState(glinet.DMZ(dmz)),
			Check:             expectFakeDMZ(srv, dmz),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectDMZAction(plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeDMZ(srv, glinettest.DMZ{}),
		Steps:                    steps,
	})
}

func dmzPriorityConfig(enabled, higherPriority bool) string {
	return fmt.Sprintf(`
resource "glinet_firewall_dmz" "test" {
  enabled         = %t
  internal_ip     = %q
  higher_priority = %t
}
`, enabled, dmzIP, higherPriority)
}

// TestFirewallDMZResourcePriority sends back the priority of the router when higher_priority is not set.
func TestFirewallDMZResourcePriority(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "firewall", "set_dmz")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: func() { srv.Firewall.SetDMZ(glinettest.DMZ{DestIP: dmzIP, Priority: true}) },
				Config:    fakeRouterConfig(srv) + dmzConfig(otherDMZIP, true),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":true}`, otherDMZIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: otherDMZIP, Priority: true}),
				),
			},
			{
				PreConfig:         func() { srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: true, DestIP: otherDMZIP}) },
				Config:            fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(false))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":false}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP}),
				),
			},
		},
	})
}

func TestFirewallDMZResourceHigherPriority(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "firewall", "set_dmz")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeDMZ(srv, glinettest.DMZ{}),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + dmzPriorityConfig(true, true),
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(true))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":true}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP, Priority: true}),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + dmzPriorityConfig(true, false),
				ConfigPlanChecks:  expectDMZAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(false))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":false}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP}),
				),
			},
			{
				PreConfig:         func() { srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: true, DestIP: dmzIP, Priority: true}) },
				Config:            fakeRouterConfig(srv) + dmzPriorityConfig(true, false),
				ConfigPlanChecks:  expectDMZAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(false))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":false}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP}),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// TestFirewallDMZResourceHigherPriorityDisabled keeps higher_priority of the state while the DMZ is disabled,
// because a disabled write does not send priority.
func TestFirewallDMZResourceHigherPriorityDisabled(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "firewall", "set_dmz")
	config := fakeRouterConfig(srv) + dmzPriorityConfig(false, true)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(true))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":false,"dest_ip":%q}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: false, DestIP: dmzIP}),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				Config:            fakeRouterConfig(srv) + dmzPriorityConfig(true, true),
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(true))},
				Check: resource.ComposeTestCheckFunc(
					calls.expect(fmt.Sprintf(`{"enabled":true,"dest_ip":%q,"priority":true}`, dmzIP)),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP, Priority: true}),
				),
			},
		},
	})
}

func TestFirewallDMZResourceHigherPriorityImport(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: true, DestIP: dmzIP, Priority: true})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				ResourceName:       dmzAddress,
				ImportState:        true,
				ImportStateId:      "firewall_dmz",
				ImportStatePersist: true,
			},
			{
				Config:            fakeRouterConfig(srv) + dmzPriorityConfig(true, true),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: []statecheck.StateCheck{expectDMZ("higher_priority", knownvalue.Bool(true))},
			},
		},
	})
}

// TestFirewallDMZResourceHigherPriorityNotApplied uses a router that does not keep priority, as firmware 4.7.0 may do.
func TestFirewallDMZResourceHigherPriorityNotApplied(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("firewall", "set_dmz", func(params json.RawMessage) (any, error) {
		var dmz glinettest.DMZ
		if err := json.Unmarshal(params, &dmz); err != nil {
			return nil, glinettest.ErrInvalidParams
		}
		dmz.Priority = false
		srv.Firewall.SetDMZ(dmz)
		return nil, nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + dmzPriorityConfig(true, true),
				ExpectError: regexp.MustCompile(`(?s)Firewall DMZ Priority Not Applied.*priority false after the write of true`),
			},
		},
	})
}

func TestFirewallDMZResourceImport(t *testing.T) {
	parallel(t)
	for _, existing := range []glinettest.DMZ{
		{Enabled: true, DestIP: dmzIP},
		{Enabled: false, DestIP: dmzIP},
	} {
		t.Run(fmt.Sprintf("enabled %t", existing.Enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetDMZ(existing)
			config := fakeRouterConfig(srv) + dmzConfig(existing.DestIP, existing.Enabled)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeDMZ(srv, glinettest.DMZ{}),
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       dmzAddress,
						ImportState:        true,
						ImportStateId:      "firewall_dmz",
						ImportStatePersist: true,
					},
					{
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						ConfigStateChecks: expectDMZState(glinet.DMZ(existing)),
					},
				},
			})
		})
	}
}

func TestFirewallDMZResourceImportNoHost(t *testing.T) {
	parallel(t)
	tests := []struct {
		name  string
		setup func(*glinettest.Server)
	}{
		{"never set", func(*glinettest.Server) {}},
		{"disabled", func(srv *glinettest.Server) { srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: false}) }},
		{"enabled", func(srv *glinettest.Server) { srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: true}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			tt.setup(srv)
			before := srv.Firewall.DMZ()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
						ResourceName:  dmzAddress,
						ImportState:   true,
						ImportStateId: "firewall_dmz",
						ExpectError:   regexp.MustCompile(`(?s)Firewall DMZ Host Not Found.*The router has no DMZ host`),
					},
				},
			})

			if got := srv.Firewall.DMZ(); got != before {
				t.Errorf("router DMZ is %+v, want %+v", got, before)
			}
		})
	}
}

func TestFirewallDMZResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetDMZ(glinettest.DMZ{Enabled: true, DestIP: dmzIP})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				ResourceName:  dmzAddress,
				ImportState:   true,
				ImportStateId: dmzIP,
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestFirewallDMZResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	want := glinettest.DMZ{Enabled: true, DestIP: dmzIP}
	config := fakeRouterConfig(srv) + dmzConfig(dmzIP, true)

	tests := []struct {
		name   string
		router glinettest.DMZ
		action plancheck.ResourceActionType
	}{
		{"IP changed", glinettest.DMZ{Enabled: true, DestIP: otherDMZIP}, plancheck.ResourceActionUpdate},
		{"disabled", glinettest.DMZ{Enabled: false, DestIP: dmzIP}, plancheck.ResourceActionUpdate},
		{"IP removed", glinettest.DMZ{Enabled: false}, plancheck.ResourceActionCreate},
		{"IP removed, enabled", glinettest.DMZ{Enabled: true}, plancheck.ResourceActionCreate},
	}

	steps := []resource.TestStep{{Config: config}}
	for _, tt := range tests {
		steps = append(steps, resource.TestStep{
			PreConfig:         func() { srv.Firewall.SetDMZ(tt.router) },
			Config:            config,
			ConfigPlanChecks:  expectDMZAction(tt.action),
			ConfigStateChecks: expectDMZState(glinet.DMZ(want)),
			Check:             expectFakeDMZ(srv, want),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeDMZ(srv, glinettest.DMZ{}),
		Steps:                    steps,
	})
}

func TestFirewallDMZResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, ip := range []string{"", "192.168.8.256", "192.168.8", "fd00::1", "host", " 192.168.8.250"} {
		t.Run(ip, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + dmzConfig(ip, true),
						ExpectError: regexp.MustCompile(`internal_ip must be an IPv4 address`),
					},
				},
			})
		})
	}

	if got := srv.Firewall.DMZ(); got != (glinettest.DMZ{}) {
		t.Errorf("router DMZ is %+v, want no DMZ", got)
	}
}

func TestFirewallDMZResourceRouterErrors(t *testing.T) {
	parallel(t)
	t.Run("get after create", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		gets := 0
		srv.Handle("firewall", "get_dmz", func(json.RawMessage) (any, error) {
			gets++
			if gets == 2 {
				return nil, glinettest.ErrMethodNotFound
			}
			return srv.Firewall.DMZ(), nil
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			CheckDestroy:             expectFakeDMZ(srv, glinettest.DMZ{}),
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall DMZ.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ConfigPlanChecks:  expectDMZAction(plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectDMZState(glinet.DMZ{Enabled: true, DestIP: dmzIP}),
				},
			},
		})
	})

	t.Run("get before set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "get_dmz")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall DMZ.*Method not found`),
				},
			},
		})
		if got := srv.Firewall.DMZ(); got != (glinettest.DMZ{}) {
			t.Errorf("router DMZ is %+v, want no DMZ", got)
		}
	})

	t.Run("get on import", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "get_dmz")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ResourceName:  dmzAddress,
					ImportState:   true,
					ImportStateId: "firewall_dmz",
					ExpectError:   regexp.MustCompile(`(?s)Unable to Read Firewall DMZ.*Method not found`),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				},
				{
					PreConfig:   failMethod(srv, "get_dmz"),
					Config:      fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall DMZ.*Method not found`),
				},
				{
					PreConfig: func() {
						srv.Handle("firewall", "get_dmz", func(json.RawMessage) (any, error) { return srv.Firewall.DMZ(), nil })
					},
					Config: fakeRouterConfig(srv),
					Check:  expectFakeDMZ(srv, glinettest.DMZ{}),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "set_dmz")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Firewall DMZ.*Method not found`),
				},
			},
		})
	})

	t.Run("set on delete", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		want := glinettest.DMZ{Enabled: true, DestIP: dmzIP}

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + dmzConfig(dmzIP, true),
				},
				{
					PreConfig:   failMethod(srv, "set_dmz"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable Firewall DMZ.*Method not found`),
				},
				{
					PreConfig: func() {
						if got := srv.Firewall.DMZ(); got != want {
							t.Errorf("router DMZ after the failed delete is %+v, want %+v", got, want)
						}
						srv.Firewall.SetDMZ(glinettest.DMZ{})
					},
					Config: fakeRouterConfig(srv),
				},
			},
		})
	})
}

// TestFirewallDMZResourceWithPortForwards creates, changes and destroys a DMZ and port forwards in the same applies.
// A DMZ section on the router before an added port forward makes the ID that add_port_forward returns
// differ from the ID of the port forward in the list.
func TestFirewallDMZResourceWithPortForwards(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	existing := forward("tcp", "50101", "8080", "existing")
	srv.Firewall.SetPortForwards(fakes(existing))
	srv.Firewall.SetDMZ(glinettest.DMZ{})

	var forwards []glinet.PortForward
	for i := range 4 {
		forwards = append(forwards, forward("tcp", fmt.Sprint(50001+i), "8080", fmt.Sprintf("f%d", i)))
	}
	config := func(dmz string, forwards ...glinet.PortForward) string {
		res := fakeRouterConfig(srv) + dmz
		for i, f := range forwards {
			res += forwardResource(fmt.Sprintf("f%d", i), f)
		}
		return res
	}
	changed := slices.Clone(forwards[:3])
	changed[1].DestPort = "9090"
	changed[2].Enabled = false

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			expectFakeForwards(srv, existing),
			expectFakeDMZ(srv, glinettest.DMZ{}),
		),
		Steps: []resource.TestStep{
			{
				Config: config(dmzConfig(dmzIP, true), forwards...),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectFakeForwardSet(srv, append([]glinet.PortForward{existing}, forwards...)...),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP}),
				),
			},
			{
				Config: config(dmzConfig(otherDMZIP, false), changed...),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectFakeForwardSet(srv, append([]glinet.PortForward{existing}, changed...)...),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: false, DestIP: otherDMZIP}),
				),
			},
			{
				Config: config(dmzConfig(otherDMZIP, false), changed...),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
