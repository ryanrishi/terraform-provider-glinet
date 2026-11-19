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

// The tests of glinet_wan_port always use the fake router: a write can cut the wired uplink of the router.

const (
	wanPortAddress    = "glinet_wan_port.test"
	wanPortDefaultMAC = "02:00:00:00:00:02"
	wanPortCloneMAC   = "02:00:5E:10:00:01"
	wanPortRandomMAC  = "06:00:5E:10:00:02"
)

var randomMACPattern = regexp.MustCompile(`^[0-9A-F][26AE](:[0-9A-F]{2}){5}$`)

func wanPortConfig(name, mode, attributes string) string {
	return fmt.Sprintf(`
resource "glinet_wan_port" "test" {
  name = %q
  mode = %q
%s}
`, name, mode, attributes)
}

// wanPortState checks the state of the first WAN port. A nil check expects null.
func wanPortState(mode string, checks map[string]knownvalue.Check) []statecheck.StateCheck {
	all := map[string]knownvalue.Check{
		"id":                  knownvalue.StringExact("wan"),
		"name":                knownvalue.StringExact("wan"),
		"mode":                knownvalue.StringExact(mode),
		"mac_mode":            knownvalue.StringExact("default"),
		"mac_address":         knownvalue.StringExact(wanPortDefaultMAC),
		"mac_update":          knownvalue.Null(),
		"mac_period_hours":    knownvalue.Null(),
		"default_mac_address": knownvalue.StringExact(wanPortDefaultMAC),
		"label":               knownvalue.StringExact("WAN"),
	}
	for k, v := range checks {
		if v == nil {
			v = knownvalue.Null()
		}
		all[k] = v
	}
	return expectAttributes(wanPortAddress, all)
}

// wanPortsResult returns a get_ports_config result with the first WAN port in mode wan with the MAC settings mac.
func wanPortsResult(t *testing.T, mac map[string]any) json.RawMessage {
	mac["defmacaddr"] = wanPortDefaultMAC
	return json.RawMessage(mustJSON(t, map[string]any{"ports": []any{
		map[string]any{"name": "wan", "mode": "wan", "default_mode": "wan", "silk": "WAN", "macaddr": mac},
		map[string]any{"name": "lan1", "mode": "lan", "default_mode": "lan", "silk": "LAN1"},
	}}))
}

// expectRandomMACCall checks that the calls since the last check are one write with mac mode random and a random MAC
// address that the provider made, and with update and the other MAC values in the params.
func expectRandomMACCall(t *testing.T, calls *callRecorder, update string, period int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := calls.take()
		if len(got) != 1 {
			return fmt.Errorf("calls have params %q, want one call", got)
		}
		var params struct {
			Name string         `json:"name"`
			Mode string         `json:"mode"`
			MAC  map[string]any `json:"macaddr"`
		}
		if err := json.Unmarshal([]byte(got[0]), &params); err != nil {
			return err
		}
		mac, _ := params.MAC["macaddr"].(string)
		if !randomMACPattern.MatchString(mac) {
			return fmt.Errorf("MAC address %q is not a random locally administered unicast MAC address", mac)
		}
		want := map[string]any{"mode": "random", "macaddr": mac, "update": update}
		if period != 0 {
			want["period"] = period
		}
		if canonicalJSON(t, mustJSON(t, params.MAC)) != canonicalJSON(t, mustJSON(t, want)) || params.Name != "wan" || params.Mode != "wan" {
			return fmt.Errorf("call has params %s, want name wan, mode wan and macaddr %s", got[0], mustJSON(t, want))
		}
		return nil
	}
}

