package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	leaseAddress = "glinet_lan_static_lease.test"
	leaseMAC     = "02:00:00:00:00:01"
	otherMAC     = "02:00:00:00:00:02"
	leaseIP      = "192.168.8.201"
	otherIP      = "192.168.8.202"
)

func leaseConfig(mac, ip, name string) string {
	nameLine := ""
	if name != "" {
		nameLine = fmt.Sprintf("  name = %q\n", name)
	}

	return fmt.Sprintf(`
resource "glinet_lan_static_lease" "test" {
  mac  = %q
  ip   = %q
%s}
`, mac, ip, nameLine)
}

func expectLease(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(leaseAddress, tfjsonpath.New(attribute), value)
}

// leaseTestRouter returns the provider block and a client for the router of the test.
// On the real router, the test fails when a binding has a test MAC or IP address, and it removes the test MAC
// addresses and checks that the bindings are as before when the test ends.
func leaseTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.LAN.ListStaticBinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range initial {
		if b.MAC == leaseMAC || b.MAC == otherMAC || b.IP == leaseIP || b.IP == otherIP {
			t.Fatalf("the router has a static lease %s %s that the test uses", b.MAC, b.IP)
		}
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, mac := range []string{leaseMAC, otherMAC} {
			if err := client.LAN.RemoveStaticBind(ctx, mac); err != nil {
				t.Errorf("remove %s: %v", mac, err)
			}
		}

		binds, err := client.LAN.ListStaticBinds(ctx)
		if err != nil {
			t.Fatalf("read static leases: %v", err)
		}
		if !slices.Equal(binds, initial) {
			t.Errorf("static leases are %v, want %v", binds, initial)
		}
	})

	return `provider "glinet" {}`, client
}

func expectRouterLease(client *glinet.Client, mac string, want *glinet.StaticBind) resource.TestCheckFunc {
	return func(*terraform.State) error {
		binds, err := client.LAN.ListStaticBinds(context.Background())
		if err != nil {
			return err
		}

		i := slices.IndexFunc(binds, func(b glinet.StaticBind) bool { return b.MAC == mac })
		switch {
		case want == nil && i >= 0:
			return fmt.Errorf("router has static lease %v, want none", binds[i])
		case want != nil && i < 0:
			return fmt.Errorf("router has no static lease for %s", mac)
		case want != nil && binds[i] != *want:
			return fmt.Errorf("router static lease is %v, want %v", binds[i], *want)
		}
		return nil
	}
}

func TestLANStaticLeaseResource(t *testing.T) {
	parallel(t)
	provider, client := leaseTestRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLease(client, leaseMAC, nil),
		Steps: []resource.TestStep{
			{
				Config: provider + leaseConfig(leaseMAC, leaseIP, "test-lease"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("mac", knownvalue.StringExact(leaseMAC)),
					expectLease("ip", knownvalue.StringExact(leaseIP)),
					expectLease("name", knownvalue.StringExact("test-lease")),
				},
				Check: expectRouterLease(client, leaseMAC, &glinet.StaticBind{MAC: leaseMAC, IP: leaseIP, Name: "test-lease"}),
			},
			{
				Config: provider + leaseConfig(leaseMAC, otherIP, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("ip", knownvalue.StringExact(otherIP)),
					expectLease("name", knownvalue.Null()),
				},
				Check: expectRouterLease(client, leaseMAC, &glinet.StaticBind{MAC: leaseMAC, IP: otherIP}),
			},
			{
				ResourceName:                         leaseAddress,
				ImportState:                          true,
				ImportStateId:                        leaseMAC,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "mac",
			},
			{
				Config: provider + leaseConfig(otherMAC, otherIP, "test-lease"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(leaseAddress, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("mac", knownvalue.StringExact(otherMAC)),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterLease(client, leaseMAC, nil),
					expectRouterLease(client, otherMAC, &glinet.StaticBind{MAC: otherMAC, IP: otherIP, Name: "test-lease"}),
				),
			},
		},
	})
}

func TestLANStaticLeaseResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, "test-lease")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					srv.LAN.SetStaticBinds([]glinettest.StaticBind{{MAC: leaseMAC, IP: otherIP}})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("ip", knownvalue.StringExact(leaseIP)),
					expectLease("name", knownvalue.StringExact("test-lease")),
				},
			},
			{
				PreConfig: func() {
					srv.LAN.SetStaticBinds(nil)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(leaseAddress, plancheck.ResourceActionCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("ip", knownvalue.StringExact(leaseIP)),
				},
			},
		},
	})

	if got := srv.LAN.StaticBinds(); len(got) != 0 {
		t.Errorf("static leases after destroy are %v, want none", got)
	}
}

func TestLANStaticLeaseResourceDeleteKeepsOtherLeases(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	other := glinettest.StaticBind{MAC: otherMAC, IP: otherIP, Name: "other"}
	srv.LAN.SetStaticBinds([]glinettest.StaticBind{other})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
			},
		},
	})

	if got := srv.LAN.StaticBinds(); !slices.Equal(got, []glinettest.StaticBind{other}) {
		t.Errorf("static leases after destroy are %v, want %v", got, []glinettest.StaticBind{other})
	}
}

func TestLANStaticLeaseResourceMACExists(t *testing.T) {
	parallel(t)
	for _, mac := range []string{"02:00:00:00:00:0a", "02:00:00:00:00:0A"} {
		t.Run(mac, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			existing := []glinettest.StaticBind{{MAC: mac, IP: otherIP, Name: "existing"}}
			srv.LAN.SetStaticBinds(existing)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + leaseConfig("02:00:00:00:00:0a", leaseIP, ""),
						ExpectError: regexp.MustCompile(`LAN Static Lease Already Exists`),
					},
				},
			})

			if got := srv.LAN.StaticBinds(); !slices.Equal(got, existing) {
				t.Errorf("static leases are %v, want %v", got, existing)
			}
		})
	}
}

func TestLANStaticLeaseResourceCreateIPInUse(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	existing := []glinettest.StaticBind{{MAC: otherMAC, IP: leaseIP}}
	srv.LAN.SetStaticBinds(existing)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
				ExpectError: regexp.MustCompile(`IP Address Already Has a LAN Static Lease`),
			},
		},
	})

	if got := srv.LAN.StaticBinds(); !slices.Equal(got, existing) {
		t.Errorf("static leases are %v, want %v", got, existing)
	}
}

func TestLANStaticLeaseResourceUpdateIPInUse(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	other := glinettest.StaticBind{MAC: otherMAC, IP: otherIP}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
			},
			{
				PreConfig: func() {
					srv.LAN.SetStaticBinds(append(srv.LAN.StaticBinds(), other))
				},
				Config:      fakeRouterConfig(srv) + leaseConfig(leaseMAC, otherIP, ""),
				ExpectError: regexp.MustCompile(`IP Address Already Has a LAN Static Lease`),
			},
		},
	})

	if got := srv.LAN.StaticBinds(); !slices.Contains(got, other) {
		t.Errorf("static leases are %v, want them to contain %v", got, other)
	}
}

func TestLANStaticLeaseResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"MAC with dashes", leaseConfig("02-00-00-00-00-0A", leaseIP, ""), `must be a MAC address`},
		{"empty name", `
resource "glinet_lan_static_lease" "test" {
  mac  = "02:00:00:00:00:01"
  ip   = "192.168.8.201"
  name = ""
}
`, `string length must be at least 1`},
	}
	for _, ip := range []string{"", "192.168.8.300", "192.168.8", " 192.168.8.201", "192.168.8.0/24", "fd00::1"} {
		tests = append(tests, struct {
			name   string
			config string
			error  string
		}{"IP " + ip, leaseConfig(leaseMAC, ip, ""), `ip must be an IPv4 address`})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + tt.config,
						ExpectError: regexp.MustCompile(tt.error),
					},
				},
			})
		})
	}
}

