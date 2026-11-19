package provider

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
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

const wgRouteAddress = "glinet_wireguard_server_route.test"

// wgRoute is a configuration of glinet_wireguard_server_route. The empty fields are left out.
type wgRoute struct {
	dest, gateway string
	metric, mtu   int64
}

func (r wgRoute) config() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nresource \"glinet_wireguard_server_route\" \"test\" {\n  destination = %q\n", r.dest)
	if r.gateway != "" {
		fmt.Fprintf(&b, "  gateway = %q\n", r.gateway)
	}
	if r.metric != 0 {
		fmt.Fprintf(&b, "  metric = %d\n", r.metric)
	}
	if r.mtu != 0 {
		fmt.Fprintf(&b, "  mtu = %d\n", r.mtu)
	}
	b.WriteString("}\n")
	return b.String()
}

func expectWGRoute(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgRouteAddress, tfjsonpath.New(attribute), value)
}

// expectRouterRoutes checks that the WireGuard server has route rules with exactly the destinations want, IPv4 first.
func expectRouterRoutes(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		routes, err := client.WireGuardServer.ListRoutes(context.Background())
		if err != nil {
			return err
		}
		var got []string
		for _, r := range append(routes.IPv4, routes.IPv6...) {
			got = append(got, r.Dest)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("router has the WireGuard server route rules %q, want %q", got, want)
		}
		return nil
	}
}

func TestWireGuardServerRouteResource(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_route")
	sets := recordCalls(t, srv, "wg-server", "set_route")
	removes := recordCalls(t, srv, "wg-server", "remove_route")
	full := wgRoute{dest: "192.168.10.0/24", gateway: "10.0.0.2", metric: 10, mtu: 1400}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			expectRouterRoutes(client),
			removes.expect(`{"rule_id":1,"route_flag":4}`),
		),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + full.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGRoute("destination", knownvalue.StringExact("192.168.10.0/24")),
					expectWGRoute("gateway", knownvalue.StringExact("10.0.0.2")),
					expectWGRoute("metric", knownvalue.Int64Exact(10)),
					expectWGRoute("mtu", knownvalue.Int64Exact(1400)),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterRoutes(client, "192.168.10.0/24"),
					adds.expect(`{"dest":"192.168.10.0/24","gateway":"10.0.0.2","metric":10,"mtu":1400,"route_flag":4}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24", gateway: "10.0.0.3"}.config(),
				ConfigPlanChecks: expectResourceAction(wgRouteAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGRoute("gateway", knownvalue.StringExact("10.0.0.3")),
					expectWGRoute("metric", knownvalue.Null()),
					expectWGRoute("mtu", knownvalue.Null()),
				},
				Check: resource.ComposeTestCheckFunc(
					adds.expect(),
					sets.expect(`{"rule_id":1,"dest":"192.168.10.0/24","gateway":"10.0.0.3","metric":0,"mtu":0,"route_flag":4}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24"}.config(),
				ConfigPlanChecks: expectResourceAction(wgRouteAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGRoute("gateway", knownvalue.Null()),
				},
				Check: sets.expect(`{"rule_id":1,"dest":"192.168.10.0/24","gateway":"","metric":0,"mtu":0,"route_flag":4}`),
			},
			{
				ResourceName:                         wgRouteAddress,
				ImportState:                          true,
				ImportStateId:                        "192.168.10.0/24",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "destination",
			},
		},
	})
}

// TestWireGuardServerRouteResourceMaxMetric sends the largest metric, 4294967295.
func TestWireGuardServerRouteResourceMaxMetric(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_route")
	config := fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24", metric: math.MaxUint32}.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectWGRoute("metric", knownvalue.Int64Exact(math.MaxUint32))},
				Check:             adds.expect(`{"dest":"192.168.10.0/24","gateway":"","metric":4294967295,"mtu":0,"route_flag":4}`),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWireGuardServerRouteResourceIPv6(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_route")
	removes := recordCalls(t, srv, "wg-server", "remove_route")
	route := wgRoute{dest: "fd00:db8:10::/64", gateway: "fd00:db8::2"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             removes.expect(`{"rule_id":1,"route_flag":6}`),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + route.config(),
				Check: resource.ComposeTestCheckFunc(
					adds.expect(`{"dest":"fd00:db8:10::/64","gateway":"fd00:db8::2","metric":0,"mtu":0,"route_flag":6}`),
					func(*terraform.State) error {
						routes, err := client.WireGuardServer.ListRoutes(context.Background())
						if err != nil {
							return err
						}
						if len(routes.IPv4) != 0 || len(routes.IPv6) != 1 {
							return fmt.Errorf("router has the IPv4 route rules %v and the IPv6 route rules %v, want one IPv6 route rule", routes.IPv4, routes.IPv6)
						}
						return nil
					},
				),
			},
			{
				Config:           fakeRouterConfig(srv) + route.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWireGuardServerRouteResourceDrift(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	route := wgRoute{dest: "192.168.10.0/24", gateway: "10.0.0.2", metric: 10}
	config := fakeRouterConfig(srv) + route.config()
	ctx := context.Background()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					if err := client.WireGuardServer.SetRoute(ctx, glinet.VPNRoute{ID: 1, Dest: "192.168.10.0/24", Metric: 20}); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgRouteAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGRoute("gateway", knownvalue.StringExact("10.0.0.2")),
					expectWGRoute("metric", knownvalue.Int64Exact(10)),
				},
			},
			{
				PreConfig: func() {
					if err := client.WireGuardServer.RemoveRoute(ctx, glinet.VPNRoute{ID: 1, Dest: "192.168.10.0/24"}); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgRouteAddress, plancheck.ResourceActionCreate),
				Check:            expectRouterRoutes(client, "192.168.10.0/24"),
			},
		},
	})
}

