package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	repeaterSavedNetworksAddress          = "data.glinet_repeater_saved_networks.test"
	repeaterSavedNetworksDataSourceConfig = `
data "glinet_repeater_saved_networks" "test" {}
`
)

func TestRepeaterSavedNetworksDataSource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetSavedNetworks(json.RawMessage(`{"res": [
		{
			"auto_portal": false,
			"disguise": false,
			"key": "example-key",
			"macaddr": {"mode": "default", "update": "none"},
			"manual": false,
			"protocol": "dhcp",
			"ssid": "example"
		},
		{
			"auto_portal": true,
			"bssid": "02:00:00:00:00:41",
			"disguise": true,
			"dns": ["192.0.2.1", "198.51.100.1"],
			"gateway": "192.0.2.1",
			"identity": "example-user",
			"ip": "192.0.2.30",
			"key": "example-password",
			"macaddr": {"mode": "random", "macaddr": "02:00:00:00:00:42", "update": "time", "period": 24},
			"manual": true,
			"mtu": 1400,
			"netmask": "255.255.255.0",
			"protocol": "static",
			"ssid": "example-eap",
			"ttl": 64,
			"ttl_ipv6": 65
		},
		{"ssid": "example-old"}
	]}`))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + repeaterSavedNetworksDataSourceConfig,
				ConfigStateChecks: expectAttributes(repeaterSavedNetworksAddress, map[string]knownvalue.Check{
					"networks": knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"ssid":             knownvalue.StringExact("example"),
							"bssid":            knownvalue.Null(),
							"protocol":         knownvalue.StringExact("dhcp"),
							"ip":               knownvalue.Null(),
							"netmask":          knownvalue.Null(),
							"gateway":          knownvalue.Null(),
							"dns":              knownvalue.ListExact([]knownvalue.Check{}),
							"disguise_enabled": knownvalue.Bool(false),
							"mac_clone": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"mode":         knownvalue.StringExact("default"),
								"mac":          knownvalue.Null(),
								"update":       knownvalue.StringExact("none"),
								"period_hours": knownvalue.Null(),
							}),
							"ttl":                 knownvalue.Null(),
							"ttl_ipv6":            knownvalue.Null(),
							"mtu":                 knownvalue.Null(),
							"manual":              knownvalue.Bool(false),
							"auto_portal_enabled": knownvalue.Bool(false),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"ssid":     knownvalue.StringExact("example-eap"),
							"bssid":    knownvalue.StringExact("02:00:00:00:00:41"),
							"protocol": knownvalue.StringExact("static"),
							"ip":       knownvalue.StringExact("192.0.2.30"),
							"netmask":  knownvalue.StringExact("255.255.255.0"),
							"gateway":  knownvalue.StringExact("192.0.2.1"),
							"dns": knownvalue.ListExact([]knownvalue.Check{
								knownvalue.StringExact("192.0.2.1"),
								knownvalue.StringExact("198.51.100.1"),
							}),
							"disguise_enabled": knownvalue.Bool(true),
							"mac_clone": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"mode":         knownvalue.StringExact("random"),
								"mac":          knownvalue.StringExact("02:00:00:00:00:42"),
								"update":       knownvalue.StringExact("time"),
								"period_hours": knownvalue.Int64Exact(24),
							}),
							"ttl":                 knownvalue.Int64Exact(64),
							"ttl_ipv6":            knownvalue.Int64Exact(65),
							"mtu":                 knownvalue.Int64Exact(1400),
							"manual":              knownvalue.Bool(true),
							"auto_portal_enabled": knownvalue.Bool(true),
						}),
						knownvalue.ObjectPartial(map[string]knownvalue.Check{
							"ssid":      knownvalue.StringExact("example-old"),
							"protocol":  knownvalue.Null(),
							"mac_clone": knownvalue.Null(),
						}),
					}),
				}),
				Check: expectNoStateValue(repeaterSavedNetworksAddress, "example-key", "example-password", "example-user"),
			},
		},
	})
}

func TestRepeaterSavedNetworksDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetSavedNetworks(json.RawMessage(`{"res": []}`))

	readDataSource(t, fakeRouterConfig(srv)+repeaterSavedNetworksDataSourceConfig, expectAttributes(repeaterSavedNetworksAddress, map[string]knownvalue.Check{
		"networks": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestRepeaterSavedNetworksDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), repeaterSavedNetworksDataSourceConfig, "repeater", "get_saved_ap_list", "Unable to Read Repeater Saved Networks")
}
