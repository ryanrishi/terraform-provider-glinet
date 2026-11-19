package provider

import (
	"context"
	"encoding/json"
	"fmt"
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

const (
	clientInfoAddress = "glinet_client_info.test"
	testClientMAC     = "02:00:00:00:00:c1"
	testClientMACUp   = "02:00:00:00:00:C1"
	missingClientMAC  = "02:00:00:00:00:c9"
)

// clientListServer returns a fake router whose client list has one client with the MAC address testClientMAC
// and the extra JSON fields fields, for example `"class": "phone",`.
func clientListServer(t *testing.T, fields string) *glinettest.Server {
	srv := glinettest.NewServer(t)
	srv.Clients.SetList(json.RawMessage(fmt.Sprintf(`{"clients": [{%s "mac": %q, "name": "host-1", "limit_rx": 0, "limit_tx": 0}]}`, fields, testClientMAC)))
	return srv
}

func clientInfoConfig(mac, attributes string) string {
	return fmt.Sprintf(`
resource "glinet_client_info" "test" {
  mac = %q
  %s
}
`, mac, attributes)
}

func expectClientInfo(alias, class string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(clientInfoAddress, tfjsonpath.New("alias"), knownvalue.StringExact(alias)),
		statecheck.ExpectKnownValue(clientInfoAddress, tfjsonpath.New("class"), knownvalue.StringExact(class)),
	}
}

func TestClientInfoResource(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, `"class": "phone",`)
	writes := recordCalls(t, srv, "clients", "set_info")
	routerList := func(fields string) string {
		return fmt.Sprintf(`{"clients": [{%s "mac": %q, "name": "host-1", "limit_rx": 0, "limit_tx": 0}]}`, fields, testClientMAC)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			writes.expect(),
			expectRouterJSON(t, srv.Clients.List, routerList(`"alias": "tablet", "class": "pad",`)),
		),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`),
				ConfigStateChecks: expectClientInfo("tablet", "phone"),
				Check: resource.ComposeTestCheckFunc(
					writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": "phone"}`, testClientMAC)),
					expectRouterJSON(t, srv.Clients.List, routerList(`"alias": "tablet", "class": "phone",`)),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`+"\n"+`class = ""`),
				ConfigPlanChecks:  expectResourceAction(clientInfoAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectClientInfo("tablet", ""),
				Check: resource.ComposeTestCheckFunc(
					writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": ""}`, testClientMAC)),
					expectRouterJSON(t, srv.Clients.List, routerList(`"alias": "tablet",`)),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `class = "pad"`),
				ConfigStateChecks: expectClientInfo("tablet", "pad"),
				Check:             writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": "pad"}`, testClientMAC)),
			},
			{
				ResourceName:                         clientInfoAddress,
				ImportState:                          true,
				ImportStateId:                        testClientMACUp,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "mac",
			},
		},
	})
}

func TestClientInfoResourceLetterCase(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	writes := recordCalls(t, srv, "clients", "set_info")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + clientInfoConfig(testClientMACUp, `alias = "tablet"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(clientInfoAddress, tfjsonpath.New("mac"), knownvalue.StringExact(testClientMACUp)),
				},
				Check: writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": ""}`, testClientMAC)),
			},
			{
				Config:           fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`),
				ConfigPlanChecks: expectResourceAction(clientInfoAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(clientInfoAddress, tfjsonpath.New("mac"), knownvalue.StringExact(testClientMAC)),
				},
				Check: writes.expect(),
			},
		},
	})
}

// twoLetterCasesServer returns a fake router whose client list has testClientMAC and testClientMACUp as two clients.
func twoLetterCasesServer(t *testing.T) *glinettest.Server {
	srv := glinettest.NewServer(t)
	srv.Clients.SetList(json.RawMessage(fmt.Sprintf(`{"clients": [
		{"mac": %q, "name": "host-1", "alias": "lower", "limit_rx": 0, "limit_tx": 0},
		{"mac": %q, "name": "host-2", "alias": "upper", "limit_rx": 0, "limit_tx": 0}
	]}`, testClientMAC, testClientMACUp)))
	return srv
}

func TestClientInfoResourceTwoLetterCases(t *testing.T) {
	parallel(t)
	srv := twoLetterCasesServer(t)
	writes := recordCalls(t, srv, "clients", "set_info")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`),
				ConfigStateChecks: expectClientInfo("tablet", ""),
				Check:             writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": ""}`, testClientMAC)),
			},
			{
				Config:      fakeRouterConfig(srv) + clientInfoConfig(testClientMACUp, `alias = "tablet"`),
				ExpectError: regexp.MustCompile(`Ambiguous Letter Case of MAC Address`),
			},
			{
				Config:      fakeRouterConfig(srv) + clientInfoConfig(testClientMACUp, `alias = "phone"`),
				ExpectError: regexp.MustCompile(`Ambiguous Letter Case of MAC Address`),
			},
			{
				Config: fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`),
				Check:  writes.expect(),
			},
		},
	})
}

// TestClientInfoResourceEmptyValues checks that an empty alias and class of the router are the same as no alias and no class.
func TestClientInfoResourceEmptyValues(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, `"alias": "", "class": "",`)
	srv.Handle("clients", "set_info", func(json.RawMessage) (any, error) { return nil, nil })

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = ""`+"\n"+`class = ""`),
				ConfigStateChecks: expectClientInfo("", ""),
			},
			{
				Config:            fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, ""),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectClientInfo("", ""),
			},
		},
	})
}

func TestClientInfoResourceDrift(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	writes := recordCalls(t, srv, "clients", "set_info")
	config := fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, `alias = "tablet"`+"\n"+`class = "pad"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": "pad"}`, testClientMAC)),
			},
			{
				PreConfig: func() {
					if err := client.Clients.SetInfo(context.Background(), glinet.ClientInfo{MAC: testClientMAC, Alias: "other"}); err != nil {
						t.Fatal(err)
					}
					writes.take()
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(clientInfoAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectClientInfo("tablet", "pad"),
				Check:             writes.expect(fmt.Sprintf(`{"mac": %q, "alias": "tablet", "class": "pad"}`, testClientMAC)),
			},
			{
				PreConfig:          func() { srv.Clients.SetList(json.RawMessage(`{"clients": []}`)) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestClientInfoResourceClientNotInList(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")
	writes := recordCalls(t, srv, "clients", "set_info")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + clientInfoConfig(missingClientMAC, `alias = "tablet"`),
				ExpectError: regexp.MustCompile(`Client Not Found`),
			},
		},
	})
	if got := writes.take(); len(got) != 0 {
		t.Errorf("set_info calls %q, want none", got)
	}
}

func TestClientInfoResourceInvalidValues(t *testing.T) {
	parallel(t)
	srv := clientListServer(t, "")

	for _, attributes := range []string{
		`alias = "a,b"`,
		`alias = "a\"b"`,
		`alias = "123456789012345678901234567890123"`,
		`class = "a,b"`,
	} {
		t.Run(attributes, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + clientInfoConfig(testClientMAC, attributes),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(`Invalid Attribute Value`),
					},
				},
			})
		})
	}
}
