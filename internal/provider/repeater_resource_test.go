package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const repeaterAddress = "glinet_repeater.test"

func expectRepeaterState(switchingMode knownvalue.Check, lockBand string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(repeaterAddress, tfjsonpath.New("id"), knownvalue.StringExact("repeater")),
		statecheck.ExpectKnownValue(repeaterAddress, tfjsonpath.New("switching_mode"), switchingMode),
		statecheck.ExpectKnownValue(repeaterAddress, tfjsonpath.New("lock_band"), knownvalue.StringExact(lockBand)),
	}
}

func TestRepeaterResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "repeater", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "auto_switching_without_network"
  lock_band      = "5g"
}
`,
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("auto_switching_without_network"), "5g"),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"auto":2,"lock_band":"5g"}`),
					expectRouterJSON(t, srv.Repeater.Config, `{"auto":2,"lock_band":"5g","dfs_support":true,"macaddr":"02:00:00:00:00:38"}`),
				),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "no_switching"
  lock_band      = ""
}
`,
				ConfigPlanChecks:  expectResourceAction(repeaterAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("no_switching"), ""),
				Check:             calls.expect(`{"auto":0}`),
			},
			{
				ResourceName:      repeaterAddress,
				ImportState:       true,
				ImportStateId:     "repeater",
				ImportStateVerify: true,
			},
		},
	})
}

// TestRepeaterResourceUnsetAttributes checks that the write sends the router value of each attribute that is not set.
func TestRepeaterResourceUnsetAttributes(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetConfig(json.RawMessage(`{"auto":0,"lock_band":"2g","dfs_support":false,"macaddr":"02:00:00:00:00:38"}`))
	calls := recordCalls(t, srv, "repeater", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + `resource "glinet_repeater" "test" {}`,
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("no_switching"), "2g"),
				Check:             calls.expect(`{"auto":0,"lock_band":"2g"}`),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "auto_switching"
}
`,
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("auto_switching"), "2g"),
				Check:             calls.expect(`{"auto":1,"lock_band":"2g"}`),
			},
			{
				PreConfig: func() {
					srv.Repeater.SetConfig(json.RawMessage(`{"auto":1}`))
				},
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  lock_band = "5g"
}
`,
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("auto_switching"), "5g"),
				Check:             calls.expect(`{"auto":1,"lock_band":"5g"}`),
			},
		},
	})
}

// TestRepeaterResourceUnknownMode reads a switching mode code that has no name, which the write sends back.
func TestRepeaterResourceUnknownMode(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetConfig(json.RawMessage(`{"auto":3}`))
	calls := recordCalls(t, srv, "repeater", "set_config")
	config := fakeRouterConfig(srv) + `resource "glinet_repeater" "test" {}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: expectRepeaterState(knownvalue.Null(), ""),
				Check:             calls.expect(`{"auto":3}`),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestRepeaterResourceImportBoolMode imports the configuration of firmware 4.7.0, which gives auto as a bool.
func TestRepeaterResourceImportBoolMode(t *testing.T) {
	parallel(t)
	tests := map[string]string{
		"true":  "auto_switching",
		"false": "no_switching",
	}

	for auto, mode := range tests {
		t.Run(auto, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Repeater.SetConfig(json.RawMessage(`{"auto":` + auto + `,"dfs_support":true,"macaddr":"02:00:00:00:00:38"}`))
			config := fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "` + mode + `"
  lock_band      = ""
}
`

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       repeaterAddress,
						ImportState:        true,
						ImportStateId:      "repeater",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectRepeaterState(knownvalue.StringExact(mode), ""),
					},
				},
			})
		})
	}
}

func TestRepeaterResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + `resource "glinet_repeater" "test" {}`,
				ResourceName:  repeaterAddress,
				ImportState:   true,
				ImportStateId: "wwan",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestRepeaterResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "auto_switching"
  lock_band      = "5g"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { srv.Repeater.SetConfig(json.RawMessage(`{"auto":0,"lock_band":"2g"}`)) },
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(repeaterAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("auto_switching"), "5g"),
				Check:             expectRouterJSON(t, srv.Repeater.Config, `{"auto":1,"lock_band":"5g"}`),
			},
		},
	})
}

// TestRepeaterResourceNotApplied checks the error when the router keeps a band lock after a write without lock_band.
func TestRepeaterResourceNotApplied(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Repeater.SetConfig(json.RawMessage(`{"auto":1,"lock_band":"5g"}`))
	srv.Handle("repeater", "set_config", func(json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  lock_band = ""
}
`,
				ExpectError: regexp.MustCompile(`(?s)Repeater Configuration Not Applied.*` + wrapped(`the router gives auto 1 and lock_band "5g" after the write`)),
			},
		},
	})
}

func TestRepeaterResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_repeater" "test" {
  switching_mode = "auto"
}
`,
				ExpectError: regexp.MustCompile(`(?s)switching_mode.*value must be one of`),
			},
		},
	})
}

func TestRepeaterResourceRouterErrors(t *testing.T) {
	parallel(t)
	config := `
resource "glinet_repeater" "test" {
  switching_mode = "no_switching"
}
`

	t.Run("get before set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		calls := recordCalls(t, srv, "repeater", "set_config")
		failFirstCall(srv, "repeater", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Repeater Configuration.*Method not found`),
				},
			},
		})

		if got := calls.take(); len(got) != 0 {
			t.Errorf("repeater.set_config was called with %q", got)
		}
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("repeater", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig:   failRouterMethod(srv, "repeater", "get_config"),
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Repeater Configuration.*Method not found`),
				},
				{
					PreConfig:         func() { srv.Handle("repeater", "get_config", get) },
					Config:            fakeRouterConfig(srv) + config,
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: expectRepeaterState(knownvalue.StringExact("no_switching"), ""),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "repeater", "set_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Repeater Configuration.*Method not found`),
				},
			},
		})

		expectJSONEqual(t, srv.Repeater.Config(), `{"auto":1,"dfs_support":true,"macaddr":"02:00:00:00:00:38"}`)
	})
}
