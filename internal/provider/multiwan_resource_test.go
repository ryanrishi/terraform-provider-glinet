package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const multiWANAddress = "glinet_multiwan.test"

func multiWANConfig(attributes string) string {
	return "resource \"glinet_multiwan\" \"test\" {\n" + attributes + "}\n"
}

func expectMultiWAN(mode string, priority, weights knownvalue.Check) []statecheck.StateCheck {
	return expectAttributes(multiWANAddress, map[string]knownvalue.Check{
		"id":       knownvalue.StringExact("multiwan"),
		"mode":     knownvalue.StringExact(mode),
		"priority": priority,
		"weights":  weights,
	})
}

// TestMultiWANResource changes the failover order, changes to load balance mode and back to failover mode. Each
// step checks the params of the write.
func TestMultiWANResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setConfig := recordCalls(t, srv, "kmwan", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANConfig("  mode = \"failover\"\n  priority = [\"wwan\"]\n"),
				ConfigStateChecks: expectMultiWAN("failover", stringsExact([]string{"wwan"}), knownvalue.Null()),
				Check: setConfig.expect(
					`{"mode":0,"interfaces":[{"interface":"wwan","metric":1},{"interface":"wan","metric":2},{"interface":"tethering","metric":3}]}`,
				),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANConfig("  mode = \"load_balance\"\n  weights = { wan = 3, tethering = 0 }\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWAN("load_balance", knownvalue.Null(), knownvalue.MapExact(map[string]knownvalue.Check{"wan": knownvalue.Int64Exact(3), "tethering": knownvalue.Int64Exact(0)})),
				Check: setConfig.expect(
					`{"mode":1,"interfaces":[{"interface":"wan","weight":3},{"interface":"wwan","weight":1},{"interface":"tethering","weight":0}]}`,
				),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANConfig("  mode = \"failover\"\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWAN("failover", stringsExact([]string{"wwan", "wan", "tethering"}), knownvalue.Null()),
				Check: setConfig.expect(
					`{"mode":0,"interfaces":[{"interface":"wwan","metric":1},{"interface":"wan","metric":2},{"interface":"tethering","metric":3}]}`,
				),
			},
			{
				ResourceName:      multiWANAddress,
				ImportState:       true,
				ImportStateId:     "multiwan",
				ImportStateVerify: true,
			},
		},
	})
}

// TestMultiWANResourceLoadBalanceImport imports the resource in load balance mode: the weights have all interfaces.
func TestMultiWANResourceLoadBalanceImport(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + multiWANConfig("  mode = \"load_balance\"\n  weights = { wan = 2, wwan = 1, tethering = 1 }\n"),
			},
			{
				ResourceName:      multiWANAddress,
				ImportState:       true,
				ImportStateId:     "multiwan",
				ImportStateVerify: true,
			},
		},
	})
}

// TestMultiWANResourceNoWrite takes over a router whose values are equal to the configuration: create sends no
// write.
func TestMultiWANResourceNoWrite(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setConfig := recordCalls(t, srv, "kmwan", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANConfig("  mode = \"failover\"\n  priority = [\"wan\", \"wwan\"]\n"),
				ConfigStateChecks: expectMultiWAN("failover", stringsExact([]string{"wan", "wwan"}), knownvalue.Null()),
				Check:             setConfig.expect(),
			},
		},
	})
}

// TestMultiWANResourceDrift changes the failover order and then the mode on the router: each plan has an update,
// which sets the configuration again.
func TestMultiWANResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setConfig := recordCalls(t, srv, "kmwan", "set_config")
	config := fakeRouterConfig(srv) + multiWANConfig("  mode = \"failover\"\n  priority = [\"wan\"]\n")
	restore := `{"mode":0,"interfaces":[{"interface":"wan","metric":1},{"interface":"tethering","metric":2},{"interface":"wwan","metric":3}]}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  setConfig.expect(),
			},
			{
				PreConfig: func() {
					c := srv.MultiWAN.Config()
					c.Interfaces[1].Metric, c.Interfaces[2].Metric = 30, 5
					srv.MultiWAN.SetConfig(c)
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(multiWANAddress, plancheck.ResourceActionUpdate),
				Check:            setConfig.expect(restore),
			},
			{
				PreConfig: func() {
					c := srv.MultiWAN.Config()
					c.Mode = 1
					srv.MultiWAN.SetConfig(c)
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(multiWANAddress, plancheck.ResourceActionUpdate),
				Check:            setConfig.expect(restore),
			},
		},
	})
}

func TestMultiWANResourceErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setConfig := recordCalls(t, srv, "kmwan", "set_config")

	tests := map[string]struct {
		attributes string
		err        string
	}{
		"unknown interface in priority": {
			"  mode = \"failover\"\n  priority = [\"modem_1_1_2\"]\n",
			`The router has no multi-WAN interface "modem_1_1_2". The interfaces of the router are: wan, wwan, tethering.`,
		},
		"unknown interface in weights": {
			"  mode = \"load_balance\"\n  weights = { secondwan = 1 }\n",
			`The router has no multi-WAN interface "secondwan".`,
		},
		"weights in failover mode":  {"  mode = \"failover\"\n  weights = { wan = 1 }\n", "weights cannot be set when mode is failover."},
		"priority in load balance":  {"  mode = \"load_balance\"\n  priority = [\"wan\"]\n", "priority cannot be set when mode is load_balance."},
		"weight out of range":       {"  mode = \"load_balance\"\n  weights = { wan = 11 }\n", "value must be between 0 and 10"},
		"duplicate interface":       {"  mode = \"failover\"\n  priority = [\"wan\", \"wan\"]\n", "This attribute contains duplicate values"},
		"unknown mode":              {"  mode = \"balance\"\n", `value must be one of: ["failover" "load_balance"]`},
		"empty priority":            {"  mode = \"failover\"\n  priority = []\n", "list must contain at least 1 elements"},
		"empty interface in weight": {"  mode = \"load_balance\"\n  weights = { \"\" = 1 }\n", "string length must be at least 1"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + multiWANConfig(tt.attributes),
						ExpectError: regexp.MustCompile(wrapped(tt.err)),
					},
				},
			})
		})
	}
	if got := setConfig.take(); len(got) != 0 {
		t.Errorf("kmwan.set_config was called with %q", got)
	}
}

func TestMultiWANResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + multiWANConfig("  mode = \"failover\"\n"),
				ResourceName:  multiWANAddress,
				ImportState:   true,
				ImportStateId: "wan",
				ExpectError:   regexp.MustCompile(wrapped(`The import ID of glinet_multiwan must be "multiwan", got "wan".`)),
			},
		},
	})
}

func TestMultiWANResourceReadError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	expectReadError(t, srv, multiWANConfig("  mode = \"failover\"\n"), "kmwan", "get_config", "Unable to Read Multi-WAN Configuration")
}
