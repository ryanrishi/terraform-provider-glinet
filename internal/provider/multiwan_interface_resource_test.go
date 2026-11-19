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

const multiWANInterfaceAddress = "glinet_multiwan_interface.test"

// The track hosts of each interface of the fake router.
var (
	fakeTrackIPv4 = []string{"192.0.2.1", "192.0.2.2", "198.51.100.1", "198.51.100.2"}
	fakeTrackIPv6 = []string{"2001:db8::1", "2001:db8::2", "2001:db8::3", "2001:db8::4"}
)

func multiWANInterfaceConfig(name, attributes string) string {
	return "resource \"glinet_multiwan_interface\" \"test\" {\n  interface = \"" + name + "\"\n" + attributes + "}\n"
}

func expectMultiWANInterface(checkEnabled bool, mode, protocol string, ipv4, ipv6 []string) []statecheck.StateCheck {
	return expectAttributes(multiWANInterfaceAddress, map[string]knownvalue.Check{
		"interface":      knownvalue.StringExact("wan"),
		"check_enabled":  knownvalue.Bool(checkEnabled),
		"track_mode":     knownvalue.StringExact(mode),
		"track_protocol": knownvalue.StringExact(protocol),
		"track_ipv4":     stringsExact(ipv4),
		"track_ipv6":     stringsExact(ipv6),
	})
}

// TestMultiWANInterfaceResource takes over the interface wan and changes the tracking. Each step checks the params
// of the write.
func TestMultiWANInterfaceResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setInterface := recordCalls(t, srv, "kmwan", "set_interface")
	ipv4 := []string{"192.0.2.10", "192.0.2.11"}
	ipv6 := []string{"2001:db8::10", "2001:db8::11", "2001:db8::12"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + multiWANInterfaceConfig("wan",
					"  track_mode = \"strict\"\n  track_protocol = \"ipv4\"\n  track_ipv4 = [\"192.0.2.10\", \"192.0.2.11\"]\n"),
				ConfigStateChecks: expectMultiWANInterface(true, "strict", "ipv4", ipv4, fakeTrackIPv6),
				Check: setInterface.expect(
					`{"interface":"wan","enable_check":true,"track_proto":0,"track_method":0,"track_mode":2,"track_ipv4":["192.0.2.10","192.0.2.11"]}`,
				),
			},
			{
				Config: fakeRouterConfig(srv) + multiWANInterfaceConfig("wan",
					"  track_protocol = \"both\"\n  track_ipv6 = [\"2001:db8::10\", \"2001:db8::11\", \"2001:db8::12\"]\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANInterfaceAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANInterface(true, "strict", "both", ipv4, ipv6),
				Check: setInterface.expect(
					`{"interface":"wan","enable_check":true,"track_proto":2,"track_method":0,"track_mode":2,` +
						`"track_ipv4":["192.0.2.10","192.0.2.11"],"track_ipv6":["2001:db8::10","2001:db8::11","2001:db8::12"]}`,
				),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", "  check_enabled = false\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANInterfaceAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANInterface(false, "strict", "both", ipv4, ipv6),
				Check:             setInterface.expect(`{"interface":"wan","enable_check":false}`),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", "  check_enabled = true\n  track_mode = \"low_data\"\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANInterfaceAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANInterface(true, "low_data", "both", ipv4, ipv6),
				Check: setInterface.expect(
					`{"interface":"wan","enable_check":true,"track_proto":2,"track_method":0,"track_mode":0,` +
						`"track_ipv4":["192.0.2.10","192.0.2.11"],"track_ipv6":["2001:db8::10","2001:db8::11","2001:db8::12"]}`,
				),
			},
			{
				ResourceName:                         multiWANInterfaceAddress,
				ImportState:                          true,
				ImportStateId:                        "wan",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "interface",
			},
		},
	})
}

// TestMultiWANInterfaceResourceNoWrite takes over an interface whose values are equal to the configuration: create
// sends no write.
func TestMultiWANInterfaceResourceNoWrite(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setInterface := recordCalls(t, srv, "kmwan", "set_interface")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", "  check_enabled = true\n  track_mode = \"normal\"\n"),
				ConfigStateChecks: expectMultiWANInterface(true, "normal", "ipv4", fakeTrackIPv4, fakeTrackIPv6),
				Check:             setInterface.expect(),
			},
		},
	})
}

