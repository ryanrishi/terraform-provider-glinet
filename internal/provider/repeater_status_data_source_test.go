package provider

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	repeaterStatusAddress          = "data.glinet_repeater_status.test"
	repeaterStatusDataSourceConfig = `
data "glinet_repeater_status" "test" {}
`
)

func TestRepeaterStatusDataSource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	readDataSource(t, fakeRouterConfig(srv)+repeaterStatusDataSourceConfig, expectAttributes(repeaterStatusAddress, map[string]knownvalue.Check{
		"state":      knownvalue.StringExact("connected"),
		"state_code": knownvalue.Int64Exact(2),
		"running":    knownvalue.Bool(true),
		"fail_type":  knownvalue.Null(),
		"ssid":       knownvalue.StringExact("example"),
		"bssid":      knownvalue.StringExact("02:00:00:00:00:36"),
		"band":       knownvalue.StringExact("5g"),
		"channel":    knownvalue.Int64Exact(48),
		"htmode":     knownvalue.StringExact("HT40"),
		"dfs":        knownvalue.Bool(false),
		"signal":     knownvalue.Int64Exact(-53),
		"device":     knownvalue.StringExact("radio0"),
		"network":    knownvalue.StringExact("wwan"),
		"mac":        knownvalue.StringExact("02:00:00:00:00:38"),
		"connected":  knownvalue.StringExact("40m,13s"),
		"eap":        knownvalue.Bool(false),
		"portal":     knownvalue.Bool(false),
		"portal_url": knownvalue.Null(),
		"bare_mode":  knownvalue.Bool(false),
		"protocol":   knownvalue.StringExact("dhcp"),
		"ipv4": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"ip":      knownvalue.StringExact("192.0.2.20/24"),
			"gateway": knownvalue.StringExact("192.0.2.1"),
			"dns":     knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("192.0.2.1")}),
		}),
	}))
}

// TestRepeaterStatusDataSourceNoSecrets checks that the Wi-Fi key, the EAP identity and wpa_status are not in the state.
func TestRepeaterStatusDataSourceNoSecrets(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetStatus(json.RawMessage(`{
		"state": 2,
		"state_s": "connected",
		"ssid": "example-eap",
		"eap": true,
		"config": {"ssid": "example-eap", "key": "example-password", "identity": "example-user", "protocol": "dhcp"},
		"wpa_status": {"identity": "example-user", "key_mgmt": "WPA2/IEEE 802.1X/EAP"}
	}`))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + repeaterStatusDataSourceConfig,
				ConfigStateChecks: expectAttributes(repeaterStatusAddress, map[string]knownvalue.Check{
					"ssid":     knownvalue.StringExact("example-eap"),
					"eap":      knownvalue.Bool(true),
					"protocol": knownvalue.StringExact("dhcp"),
				}),
				Check: expectNoStateValue(repeaterStatusAddress, "example-password", "example-user"),
			},
		},
	})
}

// expectNoStateValue checks that no attribute of address has one of the values.
func expectNoStateValue(address string, values ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		r, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("%s is not in the state", address)
		}
		for name, value := range r.Primary.Attributes {
			if slices.Contains(values, value) {
				return fmt.Errorf("attribute %s of %s has the value %q", name, address, value)
			}
		}
		return nil
	}
}

func TestRepeaterStatusDataSourceNotConnected(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetStatus(json.RawMessage(`{"state": 0}`))

	readDataSource(t, fakeRouterConfig(srv)+repeaterStatusDataSourceConfig, expectAttributes(repeaterStatusAddress, map[string]knownvalue.Check{
		"state":      knownvalue.Null(),
		"state_code": knownvalue.Int64Exact(0),
		"running":    knownvalue.Bool(false),
		"ssid":       knownvalue.Null(),
		"channel":    knownvalue.Null(),
		"signal":     knownvalue.Null(),
		"mac":        knownvalue.Null(),
		"protocol":   knownvalue.Null(),
		"ipv4":       knownvalue.Null(),
	}))
}

func TestRepeaterStatusDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), repeaterStatusDataSourceConfig, "repeater", "get_status", "Unable to Read Repeater Status")
}
