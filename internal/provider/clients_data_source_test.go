package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	clientsAddress          = "data.glinet_clients.test"
	clientsDataSourceConfig = `
data "glinet_clients" "test" {}
`
)

func TestClientsDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
			"clients": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"mac":              nonEmptyString,
					"name":             anyString,
					"alias":            orNull(anyString),
					"class":            orNull(anyString),
					"blocked":          anyBool,
					"limit_rx":         anyFloat64,
					"limit_tx":         anyFloat64,
					"ip":               anyString,
					"ipv6":             knownvalue.ListPartial(map[int]knownvalue.Check{}),
					"connection":       anyString,
					"type":             anyInt64,
					"online":           anyBool,
					"online_time":      anyInt64,
					"remote":           anyBool,
					"rx":               anyInt64,
					"tx":               anyInt64,
					"total_rx":         anyInt64,
					"total_tx":         anyInt64,
					"total_rx_init":    anyInt64,
					"total_tx_init":    anyInt64,
					"last_rx":          knownvalue.ListPartial(map[int]knownvalue.Check{}),
					"last_tx":          knownvalue.ListPartial(map[int]knownvalue.Check{}),
					"last_update_rate": anyInt64,
				}),
			}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	srv.Clients.SetList(json.RawMessage(`{
		"clients": [
			{
				"alias": "laptop-1",
				"blocked": true,
				"class": "laptop",
				"iface": "5G",
				"ip": "192.168.8.120",
				"ipv6": ["2001:db8::27", "fe80::27"],
				"last_rx": [299999000000, 300000000000],
				"last_tx": [59999000000, 60000000000],
				"last_update_rate": 1794500000,
				"limit_rx": 4294967296,
				"limit_tx": 1.125,
				"mac": "02:00:00:00:00:27",
				"name": "host-1",
				"online": false,
				"online_time": 1794400000,
				"remote": false,
				"rx": 0,
				"total_rx": 300000000000,
				"total_rx_init": 300000000000,
				"total_tx": 60000000000,
				"total_tx_init": 60000000000,
				"tx": 0,
				"type": 1
			},
			{
				"blocked": false,
				"iface": "cable",
				"ip": "192.168.8.130",
				"limit_rx": 0,
				"limit_tx": 0,
				"mac": "02:00:00:00:00:16",
				"name": "host-2",
				"online": true,
				"remote": true,
				"rx": 4096,
				"tx": 512,
				"type": 2
			}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
		"clients": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"mac":      knownvalue.StringExact("02:00:00:00:00:27"),
				"name":     knownvalue.StringExact("host-1"),
				"alias":    knownvalue.StringExact("laptop-1"),
				"class":    knownvalue.StringExact("laptop"),
				"blocked":  knownvalue.Bool(true),
				"limit_rx": knownvalue.Float64Exact(4294967296),
				"limit_tx": knownvalue.Float64Exact(1.125),
				"ip":       knownvalue.StringExact("192.168.8.120"),
				"ipv6": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.StringExact("2001:db8::27"),
					knownvalue.StringExact("fe80::27"),
				}),
				"connection":    knownvalue.StringExact("5G"),
				"type":          knownvalue.Int64Exact(1),
				"online":        knownvalue.Bool(false),
				"online_time":   knownvalue.Int64Exact(1794400000),
				"remote":        knownvalue.Bool(false),
				"rx":            knownvalue.Int64Exact(0),
				"tx":            knownvalue.Int64Exact(0),
				"total_rx":      knownvalue.Int64Exact(300000000000),
				"total_tx":      knownvalue.Int64Exact(60000000000),
				"total_rx_init": knownvalue.Int64Exact(300000000000),
				"total_tx_init": knownvalue.Int64Exact(60000000000),
				"last_rx": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.Int64Exact(299999000000),
					knownvalue.Int64Exact(300000000000),
				}),
				"last_tx": knownvalue.ListExact([]knownvalue.Check{
					knownvalue.Int64Exact(59999000000),
					knownvalue.Int64Exact(60000000000),
				}),
				"last_update_rate": knownvalue.Int64Exact(1794500000),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"mac":              knownvalue.StringExact("02:00:00:00:00:16"),
				"name":             knownvalue.StringExact("host-2"),
				"alias":            knownvalue.Null(),
				"class":            knownvalue.Null(),
				"blocked":          knownvalue.Bool(false),
				"limit_rx":         knownvalue.Float64Exact(0),
				"limit_tx":         knownvalue.Float64Exact(0),
				"ip":               knownvalue.StringExact("192.168.8.130"),
				"ipv6":             knownvalue.ListExact([]knownvalue.Check{}),
				"connection":       knownvalue.StringExact("cable"),
				"type":             knownvalue.Int64Exact(2),
				"online":           knownvalue.Bool(true),
				"online_time":      knownvalue.Int64Exact(0),
				"remote":           knownvalue.Bool(true),
				"rx":               knownvalue.Int64Exact(4096),
				"tx":               knownvalue.Int64Exact(512),
				"total_rx":         knownvalue.Int64Exact(0),
				"total_tx":         knownvalue.Int64Exact(0),
				"total_rx_init":    knownvalue.Int64Exact(0),
				"total_tx_init":    knownvalue.Int64Exact(0),
				"last_rx":          knownvalue.ListExact([]knownvalue.Check{}),
				"last_tx":          knownvalue.ListExact([]knownvalue.Check{}),
				"last_update_rate": knownvalue.Int64Exact(0),
			}),
		}),
	}))
}

func TestClientsDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Clients.SetList(json.RawMessage(`{"clients": []}`))

	readDataSource(t, fakeRouterConfig(srv)+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
		"clients": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestClientsDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), clientsDataSourceConfig, "clients", "get_list", "Unable to Read Clients")
}
