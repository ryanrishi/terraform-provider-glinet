package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	arpEntriesAddress          = "data.glinet_arp_entries.test"
	arpEntriesDataSourceConfig = `
data "glinet_arp_entries" "test" {}
`
)

func TestARPEntriesDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+arpEntriesDataSourceConfig, expectAttributes(arpEntriesAddress, map[string]knownvalue.Check{
			"entries": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"ip":     nonEmptyString,
					"mac":    nonEmptyString,
					"device": nonEmptyString,
				}),
			}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	srv.Network.SetARPEntries(json.RawMessage(`{
		"entries": [
			{"device": "br-lan", "ip": "192.168.8.130", "mac": "02:00:00:00:00:16"},
			{"device": "sta0", "ip": "192.168.100.1", "mac": "02:00:00:00:00:37"}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+arpEntriesDataSourceConfig, expectAttributes(arpEntriesAddress, map[string]knownvalue.Check{
		"entries": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"ip":     knownvalue.StringExact("192.168.8.130"),
				"mac":    knownvalue.StringExact("02:00:00:00:00:16"),
				"device": knownvalue.StringExact("br-lan"),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"ip":     knownvalue.StringExact("192.168.100.1"),
				"mac":    knownvalue.StringExact("02:00:00:00:00:37"),
				"device": knownvalue.StringExact("sta0"),
			}),
		}),
	}))
}

func TestARPEntriesDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Network.SetARPEntries(json.RawMessage(`{"entries": []}`))

	readDataSource(t, fakeRouterConfig(srv)+arpEntriesDataSourceConfig, expectAttributes(arpEntriesAddress, map[string]knownvalue.Check{
		"entries": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestARPEntriesDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), arpEntriesDataSourceConfig, "network", "get_arp_list", "Unable to Read ARP Entries")
}
