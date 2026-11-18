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

const timezoneDataSourceAddress = "data.glinet_system_timezone.test"

func TestSystemTimezoneDataSource(t *testing.T) {
	parallel(t)
	var config string
	var checks []statecheck.StateCheck

	if realRouter() {
		config = `provider "glinet" {}` + timezoneDataSourceConfig
		checks = realRouterTimezoneDataSourceChecks()
	} else {
		srv := glinettest.NewServer(t)
		srv.System.SetTimezoneConfig(glinettest.TimezoneConfig{
			Zonename:            "Europe/Berlin",
			Timezone:            "CET-1CEST,M3.5.0,M10.5.0/3",
			TZOffset:            "+0100",
			AutoTimezoneEnabled: true,
			Localtime:           1794499485,
		})
		config = fakeRouterConfig(srv) + timezoneDataSourceConfig
		checks = []statecheck.StateCheck{
			expectTimezoneDataSource("zonename", knownvalue.StringExact("Europe/Berlin")),
			expectTimezoneDataSource("timezone", knownvalue.StringExact("CET-1CEST,M3.5.0,M10.5.0/3")),
			expectTimezoneDataSource("tzoffset", knownvalue.StringExact("+0100")),
			expectTimezoneDataSource("auto_timezone_enabled", knownvalue.Bool(true)),
			expectTimezoneDataSource("localtime", knownvalue.Int64Exact(1794499485)),
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

func TestSystemTimezoneDataSourceAPIError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("system", "get_timezone_config", func(json.RawMessage) (any, error) {
		return nil, glinettest.ErrMethodNotFound
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + timezoneDataSourceConfig,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read System Timezone.*Method not found`),
			},
		},
	})
}

func TestSystemTimezoneDataSourceSessionExpired(t *testing.T) {
	parallel(t)
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
				Config: fakeRouterConfig(srv) + timezoneDataSourceConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezoneDataSource("timezone", knownvalue.StringExact(srv.System.TimezoneConfig().Timezone)),
				},
			},
		},
	})

	if !expired.Load() {
		t.Fatal("the session did not expire during the test")
	}
}

func expectTimezoneDataSource(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(timezoneDataSourceAddress, tfjsonpath.New(attribute), value)
}

// realRouterTimezoneDataSourceChecks checks only types and that values are not empty, because the router values are not fixed.
// zonename is empty when the router uses UTC.
func realRouterTimezoneDataSourceChecks() []statecheck.StateCheck {
	notEmpty := knownvalue.StringFunc(func(v string) error {
		if v == "" {
			return errors.New("value is empty")
		}
		return nil
	})

	return []statecheck.StateCheck{
		expectTimezoneDataSource("zonename", knownvalue.StringFunc(func(string) error { return nil })),
		expectTimezoneDataSource("timezone", notEmpty),
		expectTimezoneDataSource("tzoffset", notEmpty),
		expectTimezoneDataSource("auto_timezone_enabled", knownvalue.BoolFunc(func(bool) error { return nil })),
		expectTimezoneDataSource("localtime", knownvalue.Int64Func(func(v int64) error {
			if v <= 0 {
				return errors.New("value is not positive")
			}
			return nil
		})),
	}
}
