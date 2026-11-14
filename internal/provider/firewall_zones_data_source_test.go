package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	firewallZonesAddress          = "data.glinet_firewall_zones.test"
	firewallZonesDataSourceConfig = `
data "glinet_firewall_zones" "test" {}
`
)

func TestFirewallZonesDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+firewallZonesDataSourceConfig, expectAttributes(firewallZonesAddress, map[string]knownvalue.Check{
			"internal_zones": knownvalue.ListPartial(map[int]knownvalue.Check{0: nonEmptyString}),
			"external_zones": knownvalue.ListPartial(map[int]knownvalue.Check{0: nonEmptyString}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	srv.Firewall.SetZones(json.RawMessage(`{"internals": ["lan", "guest"], "externals": ["wan", "wgclient"]}`))

	readDataSource(t, fakeRouterConfig(srv)+firewallZonesDataSourceConfig, expectAttributes(firewallZonesAddress, map[string]knownvalue.Check{
		"internal_zones": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("lan"), knownvalue.StringExact("guest")}),
		"external_zones": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("wan"), knownvalue.StringExact("wgclient")}),
	}))
}

func TestFirewallZonesDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Firewall.SetZones(json.RawMessage(`{"internals": []}`))

	readDataSource(t, fakeRouterConfig(srv)+firewallZonesDataSourceConfig, expectAttributes(firewallZonesAddress, map[string]knownvalue.Check{
		"internal_zones": knownvalue.ListExact([]knownvalue.Check{}),
		"external_zones": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestFirewallZonesDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), firewallZonesDataSourceConfig, "firewall", "get_zone_list", "Unable to Read Firewall Zones")
}