// TestMultiWANInterfaceResourceDrift changes the track hosts on the router: the plan has an update, which sends
// them again.
func TestMultiWANInterfaceResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setInterface := recordCalls(t, srv, "kmwan", "set_interface")
	config := fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", "  track_ipv4 = [\"192.0.2.10\", \"192.0.2.11\"]\n")
	write := `{"interface":"wan","enable_check":true,"track_proto":0,"track_method":0,"track_mode":1,"track_ipv4":["192.0.2.10","192.0.2.11"]}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  setInterface.expect(write),
			},
			{
				PreConfig: func() {
					c := srv.MultiWAN.Config()
					c.Interfaces[0].TrackIPv4 = []string{"192.0.2.10", "192.0.2.12"}
					srv.MultiWAN.SetConfig(c)
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(multiWANInterfaceAddress, plancheck.ResourceActionUpdate),
				Check:            setInterface.expect(write),
			},
			{
				PreConfig: func() {
					c := srv.MultiWAN.Config()
					c.Interfaces = c.Interfaces[1:]
					srv.MultiWAN.SetConfig(c)
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestMultiWANInterfaceResourceRouterErrors has router values with which the write would not send a configured
// value, or would send fewer than two hosts.
func TestMultiWANInterfaceResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := map[string]struct {
		router     func(*glinettest.MultiWANInterface)
		attributes string
		err        string
	}{
		"check disabled on the router": {
			func(i *glinettest.MultiWANInterface) { i.EnableCheck = false },
			"  track_mode = \"strict\"\n",
			"track_mode cannot be set while check_enabled is false on the router",
		},
		"track_ipv6 with protocol ipv4 on the router": {
			func(*glinettest.MultiWANInterface) {},
			"  track_ipv6 = [\"2001:db8::10\", \"2001:db8::11\"]\n",
			"track_ipv6 cannot be set while track_protocol is ipv4 on the router",
		},
		"no IPv6 hosts on the router": {
			func(i *glinettest.MultiWANInterface) { i.TrackIPv6 = nil },
			"  track_protocol = \"ipv6\"\n",
			"track_protocol ipv6 needs two to four hosts in track_ipv6, and the router has 0. Set track_ipv6.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setInterface := recordCalls(t, srv, "kmwan", "set_interface")
			c := srv.MultiWAN.Config()
			tt.router(&c.Interfaces[0])
			srv.MultiWAN.SetConfig(c)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", tt.attributes),
						ExpectError: regexp.MustCompile(wrapped(tt.err)),
					},
				},
			})
			if got := setInterface.take(); len(got) != 0 {
				t.Errorf("kmwan.set_interface was called with %q", got)
			}
		})
	}
}

func TestMultiWANInterfaceResourceConfigErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setInterface := recordCalls(t, srv, "kmwan", "set_interface")

	tests := map[string]struct {
		name, attributes, err string
	}{
		"unknown interface": {"secondwan", "", `The router has no multi-WAN interface "secondwan". The interfaces of the router are: wan, wwan, tethering.`},
		"track setting with check disabled": {
			"wan", "  check_enabled = false\n  track_mode = \"strict\"\n", "track_mode cannot be set when check_enabled is false.",
		},
		"track_ipv4 with protocol ipv6": {
			"wan", "  track_protocol = \"ipv6\"\n  track_ipv4 = [\"192.0.2.10\", \"192.0.2.11\"]\n", "track_ipv4 cannot be set when track_protocol is ipv6.",
		},
		"track_ipv6 with protocol ipv4": {
			"wan", "  track_protocol = \"ipv4\"\n  track_ipv6 = [\"2001:db8::10\", \"2001:db8::11\"]\n", "track_ipv6 cannot be set when track_protocol is ipv4.",
		},
		"one host":         {"wan", "  track_ipv4 = [\"192.0.2.10\"]\n", "list must contain at least 2 elements and at most 4 elements"},
		"IPv6 in ipv4":     {"wan", "  track_ipv4 = [\"192.0.2.10\", \"2001:db8::1\"]\n", "must be an IPv4 address"},
		"IPv4 in ipv6":     {"wan", "  track_ipv6 = [\"2001:db8::1\", \"192.0.2.10\"]\n", "must be an IPv6 address"},
		"unknown mode":     {"wan", "  track_mode = \"passive\"\n", `value must be one of: ["low_data" "normal" "strict"]`},
		"unknown protocol": {"wan", "  track_protocol = \"ipv4v6\"\n", `value must be one of: ["ipv4" "ipv6" "both"]`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + multiWANInterfaceConfig(tt.name, tt.attributes),
						ExpectError: regexp.MustCompile(wrapped(tt.err)),
					},
				},
			})
		})
	}
	if got := setInterface.take(); len(got) != 0 {
		t.Errorf("kmwan.set_interface was called with %q", got)
	}
}

func TestMultiWANInterfaceResourceUnknownTrackMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	c := srv.MultiWAN.Config()
	c.Interfaces[0].TrackMode = 3
	srv.MultiWAN.SetConfig(c)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + multiWANInterfaceConfig("wan", "  check_enabled = true\n"),
				ExpectError: regexp.MustCompile(wrapped(`The multi-WAN interface "wan" has the track mode 3, which the provider does not know.`)),
			},
		},
	})
}
