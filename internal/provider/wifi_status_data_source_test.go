package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	wifiStatusAddress          = "data.glinet_wifi_status.test"
	wifiStatusDataSourceConfig = `
data "glinet_wifi_status" "test" {}
`
)

func TestWifiStatusDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+wifiStatusDataSourceConfig, expectAttributes(wifiStatusAddress, map[string]knownvalue.Check{
			"radios": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"name":    nonEmptyString,
					"state":   nonEmptyString,
					"channel": orNull(positiveInt64),
				}),
			}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	srv.Wifi.SetStatus(json.RawMessage(`{
		"res": [
			{"channel": 48, "name": "radio0", "state": "ready"},
			{"name": "radio1", "state": "disabled"}
		]
	}`))

	readDataSource(t, fakeRouterConfig(srv)+wifiStatusDataSourceConfig, expectAttributes(wifiStatusAddress, map[string]knownvalue.Check{
		"radios": knownvalue.ListExact([]knownvalue.Check{
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":    knownvalue.StringExact("radio0"),
				"state":   knownvalue.StringExact("ready"),
				"channel": knownvalue.Int64Exact(48),
			}),
			knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":    knownvalue.StringExact("radio1"),
				"state":   knownvalue.StringExact("disabled"),
				"channel": knownvalue.Null(),
			}),
		}),
	}))
}

func TestWifiStatusDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Wifi.SetStatus(json.RawMessage(`{"res": []}`))

	readDataSource(t, fakeRouterConfig(srv)+wifiStatusDataSourceConfig, expectAttributes(wifiStatusAddress, map[string]knownvalue.Check{
		"radios": knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestWifiStatusDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), wifiStatusDataSourceConfig, "wifi", "get_status", "Unable to Read Wi-Fi Status")
}