func TestWireGuardServerRouteResourceAlreadyExists(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	if err := client.WireGuardServer.AddRoute(context.Background(), glinet.VPNRoute{Dest: "192.168.10.0/24"}); err != nil {
		t.Fatal(err)
	}
	adds := recordCalls(t, srv, "wg-server", "add_route")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24"}.config(),
				ExpectError: regexp.MustCompile(`(?s)WireGuard Server Route Rule Already Exists.*` +
					wrapped(`Import it with the ID "192.168.10.0/24"`)),
			},
		},
	})
	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-server.add_route was called with %q", got)
	}
}

func TestWireGuardServerRouteResourceNotInitialized(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-server", "add_route")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24"}.config(),
				ExpectError: regexp.MustCompile(`(?s)WireGuard Server Not Initialized.*` + wrapped("Initialize it with glinet_wireguard_server or in the web UI")),
			},
		},
	})
	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-server.add_route was called with %q", got)
	}
}

func TestWireGuardServerRouteResourceImportErrors(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)

	tests := []struct {
		id, error string
	}{
		{"192.168.99.0/24", `Cannot import non-existent remote object`},
	}
	for _, id := range []string{"route", "192.168.10.1", "1"} {
		tests = append(tests, struct{ id, error string }{id, `(?s)Invalid Import ID.*` + wrapped("must be the destination of the route rule")})
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24"}.config(),
					ResourceName:  wgRouteAddress,
					ImportState:   true,
					ImportStateId: tt.id,
					ExpectError:   regexp.MustCompile(tt.error),
				},
			},
		})
	}
}

func TestWireGuardServerRouteResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_route")

	tests := []struct {
		route wgRoute
		error string
	}{
		{wgRoute{dest: "192.168.10.1"}, `destination must be an IP address with a prefix length`},
		{wgRoute{dest: "192.168.10.0/24", gateway: "gw.example.com"}, `gateway must be an IP address`},
		{wgRoute{dest: "192.168.10.0/24", gateway: "fd00:db8::2"}, wrapped("gateway fd00:db8::2 must have the address family of destination 192.168.10.0/24")},
		{wgRoute{dest: "fd00:db8:10::/64", gateway: "10.0.0.2"}, wrapped("gateway 10.0.0.2 must have the address family of destination fd00:db8:10::/64")},
		{wgRoute{dest: "192.168.10.0/24", metric: -1}, wrapped("metric value must be between 1 and 4294967295")},
		{wgRoute{dest: "192.168.10.0/24", metric: 4294967296}, wrapped("metric value must be between 1 and 4294967295")},
		{wgRoute{dest: "192.168.10.0/24", mtu: 67}, `mtu value must be between 68 and 65535`},
		{wgRoute{dest: "192.168.10.0/24", mtu: 65536}, `mtu value must be between 68 and 65535`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.route.config(),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}
	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-server.add_route was called with %q", got)
	}
}

func TestWireGuardServerRouteResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := []struct {
		method, error string
		step          int
	}{
		{"get_config", "Unable to Read WireGuard Server Configuration", 0},
		{"add_route", "Unable to Create WireGuard Server Route Rule", 0},
		{"get_route_list", "Unable to Read WireGuard Server Route Rules", 1},
		{"set_route", "Unable to Update WireGuard Server Route Rule", 1},
		{"remove_route", "Unable to Delete WireGuard Server Route Rule", 2},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			srv, _ := wgServerTestRouter(t)
			configs := []string{
				fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24"}.config(),
				fakeRouterConfig(srv) + wgRoute{dest: "192.168.10.0/24", metric: 10}.config(),
				fakeRouterConfig(srv),
			}
			steps := make([]resource.TestStep, tt.step+1)
			for i := range steps {
				steps[i].Config = configs[i]
			}
			steps[tt.step].PreConfig = func() { failFirstCall(srv, "wg-server", tt.method) }
			steps[tt.step].ExpectError = regexp.MustCompile(`(?s)` + wrapped(tt.error) + `.*Method not found`)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps:                    steps,
			})
		})
	}
}

// TestWireGuardServerRouteResourceStateOf020 refreshes a state that release 0.2.0 wrote, with dest. The framework drops
// dest, so the state has no destination, and Read removes the resource from the state although the router has the
// route rule. The next apply then fails because the route rule exists, until the route rule is imported again.
func TestWireGuardServerRouteResourceStateOf020(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	if err := client.WireGuardServer.AddRoute(context.Background(), glinet.VPNRoute{Dest: "192.168.10.0/24"}); err != nil {
		t.Fatal(err)
	}

	if state := refreshPriorState(t, srv, "glinet_wireguard_server_route", `{"dest":"192.168.10.0/24","gateway":null,"metric":null,"mtu":null}`); state != nil {
		t.Errorf("state is %v, want the resource removed", state)
	}
}