func TestLANStaticLeaseResourceRouterError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   failRouterMethod(srv, "lan", "add_static_bind"),
				Config:      fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable to Create LAN Static Lease.*Method not found`),
			},
		},
	})
}

func TestLANStaticLeaseResourceReadError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("lan", "get_static_bind_list", func(json.RawMessage) (any, error) {
		return nil, glinettest.ErrMethodNotFound
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable to Read LAN Static Leases.*Method not found`),
			},
		},
	})
}

func TestLANStaticLeaseResourceReadErrorAfterCreate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failed := false
	srv.Handle("lan", "get_static_bind_list", func(json.RawMessage) (any, error) {
		binds := srv.LAN.StaticBinds()
		if len(binds) > 0 && !failed {
			failed = true
			return nil, glinettest.ErrMethodNotFound
		}
		return map[string]any{"static_bind_list": binds}, nil
	})
	config := fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read LAN Static Lease.*Method not found`),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(leaseAddress, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("mac", knownvalue.StringExact(leaseMAC)),
					expectLease("ip", knownvalue.StringExact(leaseIP)),
				},
			},
		},
	})
}

func TestLANStaticLeaseResourceImportNotFound(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
				ResourceName:  leaseAddress,
				ImportState:   true,
				ImportStateId: leaseMAC,
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

// TestLANStaticLeaseResourceImportUpperCase imports a lease with a MAC address in upper case in the import ID.
func TestLANStaticLeaseResourceImportUpperCase(t *testing.T) {
	const mac = "02:00:00:aa:bb:cc"
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	if err := client.LAN.AddStaticBind(context.Background(), glinet.StaticBind{MAC: mac, IP: leaseIP}); err != nil {
		t.Fatal(err)
	}
	config := fakeRouterConfig(srv) + leaseConfig(mac, leaseIP, "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       leaseAddress,
				ImportState:        true,
				ImportStateId:      strings.ToUpper(mac),
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(mac))},
			},
		},
	})
}

func TestLANStaticLeaseResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, id := range []string{"02-00-00-00-00-01", "02:00:00:00:00", "lease"} {
		t.Run(id, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        fakeRouterConfig(srv) + leaseConfig(leaseMAC, leaseIP, ""),
						ResourceName:  leaseAddress,
						ImportState:   true,
						ImportStateId: id,
						ExpectError:   regexp.MustCompile(`(?s)Invalid Import ID.*must be a MAC address`),
					},
				},
			})
		})
	}
}

// TestLANStaticLeaseResourceParallelSameIP creates two leases with the same IP address in one apply.
// The router would remove the first lease when it adds the second one.
func TestLANStaticLeaseResourceParallelSameIP(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("lan", "get_static_bind_list", func(json.RawMessage) (any, error) {
		res := map[string]any{"static_bind_list": srv.LAN.StaticBinds()}
		time.Sleep(20 * time.Millisecond)
		return res, nil
	})
	first := glinettest.StaticBind{MAC: leaseMAC, IP: leaseIP}
	second := glinettest.StaticBind{MAC: otherMAC, IP: leaseIP}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if got := srv.LAN.StaticBinds(); len(got) != 0 {
				return fmt.Errorf("router static leases are %v, want none", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fmt.Sprintf(`
resource "glinet_lan_static_lease" "first" {
  mac = %q
  ip  = %q
}

