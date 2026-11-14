package provider

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	systemStatusAddress          = "data.glinet_system_status.test"
	systemStatusDataSourceConfig = `
data "glinet_system_status" "test" {}
`
)

func TestSystemStatusDataSource(t *testing.T) {
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+systemStatusDataSourceConfig, expectAttributes(systemStatusAddress, realRouterSystemStatusChecks()))
		return
	}

	srv := glinettest.NewServer(t)
	srv.System.SetStatus(json.RawMessage(`{
		"client": [{"cable_total": 1, "wireless_total": 2}],
		"network": [
			{"interface": "wwan", "online": true, "up": true},
			{"interface": "wan", "online": false, "up": true}
		],
		"service": [
			{"group_id": 2082, "name": "wgclient", "peer_id": 2004, "status": 1},
			{"name": "adguard", "status": 0}
		],
		"system": {
			"cpu": {"temperature": 69},
			"ddns_enabled": true,
			"flash_app": 1929216,
			"flash_free": 38596608,
			"flash_total": 134217728,
			"guest_ip": "192.168.9.1",
			"guest_netmask": "255.255.255.0",
			"ipv6_enabled": false,
			"lan_ip": "192.168.8.1",
			"lan_netmask": "255.255.255.0",
			"load_average": [0, 0.05, 0.07],
			"memory_buff_cache": 104353792,
			"memory_free": 155758592,
			"memory_total": 8589934592,
			"mode": 0,
			"netnat_enabled": true,
			"timestamp": 1794517485,
			"tzoffset": "-0500",
			"uptime": 835.31
		},
		"wifi": [
			{"band": "5G", "channel": 153, "encryption": "psk2", "guest": false, "hidden": false, "mld": false, "name": "default_radio0", "passwd": "secret-1", "ssid": "ssid-1", "up": true},
			{"band": "2G", "encryption": "psk2", "guest": true, "hidden": false, "mld": false, "name": "guest2g", "passwd": "secret-2", "ssid": "ssid-2", "up": false}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+systemStatusDataSourceConfig, expectAttributes(systemStatusAddress, map[string]knownvalue.Check{
		"lan_ip":        knownvalue.StringExact("192.168.8.1"),
		"lan_netmask":   knownvalue.StringExact("255.255.255.0"),
		"guest_ip":      knownvalue.StringExact("192.168.9.1"),
		"guest_netmask": knownvalue.StringExact("255.255.255.0"),
		"uptime":        knownvalue.Float64Exact(835.31),
		"load_average": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.Float64Exact(0),
			knownvalue.Float64Exact(0.05),
			knownvalue.Float64Exact(0.07),
		}),
		"memory_total":      knownvalue.Int64Exact(8589934592),
		"memory_free":       knownvalue.Int64Exact(155758592),
		"memory_buff_cache": knownvalue.Int64Exact(104353792),
		"flash_total":       knownvalue.Int64Exact(134217728),
		"flash_free":        knownvalue.Int64Exact(38596608),
		"flash_app":         knownvalue.Int64Exact(1929216),
		"cpu_temperature":   knownvalue.Int64Exact(69),
		"ddns_enabled":      knownvalue.Bool(true),
		"ipv6_enabled":      knownvalue.Bool(false),
		"nat_enabled":       knownvalue.Bool(true),
		"networks": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface": knownvalue.StringExact("wwan"),
				"up":        knownvalue.Bool(true),
				"online":    knownvalue.Bool(true),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface": knownvalue.StringExact("wan"),
				"up":        knownvalue.Bool(true),
				"online":    knownvalue.Bool(false),
			}),
		}),
		"services": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":   knownvalue.StringExact("wgclient"),
				"status": knownvalue.Int64Exact(1),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":   knownvalue.StringExact("adguard"),
				"status": knownvalue.Int64Exact(0),
			}),
		}),
		"wifi_interfaces": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":    knownvalue.StringExact("default_radio0"),
				"band":    knownvalue.StringExact("5G"),
				"ssid":    knownvalue.StringExact("ssid-1"),
				"channel": knownvalue.Int64Exact(153),
				"up":      knownvalue.Bool(true),
				"guest":   knownvalue.Bool(false),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":    knownvalue.StringExact("guest2g"),
				"band":    knownvalue.StringExact("2G"),
				"ssid":    knownvalue.StringExact("ssid-2"),
				"channel": knownvalue.Null(),
				"up":      knownvalue.Bool(false),
				"guest":   knownvalue.Bool(true),
			}),
		}),
	}))
}

func TestSystemStatusDataSourceAbsentFields(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.System.SetStatus(json.RawMessage(`{"network": [], "system": {"lan_ip": "192.168.8.1"}}`))

	readDataSource(t, fakeRouterConfig(srv)+systemStatusDataSourceConfig, expectAttributes(systemStatusAddress, map[string]knownvalue.Check{
		"lan_ip":          knownvalue.StringExact("192.168.8.1"),
		"guest_ip":        knownvalue.StringExact(""),
		"uptime":          knownvalue.Float64Exact(0),
		"load_average":    knownvalue.ListExact([]knownvalue.Check{}),
		"cpu_temperature": knownvalue.Null(),
		"networks":        knownvalue.ListExact([]knownvalue.Check{}),
		"services":        knownvalue.ListExact([]knownvalue.Check{}),
		"wifi_interfaces": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestSystemStatusDataSourceAPIError(t *testing.T) {
	expectReadError(t, glinettest.NewServer(t), systemStatusDataSourceConfig, "system", "get_status", "Unable to Read System Status")
}

// realRouterSystemStatusChecks checks only types, that the values that each router has are not empty, and the shape of the lists.
// The Wi-Fi channel is not checked, because the router can give none.
func realRouterSystemStatusChecks() map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"lan_ip":        nonEmptyString,
		"lan_netmask":   nonEmptyString,
		"guest_ip":      anyString,
		"guest_netmask": anyString,
		"uptime": knownvalue.Float64Func(func(v float64) error {
			if v <= 0 {
				return errors.New("value is not positive")
			}
			return nil
		}),
		"load_average":      knownvalue.ListSizeExact(3),
		"memory_total":      positiveInt64,
		"memory_free":       anyInt64,
		"memory_buff_cache": anyInt64,
		"flash_total":       positiveInt64,
		"flash_free":        anyInt64,
		"flash_app":         anyInt64,
		"ddns_enabled":      anyBool,
		"ipv6_enabled":      anyBool,
		"nat_enabled":       anyBool,
		"networks": knownvalue.ListPartial(map[int]knownvalue.Check{
			0: knownvalue.ObjectExact(map[string]knownvalue.Check{
				"interface": nonEmptyString,
				"up":        anyBool,
				"online":    anyBool,
			}),
		}),
		"services": knownvalue.ListPartial(map[int]knownvalue.Check{
			0: knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":   nonEmptyString,
				"status": anyInt64,
			}),
		}),
		"wifi_interfaces": knownvalue.ListPartial(map[int]knownvalue.Check{
			0: knownvalue.ObjectPartial(map[string]knownvalue.Check{
				"name":  nonEmptyString,
				"band":  nonEmptyString,
				"ssid":  anyString,
				"up":    anyBool,
				"guest": anyBool,
			}),
		}),
	}
}
