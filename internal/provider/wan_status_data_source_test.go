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
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
			"cable_inserted":    anyBool,
			"cable_enabled":     anyBool,
			"mac_clone_enabled": anyBool,
			"mode":              anyInt64,
			"status":            anyInt64,
			"second_wan": knownvalue.ObjectExact(map[string]knownvalue.Check{
				"mode":          orNull(anyInt64),
				"status":        orNull(anyInt64),
				"error_code":    orNull(anyInt64),
				"error_message": orNull(anyString),
			}),
			"interfaces": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"interface": nonEmptyString,
					"ip":        nonEmptyString,
					"netmask":   nonEmptyString,
					"prefix":    positiveInt64,
					"network":   nonEmptyString,
					"broadcast": nonEmptyString,
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
			{"interface": "wwan", "info": {"broadcast": "192.168.100.255", "ipaddr": "192.168.100.20", "netmask": "255.255.255.0", "network": "192.168.100.0", "prefix": 24}}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
		"cable_inserted":    knownvalue.Bool(true),
		"cable_enabled":     knownvalue.Bool(true),
		"mac_clone_enabled": knownvalue.Bool(false),
		"mode":              knownvalue.Int64Exact(2),
		"status":            knownvalue.Int64Exact(1),
		"second_wan": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"mode":          knownvalue.Null(),
			"status":        knownvalue.Null(),
			"error_code":    knownvalue.Int64Exact(-4),
			"error_message": knownvalue.StringExact("Unsupported features!"),
		}),
		"interfaces": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface": knownvalue.StringExact("wan"),
				"ip":        knownvalue.StringExact("10.0.0.2"),
				"netmask":   knownvalue.StringExact("255.255.255.0"),
				"prefix":    knownvalue.Int64Exact(24),
				"network":   knownvalue.StringExact("10.0.0.0"),
				"broadcast": knownvalue.StringExact("10.0.0.255"),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface": knownvalue.StringExact("wwan"),
				"ip":        knownvalue.StringExact("192.168.100.20"),
				"netmask":   knownvalue.StringExact("255.255.255.0"),
				"prefix":    knownvalue.Int64Exact(24),
				"network":   knownvalue.StringExact("192.168.100.0"),
				"broadcast": knownvalue.StringExact("192.168.100.255"),
			}),
		}),
	}))
}

// TestWANStatusDataSourceSecondWAN reads a router with a second WAN port, which does not read the cable of the second port.
func TestWANStatusDataSourceSecondWAN(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Cable.SetStatus(json.RawMessage(`{"mode": 0, "secondwan": {"mode": 2, "status": 1}, "status": 3}`))
	calls := recordCalls(t, srv, "network", "check_wan_cable")

	readDataSource(t, fakeRouterConfig(srv)+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
		"mode":   knownvalue.Int64Exact(0),
		"status": knownvalue.Int64Exact(3),
		"second_wan": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"mode":          knownvalue.Int64Exact(2),
			"status":        knownvalue.Int64Exact(1),
			"error_code":    knownvalue.Null(),
			"error_message": knownvalue.Null(),
		}),
	}))

	got := calls.take()
	if len(got) == 0 {
		t.Error("network.check_wan_cable was not called")
	}
	for _, params := range got {
		if params != `{"secondwan":false}` {
			t.Errorf("network.check_wan_cable was called with %s", params)
		}
	}
}

func TestWANStatusDataSourceNoUplink(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.LAN.SetWANInfo(json.RawMessage(`{"wan_info": []}`))

	readDataSource(t, fakeRouterConfig(srv)+wanStatusDataSourceConfig, expectAttributes(wanStatusAddress, map[string]knownvalue.Check{
		"cable_inserted": knownvalue.Bool(false),
		"status":         knownvalue.Int64Exact(3),
		"interfaces":     knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestWANStatusDataSourceAPIError(t *testing.T) {
	parallel(t)
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