resource "glinet_lan_static_lease" "second" {
  mac = %q
  ip  = %q
}
`, first.MAC, first.IP, second.MAC, second.IP),
				ExpectError: regexp.MustCompile(`IP Address Already Has a LAN Static Lease`),
			},
			{
				PreConfig: func() {
					got := srv.LAN.StaticBinds()
					if len(got) != 1 || (got[0] != first && got[0] != second) {
						t.Errorf("static leases are %v, want %v or %v", got, first, second)
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

const (
	upperCaseLeaseMAC = "02:00:00:AA:BB:CC"
	lowerCaseLeaseMAC = "02:00:00:aa:bb:cc"
	mixedCaseLeaseMAC = "02:00:00:Aa:bB:cC"
)

func expectRouterLeases(srv *glinettest.Server, want ...glinettest.StaticBind) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.LAN.StaticBinds(); !slices.Equal(got, want) {
			return fmt.Errorf("router static leases are %v, want %v", got, want)
		}
		return nil
	}
}

// TestLANStaticLeaseResourceLetterCase configures the MAC address in upper case and in mixed case.
// The state keeps the configured letter case, also when the router has the MAC address in another letter case,
// and each write goes to the binding of the router.
func TestLANStaticLeaseResourceLetterCase(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLeases(srv),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
				ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(upperCaseLeaseMAC))},
				Check:             expectRouterLeases(srv, glinettest.StaticBind{MAC: upperCaseLeaseMAC, IP: leaseIP}),
			},
			{
				PreConfig: func() {
					srv.LAN.SetStaticBinds([]glinettest.StaticBind{{MAC: lowerCaseLeaseMAC, IP: leaseIP}})
				},
				Config:            fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(upperCaseLeaseMAC))},
			},
			{
				Config:            fakeRouterConfig(srv) + leaseConfig(mixedCaseLeaseMAC, leaseIP, ""),
				ConfigPlanChecks:  expectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(mixedCaseLeaseMAC))},
				Check:             expectRouterLeases(srv, glinettest.StaticBind{MAC: lowerCaseLeaseMAC, IP: leaseIP}),
			},
			{
				Config:           fakeRouterConfig(srv) + leaseConfig(mixedCaseLeaseMAC, otherIP, "test-lease"),
				ConfigPlanChecks: expectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectLease("mac", knownvalue.StringExact(mixedCaseLeaseMAC)),
					expectLease("ip", knownvalue.StringExact(otherIP)),
				},
				Check: expectRouterLeases(srv, glinettest.StaticBind{MAC: lowerCaseLeaseMAC, IP: otherIP, Name: "test-lease"}),
			},
			{
				Config:           fakeRouterConfig(srv) + leaseConfig(mixedCaseLeaseMAC, otherIP, "test-lease"),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestLANStaticLeaseResourceImportLetterCase imports with the import ID in each letter case.
// The import sets the MAC address of the router, so a configuration in that letter case has no difference,
// and a configuration in another letter case changes only the state.
func TestLANStaticLeaseResourceImportLetterCase(t *testing.T) {
	parallel(t)
	otherCase := map[string]string{upperCaseLeaseMAC: lowerCaseLeaseMAC, lowerCaseLeaseMAC: upperCaseLeaseMAC}
	for _, routerMAC := range []string{upperCaseLeaseMAC, lowerCaseLeaseMAC} {
		for _, id := range []string{upperCaseLeaseMAC, lowerCaseLeaseMAC, mixedCaseLeaseMAC} {
			t.Run(routerMAC+" as "+id, func(t *testing.T) {
				srv := glinettest.NewServer(t)
				existing := glinettest.StaticBind{MAC: routerMAC, IP: leaseIP}
				srv.LAN.SetStaticBinds([]glinettest.StaticBind{existing})
				config := fakeRouterConfig(srv) + leaseConfig(routerMAC, leaseIP, "")
				otherCaseConfig := fakeRouterConfig(srv) + leaseConfig(otherCase[routerMAC], leaseIP, "")

				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: testProtoV6ProviderFactories,
					CheckDestroy:             expectRouterLeases(srv),
					Steps: []resource.TestStep{
						{
							Config:             config,
							ResourceName:       leaseAddress,
							ImportState:        true,
							ImportStateId:      id,
							ImportStatePersist: true,
						},
						{
							Config:            config,
							ConfigPlanChecks:  expectEmptyPlan,
							ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(routerMAC))},
						},
						{
							Config:            otherCaseConfig,
							ConfigPlanChecks:  expectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
							ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(otherCase[routerMAC]))},
							Check:             expectRouterLeases(srv, existing),
						},
						{
							Config:           otherCaseConfig,
							ConfigPlanChecks: expectEmptyPlan,
						},
					},
				})
			})
		}
	}
}

// TestLANStaticLeaseResourceTwoLetterCases has a binding for each letter case of the MAC address on the router.
// The resource uses only the binding with exactly the configured MAC address.
func TestLANStaticLeaseResourceTwoLetterCases(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	other := glinettest.StaticBind{MAC: lowerCaseLeaseMAC, IP: otherIP, Name: "other"}
	srv.LAN.SetStaticBinds([]glinettest.StaticBind{other, {MAC: upperCaseLeaseMAC, IP: leaseIP}})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLeases(srv, other),
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
				ResourceName:       leaseAddress,
				ImportState:        true,
				ImportStateId:      upperCaseLeaseMAC,
				ImportStatePersist: true,
			},
			{
				Config:           fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, "test-lease"),
				ConfigPlanChecks: expectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
				Check:            expectRouterLeases(srv, other, glinettest.StaticBind{MAC: upperCaseLeaseMAC, IP: leaseIP, Name: "test-lease"}),
			},
		},
	})
}

// TestLANStaticLeaseResourceTwoLetterCasesAmbiguousChange changes mac to a letter case that does not select
// exactly one router binding. The update fails before a write, so the resource keeps its binding.
func TestLANStaticLeaseResourceTwoLetterCasesAmbiguousChange(t *testing.T) {
	const freeIP = "192.168.8.203"
	parallel(t)
	srv := glinettest.NewServer(t)
	other := glinettest.StaticBind{MAC: lowerCaseLeaseMAC, IP: otherIP, Name: "other"}
	own := glinettest.StaticBind{MAC: upperCaseLeaseMAC, IP: leaseIP}
	srv.LAN.SetStaticBinds([]glinettest.StaticBind{other, own})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLeases(srv, other),
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
				ResourceName:       leaseAddress,
				ImportState:        true,
				ImportStateId:      upperCaseLeaseMAC,
				ImportStatePersist: true,
			},
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(mixedCaseLeaseMAC, freeIP, ""),
				ExpectError: regexp.MustCompile(`Ambiguous Letter Case of MAC Address`),
			},
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(lowerCaseLeaseMAC, freeIP, ""),
				ExpectError: regexp.MustCompile(`Ambiguous Letter Case of MAC Address`),
			},
			{
				PreConfig: func() {
					if got := srv.LAN.StaticBinds(); !slices.Equal(got, []glinettest.StaticBind{other, own}) {
						t.Errorf("static leases after the failed updates are %v, want %v", got, []glinettest.StaticBind{other, own})
					}
				},
				Config:            fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, freeIP, ""),
				ConfigPlanChecks:  expectResourceAction(leaseAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectLease("mac", knownvalue.StringExact(upperCaseLeaseMAC))},
				Check:             expectRouterLeases(srv, other, glinettest.StaticBind{MAC: upperCaseLeaseMAC, IP: freeIP}),
			},
		},
	})
}

// TestLANStaticLeaseResourceTwoLetterCasesIPInUse changes the IP address of a binding to the IP address of
// the binding of the same MAC address in another letter case. The router would remove that binding, so the update fails.
func TestLANStaticLeaseResourceTwoLetterCasesIPInUse(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	other := glinettest.StaticBind{MAC: lowerCaseLeaseMAC, IP: otherIP, Name: "other"}
	own := glinettest.StaticBind{MAC: upperCaseLeaseMAC, IP: leaseIP}
	srv.LAN.SetStaticBinds([]glinettest.StaticBind{own, other})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLeases(srv, other),
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
				ResourceName:       leaseAddress,
				ImportState:        true,
				ImportStateId:      upperCaseLeaseMAC,
				ImportStatePersist: true,
			},
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, otherIP, ""),
				ExpectError: regexp.MustCompile(`(?s)IP Address Already Has a LAN Static Lease.*` + lowerCaseLeaseMAC),
			},
			{
				PreConfig: func() {
					if got := srv.LAN.StaticBinds(); !slices.Equal(got, []glinettest.StaticBind{own, other}) {
						t.Errorf("static leases after the failed update are %v, want %v", got, []glinettest.StaticBind{own, other})
					}
				},
				Config: fakeRouterConfig(srv) + leaseConfig(upperCaseLeaseMAC, leaseIP, ""),
			},
		},
	})
}
