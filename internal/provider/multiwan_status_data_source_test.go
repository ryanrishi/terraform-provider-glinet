package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	multiWANStatusAddress          = "data.glinet_multiwan_status.test"
	multiWANStatusDataSourceConfig = `
data "glinet_multiwan_status" "test" {}
`
)

func multiWANInterfaceStatus(name string, ipv4, ipv6 knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"interface":   knownvalue.StringExact(name),
		"ipv4_status": ipv4,
		"ipv6_status": ipv6,
	})
}

func TestMultiWANStatusDataSource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.MultiWAN.SetStatus(json.RawMessage(`{
		"interfaces": [
			{"interface": "wan", "status_v4": 0, "status_v6": 1},
			{"interface": "wwan", "status_v4": 2, "status_v6": 3}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+multiWANStatusDataSourceConfig, expectAttributes(multiWANStatusAddress, map[string]knownvalue.Check{
		"interfaces": knownvalue.ListExact([]knownvalue.Check{
			multiWANInterfaceStatus("wan", knownvalue.StringExact("online"), knownvalue.StringExact("offline")),
			multiWANInterfaceStatus("wwan", knownvalue.StringExact("error"), knownvalue.Null()),
		}),
	}))
}

func TestMultiWANStatusDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.MultiWAN.SetStatus(json.RawMessage(`{"interfaces": []}`))

	readDataSource(t, fakeRouterConfig(srv)+multiWANStatusDataSourceConfig, expectAttributes(multiWANStatusAddress, map[string]knownvalue.Check{
		"interfaces": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestMultiWANStatusDataSourceReadError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	expectReadError(t, srv, multiWANStatusDataSourceConfig, "kmwan", "get_status", "Unable to Read Multi-WAN Status")
}