// TestWANPortResource clones a MAC address, changes to a random MAC address that the provider makes, and back to the
// MAC address of the router. Destroy leaves the port unchanged.
func TestWANPortResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_port_config")
	clone := wanPortConfig("wan", "wan", "  mac_mode = \"clone\"\n  mac_address = \""+wanPortCloneMAC+"\"\n")
	defaultMAC := `{"name":"wan","mode":"wan","macaddr":{"mode":"default","macaddr":"` + wanPortDefaultMAC + `"}}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterJSON(t, srv.Cable.Ports, string(wanPortsResult(t, map[string]any{"mode": "default", "macaddr": wanPortDefaultMAC}))),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + clone,
				ConfigStateChecks: wanPortState("wan", map[string]knownvalue.Check{
					"mac_mode":    knownvalue.StringExact("clone"),
					"mac_address": knownvalue.StringExact(wanPortCloneMAC),
				}),
				Check: calls.expect(`{"name":"wan","mode":"wan","macaddr":{"mode":"clone","macaddr":"` + wanPortCloneMAC + `"}}`),
			},
			{
				PreConfig: func() {
					srv.Cable.SetPorts(wanPortsResult(t, map[string]any{"mode": "clone", "macaddr": "02:00:5e:10:00:01"}))
				},
				Config:           fakeRouterConfig(srv) + clone,
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				ResourceName:                         wanPortAddress,
				ImportState:                          true,
				ImportStateId:                        "wan",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"mac_address"},
			},
			{
				Config:            fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"random\"\n"),
				ConfigPlanChecks:  expectResourceAction(wanPortAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: wanPortState("wan", map[string]knownvalue.Check{"mac_mode": knownvalue.StringExact("random"), "mac_address": knownvalue.StringRegexp(randomMACPattern), "mac_update": knownvalue.StringExact("none")}),
				Check:             expectRandomMACCall(t, calls, "none", 0),
			},
			{
				Config:           fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"random\"\n"),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"default\"\n"),
				ConfigPlanChecks:  expectResourceAction(wanPortAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: wanPortState("wan", nil),
				Check:             calls.expect(defaultMAC),
			},
			{
				Config:           fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"default\"\n"),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWANPortResourceRandomUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_port_config")
	random := func(attributes string) string {
		return fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"random\"\n  mac_address = \""+wanPortRandomMAC+"\"\n"+attributes)
	}
	call := func(update string, period int) string {
		params := map[string]any{"mode": "random", "macaddr": wanPortRandomMAC, "update": update}
		if period != 0 {
			params["period"] = period
		}
		return mustJSON(t, map[string]any{"name": "wan", "mode": "wan", "macaddr": params})
	}
	state := func(update string, period int64) []statecheck.StateCheck {
		return wanPortState("wan", map[string]knownvalue.Check{
			"mac_mode":         knownvalue.StringExact("random"),
			"mac_address":      knownvalue.StringExact(wanPortRandomMAC),
			"mac_update":       knownvalue.StringExact(update),
			"mac_period_hours": knownvalue.Int64Exact(period),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            random("  mac_update = \"time\"\n"),
				ConfigStateChecks: state("time", 168),
				Check:             calls.expect(call("time", 168)),
			},
			{
				Config:            random("  mac_update = \"time\"\n  mac_period_hours = 72\n"),
				ConfigStateChecks: state("time", 72),
				Check:             calls.expect(call("time", 72)),
			},
			{
				Config:            random("  mac_update = \"reboot\"\n"),
				ConfigStateChecks: state("reboot", 72),
				Check:             calls.expect(call("reboot", 72)),
			},
			{
				Config:           random("  mac_update = \"reboot\"\n"),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestWANPortResourceRouterValues writes a port that the router already has with a random MAC address: the write
// sends back the router values of the MAC attributes that are not in the configuration.
func TestWANPortResourceRouterValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Cable.SetPorts(wanPortsResult(t, map[string]any{"mode": "random", "macaddr": wanPortRandomMAC, "update": "time", "period": 48}))
	calls := recordCalls(t, srv, "cable", "set_port_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wanPortConfig("wan", "wan", ""),
				ConfigStateChecks: wanPortState("wan", map[string]knownvalue.Check{
					"mac_mode":         knownvalue.StringExact("random"),
					"mac_address":      knownvalue.StringExact(wanPortRandomMAC),
					"mac_update":       knownvalue.StringExact("time"),
					"mac_period_hours": knownvalue.Int64Exact(48),
				}),
				Check: calls.expect(`{"name":"wan","mode":"wan","macaddr":{"mode":"random","macaddr":"` + wanPortRandomMAC + `","update":"time","period":48}}`),
			},
			{
				PreConfig: func() {
					srv.Cable.SetPorts(wanPortsResult(t, map[string]any{"mode": "random", "macaddr": wanPortCloneMAC, "update": "reboot", "period": 24}))
				},
				Config: fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"random\"\n  mac_update = \"time\"\n"),
				ConfigStateChecks: wanPortState("wan", map[string]knownvalue.Check{
					"mac_mode":         knownvalue.StringExact("random"),
					"mac_address":      knownvalue.StringExact(wanPortCloneMAC),
					"mac_update":       knownvalue.StringExact("time"),
					"mac_period_hours": knownvalue.Int64Exact(24),
				}),
				Check: calls.expect(`{"name":"wan","mode":"wan","macaddr":{"mode":"random","macaddr":"` + wanPortCloneMAC + `","update":"time","period":24}}`),
			},
		},
	})
}

// TestWANPortResourceLAN changes the port to lan and back to wan. The change to lan sends no MAC settings.
func TestWANPortResourceLAN(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_port_config")
	cable := recordCalls(t, srv, "network", "check_wan_cable")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + wanPortConfig("wan", "lan", ""),
				ConfigStateChecks: wanPortState("lan", nil),
				Check:             resource.ComposeTestCheckFunc(calls.expect(`{"name":"wan","mode":"lan"}`), cable.expect(`{"secondwan":false}`)),
			},
			{
				Config:           fakeRouterConfig(srv) + wanPortConfig("wan", "lan", ""),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + wanPortConfig("wan", "wan", ""),
				ConfigPlanChecks:  expectResourceAction(wanPortAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: wanPortState("wan", nil),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"name":"wan","mode":"wan","macaddr":{"mode":"default","macaddr":"`+wanPortDefaultMAC+`"}}`),
					cable.expect(),
				),
			},
		},
	})
}

