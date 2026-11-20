package provider

import (
	"encoding/json"
	"fmt"
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

const orderAddress = "glinet_firewall_port_forward_order.test"

var (
	orderA = forward("tcp", "50001", "8080", "a")
	orderB = forward("udp", "50002", "8080", "b")
	orderC = forward("tcp udp", "50003-50004", "8080", "c")
	orderD = forward("tcp", "50005", "8080", "d")
)

func orderConfig(forwards ...glinet.PortForward) string {
	var items strings.Builder
	for _, f := range forwards {
		fmt.Fprintf(&items, "    { external_zone = %q, protocol = %q, external_port = %q },\n", f.Src, f.Proto, f.SrcDport)
	}
	return fmt.Sprintf(`
resource "glinet_firewall_port_forward_order" "test" {
  port_forwards = [
%s  ]
}
`, items.String())
}

func expectOrderState(forwards ...glinet.PortForward) []statecheck.StateCheck {
	items := make([]knownvalue.Check, len(forwards))
	for i, f := range forwards {
		items[i] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"external_zone": knownvalue.StringExact(f.Src),
			"protocol":      knownvalue.StringExact(f.Proto),
			"external_port": knownvalue.StringExact(f.SrcDport),
		})
	}
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(orderAddress, tfjsonpath.New("id"), knownvalue.StringExact("firewall_port_forward_order")),
		statecheck.ExpectKnownValue(orderAddress, tfjsonpath.New("port_forwards"), knownvalue.ListExact(items)),
	}
}

// expectImportedOrder checks that the import gives all port forwards of the router in their order.
func expectImportedOrder(forwards ...glinet.PortForward) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("import gave %d states, want 1", len(states))
		}
		attributes := states[0].Attributes
		want := map[string]string{
			"id":              "firewall_port_forward_order",
			"port_forwards.#": fmt.Sprint(len(forwards)),
		}
		for i, f := range forwards {
			want[fmt.Sprintf("port_forwards.%d.external_zone", i)] = f.Src
			want[fmt.Sprintf("port_forwards.%d.protocol", i)] = f.Proto
			want[fmt.Sprintf("port_forwards.%d.external_port", i)] = f.SrcDport
		}
		for key, value := range want {
			if attributes[key] != value {
				return fmt.Errorf("imported %s is %q, want %q", key, attributes[key], value)
			}
		}
		return nil
	}
}

func expectOrderAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(orderAddress, action)},
	}
}

func orderCall(ids ...int) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", glinettest.PortForwardID(id))
	}
	return `{"id_list":[` + strings.Join(quoted, ",") + `]}`
}

func TestFirewallPortForwardOrderResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(orderA, orderB, orderC, orderD))
	calls := recordCalls(t, srv, "firewall", "order_port_forward")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, orderB, orderD, orderA, orderC),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + orderConfig(orderC, orderA),
				ConfigStateChecks: expectOrderState(orderC, orderA),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(orderCall(2, 0, 1, 3)),
					expectFakeForwards(srv, orderC, orderA, orderB, orderD),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + orderConfig(orderC, orderA),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            calls.expect(),
			},
			{
				Config:            fakeRouterConfig(srv) + orderConfig(orderB, orderD),
				ConfigPlanChecks:  expectOrderAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectOrderState(orderB, orderD),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(orderCall(2, 3, 0, 1)),
					expectFakeForwards(srv, orderB, orderD, orderC, orderA),
				),
			},
			{
				PreConfig:         func() { srv.Firewall.SetPortForwards(fakes(orderB, orderD, orderA, orderC)) },
				Config:            fakeRouterConfig(srv) + orderConfig(orderB, orderD),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: expectOrderState(orderB, orderD),
				Check:             calls.expect(),
			},
			{
				ResourceName:     orderAddress,
				ImportState:      true,
				ImportStateId:    "firewall_port_forward_order",
				ImportStateCheck: expectImportedOrder(orderB, orderD, orderA, orderC),
			},
		},
	})
}

func TestFirewallPortForwardOrderResourceAlreadyInOrder(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(orderA, orderB, orderC))
	calls := recordCalls(t, srv, "firewall", "order_port_forward")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + orderConfig(orderA, orderB),
				ConfigStateChecks: expectOrderState(orderA, orderB),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(),
					expectFakeForwards(srv, orderA, orderB, orderC),
				),
			},
		},
	})
}

func TestFirewallPortForwardOrderResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(orderA, orderB, orderC))
	calls := recordCalls(t, srv, "firewall", "order_port_forward")
	config := fakeRouterConfig(srv) + orderConfig(orderC, orderB)

	tests := []struct {
		name   string
		router []glinet.PortForward
		call   string
		want   []glinet.PortForward
	}{
		{"order changed", []glinet.PortForward{orderB, orderC, orderA}, orderCall(1, 0, 2), []glinet.PortForward{orderC, orderB, orderA}},
		{"other forward first", []glinet.PortForward{orderA, orderC, orderB}, orderCall(1, 2, 0), []glinet.PortForward{orderC, orderB, orderA}},
		{"forward added before", []glinet.PortForward{orderD, orderC, orderB, orderA}, orderCall(1, 2, 0, 3), []glinet.PortForward{orderC, orderB, orderD, orderA}},
	}

	steps := []resource.TestStep{{Config: config, Check: calls.expect(orderCall(2, 1, 0))}}
	for _, tt := range tests {
		steps = append(steps, resource.TestStep{
			PreConfig:         func() { srv.Firewall.SetPortForwards(fakes(tt.router...)) },
			Config:            config,
			ConfigPlanChecks:  expectOrderAction(plancheck.ResourceActionUpdate),
			ConfigStateChecks: expectOrderState(orderC, orderB),
			Check: resource.ComposeTestCheckFunc(
				calls.expect(tt.call),
				expectFakeForwards(srv, tt.want...),
			),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

// TestFirewallPortForwardOrderResourceWithPortForwards creates the port forwards and their order in the same apply.
func TestFirewallPortForwardOrderResourceWithPortForwards(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(orderD))
	config := fakeRouterConfig(srv) + forwardResource("a", orderA) + forwardResource("b", orderB) + `
resource "glinet_firewall_port_forward_order" "test" {
  port_forwards = [
    for f in [glinet_firewall_port_forward.b, glinet_firewall_port_forward.a] :
    { external_zone = f.external_zone, protocol = f.protocol, external_port = f.external_port }
  ]
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, orderD),
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: expectOrderState(orderB, orderA),
				Check:             expectFakeForwards(srv, orderB, orderA, orderD),
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// TestFirewallPortForwardOrderResourceDuplicate moves the first port forward with an identity, as the firewall uses it.
func TestFirewallPortForwardOrderResourceDuplicate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	duplicate := orderB
	duplicate.Name = "duplicate"
	srv.Firewall.SetPortForwards(fakes(orderA, orderB, duplicate))
	calls := recordCalls(t, srv, "firewall", "order_port_forward")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + orderConfig(orderB),
				ConfigStateChecks: expectOrderState(orderB),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(orderCall(1, 0, 2)),
					expectFakeForwards(srv, orderB, orderA, duplicate),
				),
			},
		},
	})
}

func TestFirewallPortForwardOrderResourceImport(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(orderA, orderB, orderC))
	calls := recordCalls(t, srv, "firewall", "order_port_forward")
	config := fakeRouterConfig(srv) + orderConfig(orderA, orderB)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, orderA, orderB, orderC),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       orderAddress,
				ImportState:        true,
				ImportStateId:      "firewall_port_forward_order",
				ImportStatePersist: true,
				ImportStateCheck:   expectImportedOrder(orderA, orderB, orderC),
			},
			{
				Config:            fakeRouterConfig(srv) + orderConfig(orderA, orderB, orderC),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: expectOrderState(orderA, orderB, orderC),
				Check:             calls.expect(),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectOrderAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectOrderState(orderA, orderB),
				Check:             calls.expect(),
			},
		},
	})
}

func TestFirewallPortForwardOrderResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + orderConfig(orderA),
				ResourceName:  orderAddress,
				ImportState:   true,
				ImportStateId: "wan/tcp/50001",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestFirewallPortForwardOrderResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	tests := []struct {
		config string
		err    string
	}{
		{orderConfig(), `port_forwards list must contain at least 1 elements`},
		{orderConfig(orderA, orderB, orderA), `(?s)Duplicate Port Forward.*external port 50001 more than one time`},
		{orderConfig(forward("icmp", "50001", "8080", "")), `protocol value must be one of`},
		{orderConfig(forward("tcp", "0", "8080", "")), `external_port must be a port from 1 to 65535`},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + tt.config,
						ExpectError: regexp.MustCompile(tt.err),
					},
				},
			})
		})
	}
}

// TestFirewallPortForwardOrderResourceUnknownConfig uses values of terraform_data, which are unknown until the apply.
func TestFirewallPortForwardOrderResourceUnknownConfig(t *testing.T) {
	parallel(t)
	item := func(f glinet.PortForward) string {
		return fmt.Sprintf("{ external_zone = %q, protocol = %q, external_port = %q }", f.Src, f.Proto, f.SrcDport)
	}
	config := func(portForwards string) string {
		return fmt.Sprintf(`
resource "terraform_data" "list" {
  input = [%s, %s]
}

resource "terraform_data" "item" {
  input = %s
}

resource "glinet_firewall_port_forward_order" "test" {
  port_forwards = %s
}
`, item(orderC), item(orderA), item(orderB), portForwards)
	}

	tests := []struct {
		name         string
		portForwards string
		want         []glinet.PortForward
	}{
		{"unknown list", "terraform_data.list.output", []glinet.PortForward{orderC, orderA}},
		{"unknown attribute", fmt.Sprintf("[%s, { external_zone = %q, protocol = terraform_data.item.output.protocol, external_port = %q }]",
			item(orderC), orderB.Src, orderB.SrcDport), []glinet.PortForward{orderC, orderB}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetPortForwards(fakes(orderA, orderB, orderC))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + config(tt.portForwards),
						ConfigStateChecks: expectOrderState(tt.want...),
					},
				},
			})
		})
	}

	// The framework also reports the attributes of an unknown item as missing, before the apply.
	t.Run("duplicate with unknown item", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config(fmt.Sprintf("[%s, terraform_data.item.output, %s]", item(orderA), item(orderA))),
					ExpectError: regexp.MustCompile(`(?s)Duplicate Port Forward.*external port 50001 more than one time`),
				},
			},
		})
	})
}

func TestFirewallPortForwardOrderResourceRouterErrors(t *testing.T) {
	parallel(t)
	t.Run("forward not found", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetPortForwards(fakes(orderA, orderB))
		calls := recordCalls(t, srv, "firewall", "order_port_forward")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + orderConfig(orderB, orderC),
					ExpectError: regexp.MustCompile(`(?s)Firewall Port Forward Not Found.*external port 50003-50004`),
				},
			},
		})
		if got := calls.take(); len(got) != 0 {
			t.Errorf("order_port_forward calls have params %q, want none", got)
		}
	})

	t.Run("method not found", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetPortForwards(fakes(orderA, orderB))
		failMethod(srv, "order_port_forward")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + orderConfig(orderB),
					ExpectError: regexp.MustCompile(`(?s)Unable to Order Firewall Port Forwards.*does not have the method\s+firewall.order_port_forward.*Firmware\s+4.7.0`),
				},
			},
		})
	})

	t.Run("other error", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetPortForwards(fakes(orderA, orderB))
		srv.Handle("firewall", "order_port_forward", func(json.RawMessage) (any, error) {
			return nil, glinettest.ErrInvalidParams
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + orderConfig(orderB),
					ExpectError: regexp.MustCompile(`(?s)Unable to Order Firewall Port Forwards.*Invalid params`),
				},
			},
		})
	})

	t.Run("not applied", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetPortForwards(fakes(orderA, orderB))
		srv.Handle("firewall", "order_port_forward", func(json.RawMessage) (any, error) { return nil, nil })

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + orderConfig(orderB),
					ExpectError: regexp.MustCompile(`(?s)Firewall Port Forward Order Not Applied.*external port 50001`),
				},
			},
		})
	})

	t.Run("list", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetPortForwards(fakes(orderA, orderB))
		failMethod(srv, "get_port_forward_list")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + orderConfig(orderB),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Port Forwards.*Method not found`),
				},
			},
		})
	})
}
