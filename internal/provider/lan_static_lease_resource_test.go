package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"testing"

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
	srv := glinettest.NewServer(t)

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"upper case MAC", leaseConfig("02:00:00:00:00:0A", leaseIP, ""), `must be a MAC address in lower case`},
		{"empty name", `
resource "glinet_lan_static_lease" "test" {
  mac  = "02:00:00:00:00:01"
  ip   = "192.168.8.201"
  name = ""
}
`, `string length must be at least 1`},
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
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + leaseConfig(leaseMAC, "192.168.8.300", ""),
				ExpectError: regexp.MustCompile(`(?s)Unable to Create LAN Static Lease.*Invalid params: invalid ip`),
			},
		},
	})
}

func TestLANStaticLeaseResourceReadError(t *testing.T) {
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

func TestLANStaticLeaseResourceImportNotFound(t *testing.T) {
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
