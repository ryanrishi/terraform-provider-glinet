package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	parentalControlAppsAddress          = "data.glinet_parental_control_apps.test"
	parentalControlAppsDataSourceConfig = `
data "glinet_parental_control_apps" "test" {}
`
)

func appCheck(id int64, name string, appType int64) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":   knownvalue.Int64Exact(id),
		"name": knownvalue.StringExact(name),
		"type": knownvalue.Int64Exact(appType),
	})
}

func TestParentalControlAppsDataSource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	readDataSource(t, fakeRouterConfig(srv)+parentalControlAppsDataSourceConfig, expectAttributes(parentalControlAppsAddress, map[string]knownvalue.Check{
		"apps": knownvalue.ListExact([]knownvalue.Check{
			appCheck(1001, "QQ", 1),
			appCheck(4054, "Lazada", 4),
			appCheck(7031, "TeamViewer", 7),
			appCheck(8009, "hao123", 8),
		}),
	}))
}

func TestParentalControlAppsDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), parentalControlAppsDataSourceConfig, "parental-control", "get_app_list", "Unable to Read Parental Control Apps")
}