// TestWANPortResourceCableEnabled stops the change to lan while the router reports the cable as enabled.
func TestWANPortResourceCableEnabled(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Network.SetWANCable(json.RawMessage(`{"cable_enabled":true,"cable_inserted":true,"macclone_enabled":false}`))
	calls := recordCalls(t, srv, "cable", "set_port_config")
	before := srv.Cable.Ports()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + wanPortConfig("wan", "lan", ""),
				ExpectError: regexp.MustCompile(`(?s)Cable Not Removed.*` + wrapped("Remove the cable from the port")),
			},
		},
	})

	if params := calls.take(); len(params) != 0 {
		t.Errorf("set_port_config calls %v, want none", params)
	}
	expectJSONEqual(t, srv.Cable.Ports(), string(before))
}

func TestWANPortResourceSecondWAN(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_port_config")
	config := fakeRouterConfig(srv) + wanPortConfig("secondwan", "wan", "  mac_mode = \"clone\"\n  mac_address = \""+wanPortCloneMAC+"\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)No Second WAN Port`),
			},
			{
				PreConfig: func() {
					addSecondWANPort(t, srv)
					srv.Cable.SetPorts(json.RawMessage(`{"ports":[` +
						`{"name":"wan","mode":"wan","silk":"WAN1","macaddr":{"mode":"default","macaddr":"02:00:00:00:00:02","defmacaddr":"02:00:00:00:00:02"}},` +
						`{"name":"secondwan","mode":"wan","silk":"WAN2","macaddr":{"mode":"default","macaddr":"02:00:00:00:00:03","defmacaddr":"02:00:00:00:00:03"}}]}`))
				},
				Config: config,
				ConfigStateChecks: expectAttributes(wanPortAddress, map[string]knownvalue.Check{
					"id":                  knownvalue.StringExact("secondwan"),
					"name":                knownvalue.StringExact("secondwan"),
					"mode":                knownvalue.StringExact("wan"),
					"mac_mode":            knownvalue.StringExact("clone"),
					"mac_address":         knownvalue.StringExact(wanPortCloneMAC),
					"mac_update":          knownvalue.Null(),
					"mac_period_hours":    knownvalue.Null(),
					"default_mac_address": knownvalue.StringExact("02:00:00:00:00:03"),
					"label":               knownvalue.StringExact("WAN2"),
				}),
				Check: calls.expect(`{"name":"secondwan","mode":"wan","macaddr":{"mode":"clone","macaddr":"` + wanPortCloneMAC + `"}}`),
			},
			{
				ResourceName:                         wanPortAddress,
				ImportState:                          true,
				ImportStateId:                        "secondwan",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func TestWANPortResourcePortNotFound(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	addSecondWANPort(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + wanPortConfig("secondwan", "wan", ""),
				ExpectError: regexp.MustCompile(`(?s)Ethernet Port Not Found.*` + wrapped(`The router has no Ethernet port "secondwan". The ports of the router are: wan.`)),
			},
		},
	})
}

func TestWANPortResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "cable", "set_port_config")
	config := fakeRouterConfig(srv) + wanPortConfig("wan", "wan", "  mac_mode = \"clone\"\n  mac_address = \""+wanPortCloneMAC+"\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					calls.take()
					srv.Cable.SetPorts(wanPortsResult(t, map[string]any{"mode": "default", "macaddr": wanPortDefaultMAC}))
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wanPortAddress, plancheck.ResourceActionUpdate),
				Check:            calls.expect(`{"name":"wan","mode":"wan","macaddr":{"mode":"clone","macaddr":"` + wanPortCloneMAC + `"}}`),
			},
			{
				PreConfig:        func() { srv.Cable.SetPorts(json.RawMessage(`{"ports":[]}`)) },
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wanPortAddress, plancheck.ResourceActionCreate),
				ExpectError:      regexp.MustCompile(`Ethernet Port Not Found`),
			},
		},
	})
}

func TestWANPortResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, tt := range []struct{ mode, attributes, err string }{
		{"lan", `  mac_mode = "default"` + "\n", `mac_mode cannot be set when mode is lan`},
		{"lan", `  mac_period_hours = 24` + "\n", `mac_period_hours cannot be set when mode is lan`},
		{"wan", `  mac_mode = "clone"` + "\n", `mac_address is required when mac_mode is clone`},
		{"wan", `  mac_mode = "default"` + "\n  mac_address = \"" + wanPortCloneMAC + "\"\n", `mac_address cannot be set when mac_mode is default`},
		{"wan", `  mac_address = "` + wanPortCloneMAC + "\"\n", `mac_address cannot be set when mac_mode is not set`},
		{"wan", `  mac_mode = "clone"` + "\n  mac_address = \"" + wanPortCloneMAC + "\"\n  mac_update = \"time\"\n", `mac_update cannot be set when mac_mode is clone`},
		{"wan", `  mac_mode = "random"` + "\n  mac_update = \"none\"\n  mac_period_hours = 24\n", `mac_period_hours cannot be set when mac_update is none`},
		{"wan", `  mac_mode = "random"` + "\n  mac_period_hours = 25\n", `mac_period_hours must be a multiple of 24`},
		{"wan", `  mac_mode = "random"` + "\n  mac_period_hours = 4344\n", `mac_period_hours value must be between 24 and 4320`},
		{"wan", `  mac_mode = "clone"` + "\n  mac_address = \"01:00:5E:00:00:01\"\n", `must be a unicast MAC address`},
		{"wan", `  mac_mode = "clone"` + "\n  mac_address = \"00:00:00:00:00:00\"\n", `must be a unicast MAC address`},
		{"wan", `  mac_mode = "other"` + "\n", `mac_mode value must be one of`},
	} {
		t.Run(tt.err, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + wanPortConfig("wan", tt.mode, tt.attributes),
						ExpectError: regexp.MustCompile(wrapped(tt.err)),
					},
				},
			})
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + wanPortConfig("wan", "wan", ""),
				ResourceName:  wanPortAddress,
				ImportState:   true,
				ImportStateId: "lan1",
				ExpectError:   regexp.MustCompile(`(?s)Invalid Import ID.*must be "wan" or "secondwan"`),
			},
		},
	})
}

func TestWANPortResourceRouterErrors(t *testing.T) {
	parallel(t)

	for _, tt := range []struct{ module, method, mode, err string }{
		{"cable", "get_ports_config", "wan", `(?s)Unable to Read Ethernet Ports.*Method not found`},
		{"cable", "set_port_config", "wan", `(?s)Unable to Set WAN Port.*Method not found`},
		{"network", "check_wan_cable", "lan", `(?s)Unable to Read WAN Cable State.*Method not found`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			before := srv.Cable.Ports()
			failRouterMethod(srv, tt.module, tt.method)()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + wanPortConfig("wan", tt.mode, ""),
						ExpectError: regexp.MustCompile(tt.err),
					},
				},
			})
			expectJSONEqual(t, srv.Cable.Ports(), string(before))
		})
	}
}
