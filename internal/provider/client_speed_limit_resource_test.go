package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const speedLimitAddress = "glinet_client_speed_limit.test"

func speedLimitConfig(mac, attributes string) string {
	return fmt.Sprintf(`
resource "glinet_client_speed_limit" "test" {
  mac = %q
  %s
}
`, mac, attributes)
}

func expectSpeedLimit(download, upload float64) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(speedLimitAddress, tfjsonpath.New("download"), knownvalue.Float64Exact(download)),
		statecheck.ExpectKnownValue(speedLimitAddress, tfjsonpath.New("upload"), knownvalue.Float64Exact(upload)),
	}
}

func routerSpeedLimit(download, upload float64) string {
	return fmt.Sprintf(`{"clients": [{"mac": %q, "name": "host-1", "limit_rx": %g, "limit_tx": %g}]}`, testClientMAC, download, upload)
}

func TestClientSpeedLimitResource(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	sets := recordCalls(t, srv, "qos", "set_speed_limit_rule")
	removes := recordCalls(t, srv, "qos", "remove_speed_limit_rule")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			removes.expect(fmt.Sprintf(`{"mac": %q}`, testClientMAC)),
			expectRouterJSON(t, srv.Clients.List, routerSpeedLimit(0, 0)),
		),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + speedLimitConfig(testClientMAC, `download = 1024`),
				ConfigStateChecks: expectSpeedLimit(1024, 0),
				Check: resource.ComposeTestCheckFunc(
					sets.expect(fmt.Sprintf(`{"mac": %q, "download": 1024, "upload": 0}`, testClientMAC)),
					expectRouterJSON(t, srv.Clients.List, routerSpeedLimit(1024, 0)),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + speedLimitConfig(testClientMAC, `download = 1.125`+"\n"+`upload = 1073741824`),
				ConfigPlanChecks:  expectResourceAction(speedLimitAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSpeedLimit(1.125, 1073741824),
				Check: resource.ComposeTestCheckFunc(
					sets.expect(fmt.Sprintf(`{"mac": %q, "download": 1.125, "upload": 1073741824}`, testClientMAC)),
					removes.expect(),
				),
			},
			{
				ResourceName:                         speedLimitAddress,
				ImportState:                          true,
				ImportStateId:                        testClientMACUp,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "mac",
			},
		},
	})
}

func TestClientSpeedLimitResourceLetterCase(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	sets := recordCalls(t, srv, "qos", "set_speed_limit_rule")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + speedLimitConfig(testClientMACUp, `upload = 8`),
				ConfigStateChecks: append(expectSpeedLimit(0, 8),
					statecheck.ExpectKnownValue(speedLimitAddress, tfjsonpath.New("mac"), knownvalue.StringExact(testClientMACUp))),
				Check: sets.expect(fmt.Sprintf(`{"mac": %q, "download": 0, "upload": 8}`, testClientMAC)),
			},
		},
	})
}

// TestClientSpeedLimitResourceRounding checks that a router that rounds a fraction gives no difference.
func TestClientSpeedLimitResourceRounding(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	set := srv.Handler("qos", "set_speed_limit_rule")
	srv.Handle("qos", "set_speed_limit_rule", func(params json.RawMessage) (any, error) {
		var p glinet.SpeedLimit
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		p.Download = math.Round(p.Download)
		p.Upload = math.Round(p.Upload)
		rounded, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		return set(rounded)
	})
	config := fakeRouterConfig(srv) + speedLimitConfig(testClientMAC, `download = 1.125`+"\n"+`upload = 2.875`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: expectSpeedLimit(1.125, 2.875),
				Check:             expectRouterJSON(t, srv.Clients.List, routerSpeedLimit(1, 3)),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectSpeedLimit(1.125, 2.875),
			},
		},
	})
}

func TestClientSpeedLimitResourceDrift(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	sets := recordCalls(t, srv, "qos", "set_speed_limit_rule")
	config := fakeRouterConfig(srv) + speedLimitConfig(testClientMAC, `download = 100`+"\n"+`upload = 50`)
	want := fmt.Sprintf(`{"mac": %q, "download": 100, "upload": 50}`, testClientMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  sets.expect(want),
			},
			{
				PreConfig:         func() { srv.Clients.SetList(json.RawMessage(routerSpeedLimit(100, 20))) },
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(speedLimitAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSpeedLimit(100, 50),
				Check:             sets.expect(want),
			},
			{
				PreConfig: func() {
					if err := client.QoS.RemoveSpeedLimit(context.Background(), testClientMAC); err != nil {
						t.Fatal(err)
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(speedLimitAddress, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSpeedLimit(100, 50),
				Check:             sets.expect(want),
			},
		},
	})
}

func TestClientSpeedLimitResourceCreateErrors(t *testing.T) {
	parallel(t)

	for name, test := range map[string]struct {
		upload      float64
		mac         string
		ignoreWrite bool
		error       string
	}{
		"client not in list":   {mac: missingClientMAC, error: `Client Not Found`},
		"client has a limit":   {upload: 64, mac: testClientMAC, error: `Client Speed Limit Already Exists`},
		"router ignores write": {mac: testClientMAC, ignoreWrite: true, error: `Client Speed Limit Not Found`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Clients.SetList(json.RawMessage(routerSpeedLimit(0, test.upload)))
			if test.ignoreWrite {
				srv.Handle("qos", "set_speed_limit_rule", func(json.RawMessage) (any, error) { return nil, nil })
			}
			sets := recordCalls(t, srv, "qos", "set_speed_limit_rule")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + speedLimitConfig(test.mac, `download = 10`),
						ExpectError: regexp.MustCompile(test.error),
					},
				},
			})
			if got := sets.take(); !test.ignoreWrite && len(got) != 0 {
				t.Errorf("set_speed_limit_rule calls %q, want none", got)
			}
		})
	}
}

func TestClientSpeedLimitResourceInvalidValues(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")

	for attributes, want := range map[string]string{
		`download = 0.5`:                     `Invalid Attribute Value`,
		`download = 1.1`:                     `Invalid Attribute Value`,
		`upload = 1073741825`:                `Invalid Attribute Value`,
		`download = -1`:                      `Invalid Attribute Value`,
		``:                                   `No Speed Limit`,
		`download = 0` + "\n" + `upload = 0`: `No Speed Limit`,
	} {
		t.Run(attributes, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + speedLimitConfig(testClientMAC, attributes),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(want),
					},
				},
			})
		})
	}
}
