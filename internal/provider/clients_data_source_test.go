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
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
			"clients": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"mac":       nonEmptyString,
					"name":      anyString,
					"alias":     orNull(anyString),
					"class":     orNull(anyString),
					"blocked":   anyBool,
					"limit_rx":  anyInt64,
					"limit_tx":  anyInt64,
					"ip":        anyString,
					"interface": anyString,
					"online":    anyBool,
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
				"ip": "192.168.8.174",
				"ipv6": [],
				"last_rx": [1020070722],
				"last_tx": [212475725],
				"last_update_rate": 1794515224,
				"limit_rx": 4294967296,
				"limit_tx": 1024,
				"mac": "02:00:00:00:00:27",
				"name": "host-1",
				"online": false,
				"online_time": 1713557437,
				"remote": false,
				"rx": 0,
				"total_rx": 354843683875,
				"total_rx_init": 354843683875,
				"total_tx": 70437565349,
				"total_tx_init": 70437565349,
				"tx": 0,
				"type": 1
			},
			{
				"blocked": false,
				"iface": "cable",
				"ip": "192.168.8.193",
				"limit_rx": 0,
				"limit_tx": 0,
				"mac": "02:00:00:00:00:16",
				"name": "host-2",
				"online": true,
				"type": 2
			}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
		"clients": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"mac":       knownvalue.StringExact("02:00:00:00:00:27"),
				"name":      knownvalue.StringExact("host-1"),
				"alias":     knownvalue.StringExact("laptop-1"),
				"class":     knownvalue.StringExact("laptop"),
				"blocked":   knownvalue.Bool(true),
				"limit_rx":  knownvalue.Int64Exact(4294967296),
				"limit_tx":  knownvalue.Int64Exact(1024),
				"ip":        knownvalue.StringExact("192.168.8.174"),
				"interface": knownvalue.StringExact("5G"),
				"online":    knownvalue.Bool(false),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"mac":       knownvalue.StringExact("02:00:00:00:00:16"),
				"name":      knownvalue.StringExact("host-2"),
				"alias":     knownvalue.Null(),
				"class":     knownvalue.Null(),
				"blocked":   knownvalue.Bool(false),
				"limit_rx":  knownvalue.Int64Exact(0),
				"limit_tx":  knownvalue.Int64Exact(0),
				"ip":        knownvalue.StringExact("192.168.8.193"),
				"interface": knownvalue.StringExact("cable"),
				"online":    knownvalue.Bool(true),
			}),
		}),
	}))
}

func TestClientsDataSourceEmpty(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.Clients.SetList(json.RawMessage(`{"clients": []}`))

	readDataSource(t, fakeRouterConfig(srv)+clientsDataSourceConfig, expectAttributes(clientsAddress, map[string]knownvalue.Check{
		"clients": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestClientsDataSourceAPIError(t *testing.T) {
	expectReadError(t, glinettest.NewServer(t), clientsDataSourceConfig, "clients", "get_list", "Unable to Read Clients")
}
