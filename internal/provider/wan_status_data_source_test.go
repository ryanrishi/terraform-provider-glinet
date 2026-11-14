package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	wanStatusAddress          = "data.glinet_wan_status.test"
	wanStatusDataSourceConfig = `
data "glinet_wan_status" "test" {}
`
)

func TestWANStatusDataSource(t *testing.T) {
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
			"cable_inserted":    anyBool,
			"cable_enabled":     anyBool,
			"mac_clone_enabled": anyBool,
			"mode":              anyInt64,
			"status":            anyInt64,
			"interfaces": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"interface":  nonEmptyString,
					"ip_address": nonEmptyString,
					"netmask":    nonEmptyString,
					"prefix":     positiveInt64,
					"network":    nonEmptyString,
					"broadcast":  nonEmptyString,
				}),
			}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	srv.Cable.SetStatus(json.RawMessage(`{"mode": 2, "secondwan": {"err_code": -4, "err_msg": "Unsupported features!"}, "status": 1}`))
	srv.Network.SetWANCable(json.RawMessage(`{"cable_enabled": true, "cable_inserted": true, "macclone_enabled": false}`))
	srv.LAN.SetWANInfo(json.RawMessage(`{
		"wan_info": [
			{"interface": "wan", "info": {"broadcast": "10.0.0.255", "ipaddr": "10.0.0.2", "netmask": "255.255.255.0", "network": "10.0.0.0", "prefix": 24}},
			{"interface": "wwan", "info": {"broadcast": "192.168.0.255", "ipaddr": "192.168.0.210", "netmask": "255.255.255.0", "network": "192.168.0.0", "prefix": 24}}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
		"cable_inserted":    knownvalue.Bool(true),
		"cable_enabled":     knownvalue.Bool(true),
		"mac_clone_enabled": knownvalue.Bool(false),
		"mode":              knownvalue.Int64Exact(2),
		"status":            knownvalue.Int64Exact(1),
		"interfaces": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface":  knownvalue.StringExact("wan"),
				"ip_address": knownvalue.StringExact("10.0.0.2"),
				"netmask":    knownvalue.StringExact("255.255.255.0"),
				"prefix":     knownvalue.Int64Exact(24),
				"network":    knownvalue.StringExact("10.0.0.0"),
				"broadcast":  knownvalue.StringExact("10.0.0.255"),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface":  knownvalue.StringExact("wwan"),
				"ip_address": knownvalue.StringExact("192.168.0.210"),
				"netmask":    knownvalue.StringExact("255.255.255.0"),
				"prefix":     knownvalue.Int64Exact(24),
				"network":    knownvalue.StringExact("192.168.0.0"),
				"broadcast":  knownvalue.StringExact("192.168.0.255"),
			}),
		}),
	}))
}

func TestWANStatusDataSourceNoUplink(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.LAN.SetWANInfo(json.RawMessage(`{"wan_info": []}`))

	readDataSource(t, fakeRouterConfig(srv)+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
		"cable_inserted": knownvalue.Bool(false),
		"status":         knownvalue.Int64Exact(3),
		"interfaces":     knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestWANStatusDataSourceAPIError(t *testing.T) {
	tests := []struct {
		module, method, summary string
	}{
		{"cable", "get_status", "Unable to Read WAN Port Status"},
		{"network", "check_wan_cable", "Unable to Read WAN Cable State"},
		{"lan", "get_wan_info", "Unable to Read WAN Addresses"},
	}

	for _, tt := range tests {
		t.Run(tt.module+"."+tt.method, func(t *testing.T) {
			expectReadError(t, glinettest.NewServer(t), wanStatusDataSourceConfig, tt.module, tt.method, tt.summary)
		})
	}
}
