package provider

import (
	"encoding/json"
	"errors"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const timezoneConfigAddress = "data.glinet_system_timezone_config.test"

func TestSystemTimezoneConfigDataSource(t *testing.T) {
	var config string
	var checks []statecheck.StateCheck

	if realRouter() {
		config = `provider "glinet" {}` + timezoneConfigDataSourceConfig
		checks = realRouterTimezoneConfigChecks()
	} else {
		srv := glinettest.NewServer(t)
		srv.System.SetTimezoneConfig(glinettest.TimezoneConfig{
			Zonename:            "America/New York",
			Timezone:            "EST5EDT,M3.2.0,M11.1.0",
			TZOffset:            "-0500",
			AutoTimezoneEnabled: true,
			Localtime:           1794499485,
		})
		config = fakeRouterConfig(srv) + timezoneConfigDataSourceConfig
		checks = []statecheck.StateCheck{
			expectTimezoneConfig("id", knownvalue.StringExact("EST5EDT,M3.2.0,M11.1.0")),
			expectTimezoneConfig("zonename", knownvalue.StringExact("America/New York")),
			expectTimezoneConfig("timezone", knownvalue.StringExact("EST5EDT,M3.2.0,M11.1.0")),
			expectTimezoneConfig("tzoffset", knownvalue.StringExact("-0500")),
			expectTimezoneConfig("auto_timezone_enabled", knownvalue.Bool(true)),
			expectTimezoneConfig("localtime", knownvalue.Int64Exact(1794499485)),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: checks,
			},
		},
	})
}

func TestSystemTimezoneConfigDataSourceAPIError(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.Handle("system", "get_timezone_config", func(json.RawMessage) (any, error) {
		return nil, glinettest.ErrMethodNotFound
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + timezoneConfigDataSourceConfig,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read System Timezone Config.*Method not found`),
			},
		},
	})
}

func TestSystemTimezoneConfigDataSourceSessionExpired(t *testing.T) {
	srv := glinettest.NewServer(t)

	var expired atomic.Bool
	srv.Handle("system", "get_timezone_config", func(json.RawMessage) (any, error) {
		if expired.CompareAndSwap(false, true) {
			srv.ExpireSessions()
			return nil, glinettest.ErrAccessDenied
		}
		return srv.System.TimezoneConfig(), nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + timezoneConfigDataSourceConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezoneConfig("timezone", knownvalue.StringExact(srv.System.TimezoneConfig().Timezone)),
				},
			},
		},
	})

	if !expired.Load() {
		t.Fatal("the session did not expire during the test")
	}
}

func expectTimezoneConfig(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(timezoneConfigAddress, tfjsonpath.New(attribute), value)
}

// realRouterTimezoneConfigChecks checks only types and that values are not empty, because the router values are not fixed.
// zonename is empty when the router uses UTC.
func realRouterTimezoneConfigChecks() []statecheck.StateCheck {
	notEmpty := knownvalue.StringFunc(func(v string) error {
		if v == "" {
			return errors.New("value is empty")
		}
		return nil
	})

	return []statecheck.StateCheck{
		expectTimezoneConfig("id", notEmpty),
		expectTimezoneConfig("zonename", knownvalue.StringFunc(func(string) error { return nil })),
		expectTimezoneConfig("timezone", notEmpty),
		expectTimezoneConfig("tzoffset", notEmpty),
		expectTimezoneConfig("auto_timezone_enabled", knownvalue.BoolFunc(func(bool) error { return nil })),
		expectTimezoneConfig("localtime", knownvalue.Int64Func(func(v int64) error {
			if v <= 0 {
				return errors.New("value is not positive")
			}
			return nil
		})),
	}
}
