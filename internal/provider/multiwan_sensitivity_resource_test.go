package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const multiWANSensitivityAddress = "glinet_multiwan_sensitivity.test"

func multiWANSensitivityConfig(attributes string) string {
	return "resource \"glinet_multiwan_sensitivity\" \"test\" {\n" + attributes + "}\n"
}

func expectMultiWANSensitivity(level string, interval float64) []statecheck.StateCheck {
	return expectAttributes(multiWANSensitivityAddress, map[string]knownvalue.Check{
		"id":       knownvalue.StringExact("multiwan_sensitivity"),
		"level":    knownvalue.StringExact(level),
		"interval": knownvalue.Float64Exact(interval),
	})
}

// TestMultiWANSensitivityResource changes the level and the interval. Each step checks the params of the write.
func TestMultiWANSensitivityResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"high\"\n"),
				ConfigStateChecks: expectMultiWANSensitivity("high", 5),
				Check:             setSensitivity.expect(`{"sensitivity":{"level":"high","val":5}}`),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"custom\"\n  interval = 0.5\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANSensitivityAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANSensitivity("custom", 0.5),
				Check:             setSensitivity.expect(`{"sensitivity":{"level":"custom","val":0.5}}`),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"custom\"\n  interval = 90\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANSensitivityAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANSensitivity("custom", 90),
				Check:             setSensitivity.expect(`{"sensitivity":{"level":"custom","val":90}}`),
			},
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"low\"\n"),
				ConfigPlanChecks:  expectResourceAction(multiWANSensitivityAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMultiWANSensitivity("low", 90),
				Check:             setSensitivity.expect(`{"sensitivity":{"level":"low","val":90}}`),
			},
			{
				ResourceName:      multiWANSensitivityAddress,
				ImportState:       true,
				ImportStateId:     "multiwan_sensitivity",
				ImportStateVerify: true,
			},
		},
	})
}

// TestMultiWANSensitivityResourceNoWrite takes over a router whose sensitivity is equal to the configuration:
// create sends no write.
func TestMultiWANSensitivityResourceNoWrite(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"medium\"\n"),
				ConfigStateChecks: expectMultiWANSensitivity("medium", 5),
				Check:             setSensitivity.expect(),
			},
		},
	})
}

// withoutSensitivity makes the fake router give a get_sensitivity result without sensitivity until a write sets it.
func withoutSensitivity(srv *glinettest.Server) {
	srv.MultiWAN.SetSensitivity(glinettest.MultiWANSensitivity{})
	next := srv.Handler("kmwan", "get_sensitivity")
	srv.Handle("kmwan", "get_sensitivity", func(params json.RawMessage) (any, error) {
		if srv.MultiWAN.Sensitivity() == (glinettest.MultiWANSensitivity{}) {
			return map[string]any{}, nil
		}
		return next(params)
	})
}

// TestMultiWANSensitivityResourceNoSensitivity reads a router result without sensitivity: the state and the import
// get the web UI defaults, and a create with the default level sends no write.
func TestMultiWANSensitivityResourceNoSensitivity(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	withoutSensitivity(srv)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"medium\"\n"),
				ConfigStateChecks: expectMultiWANSensitivity("medium", 5),
				Check:             setSensitivity.expect(),
			},
			{
				ResourceName:      multiWANSensitivityAddress,
				ImportState:       true,
				ImportStateId:     "multiwan_sensitivity",
				ImportStateVerify: true,
			},
		},
	})
}

// TestMultiWANSensitivityResourceNoSensitivityWrite writes a level without interval to a router result without
// sensitivity: the write sends the default interval of the web UI.
func TestMultiWANSensitivityResourceNoSensitivityWrite(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	withoutSensitivity(srv)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"high\"\n"),
				ConfigStateChecks: expectMultiWANSensitivity("high", 5),
				Check:             setSensitivity.expect(`{"sensitivity":{"level":"high","val":5}}`),
			},
		},
	})
}

func TestMultiWANSensitivityResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")
	config := fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"custom\"\n  interval = 10\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  setSensitivity.expect(`{"sensitivity":{"level":"custom","val":10}}`),
			},
			{
				PreConfig:        func() { srv.MultiWAN.SetSensitivity(glinettest.MultiWANSensitivity{Level: "custom", Val: 20}) },
				Config:           config,
				ConfigPlanChecks: expectResourceAction(multiWANSensitivityAddress, plancheck.ResourceActionUpdate),
				Check:            setSensitivity.expect(`{"sensitivity":{"level":"custom","val":10}}`),
			},
		},
	})
}

func TestMultiWANSensitivityResourceErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setSensitivity := recordCalls(t, srv, "kmwan", "set_sensitivity")

	tests := map[string]struct {
		attributes, err string
	}{
		"interval with level high": {"  level = \"high\"\n  interval = 5\n", "interval cannot be set when level is not custom."},
		"interval 1.5":             {"  level = \"custom\"\n  interval = 1.5\n", "must be 0.5 or a whole number from 1 to 90, got: 1.5"},
		"interval 91":              {"  level = \"custom\"\n  interval = 91\n", "must be 0.5 or a whole number from 1 to 90, got: 91"},
		"interval 0":               {"  level = \"custom\"\n  interval = 0\n", "must be 0.5 or a whole number from 1 to 90, got: 0"},
		"unknown level":            {"  level = \"max\"\n", `value must be one of: ["low" "medium" "high" "custom"]`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + multiWANSensitivityConfig(tt.attributes),
						ExpectError: regexp.MustCompile(wrapped(tt.err)),
					},
				},
			})
		})
	}
	if got := setSensitivity.take(); len(got) != 0 {
		t.Errorf("kmwan.set_sensitivity was called with %q", got)
	}
}

func TestMultiWANSensitivityResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + multiWANSensitivityConfig("  level = \"low\"\n"),
				ResourceName:  multiWANSensitivityAddress,
				ImportState:   true,
				ImportStateId: "sensitivity",
				ExpectError:   regexp.MustCompile(wrapped(`The import ID of glinet_multiwan_sensitivity must be "multiwan_sensitivity", got "sensitivity".`)),
			},
		},
	})
}

func TestMultiWANSensitivityResourceReadError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	expectReadError(t, srv, multiWANSensitivityConfig("  level = \"low\"\n"), "kmwan", "get_sensitivity", "Unable to Read Multi-WAN Sensitivity")
}
