package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	tetheringStatusAddress          = "data.glinet_tethering_status.test"
	tetheringStatusDataSourceConfig = `
data "glinet_tethering_status" "test" {}
`
)

// TestTetheringStatusDataSource reads the status of the test router, which had no phone on USB.
func TestTetheringStatusDataSource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	readDataSource(t, fakeRouterConfig(srv)+tetheringStatusDataSourceConfig, expectAttributes(tetheringStatusAddress, map[string]knownvalue.Check{
		"status":      knownvalue.StringExact("connecting"),
		"status_code": knownvalue.Int64Exact(2),
		"devices":     knownvalue.ListExact([]knownvalue.Check{}),
		"ipv4":        knownvalue.Null(),
	}))
}

func TestTetheringStatusDataSourceConnected(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Tethering.SetStatus(json.RawMessage(`{
		"status": 1,
		"devices": [
			{"device": "usb0", "type": 0, "use": true},
			{"device": "eth2", "type": 1, "use": false},
			{"device": "usb1", "use": false},
			{"device": "usb2", "type": 5, "use": false}
		],
		"ipv4": {"ip": "198.51.100.20/24", "gateway": "198.51.100.1", "dns": ["198.51.100.1"]}
	}`))

	device := func(name string, deviceType knownvalue.Check, use bool) knownvalue.Check {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{
			"device": knownvalue.StringExact(name),
			"type":   deviceType,
			"use":    knownvalue.Bool(use),
		})
	}

	readDataSource(t, fakeRouterConfig(srv)+tetheringStatusDataSourceConfig, expectAttributes(tetheringStatusAddress, map[string]knownvalue.Check{
		"status":      knownvalue.StringExact("connected"),
		"status_code": knownvalue.Int64Exact(1),
		"devices": knownvalue.ListExact([]knownvalue.Check{
			device("usb0", knownvalue.StringExact("android"), true),
			device("eth2", knownvalue.StringExact("ios"), false),
			device("usb1", knownvalue.Null(), false),
			device("usb2", knownvalue.Null(), false),
		}),
		"ipv4": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"ip":      knownvalue.StringExact("198.51.100.20/24"),
			"gateway": knownvalue.StringExact("198.51.100.1"),
			"dns":     knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("198.51.100.1")}),
		}),
	}))
}

func TestTetheringStatusDataSourceUnknownStatus(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Tethering.SetStatus(json.RawMessage(`{"status": 7, "devices": []}`))

	readDataSource(t, fakeRouterConfig(srv)+tetheringStatusDataSourceConfig, expectAttributes(tetheringStatusAddress, map[string]knownvalue.Check{
		"status":      knownvalue.Null(),
		"status_code": knownvalue.Int64Exact(7),
	}))
}

func TestTetheringStatusDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), tetheringStatusDataSourceConfig, "tethering", "get_status", "Unable to Read Tethering Status")
}
