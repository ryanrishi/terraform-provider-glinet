package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	wgGroupAddress = "glinet_wireguard_client_group.test"

	// IDs of groups of the fake router: a preset of a VPN service provider and a group of the user.
	wgProviderGroupID = 7573
	wgCustomGroupID   = 2082
)

func wgGroupConfig(name string) string {
	return fmt.Sprintf(`
resource "glinet_wireguard_client_group" "test" {
  name = %q
}
`, name)
}

func expectWGGroup(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgGroupAddress, tfjsonpath.New(attribute), value)
}

// stateID returns the value of the attribute id of the resource at address in the state s.
func stateID(s *terraform.State, address string) (int64, error) {
	rs, ok := s.RootModule().Resources[address]
	if !ok {
		return 0, fmt.Errorf("no resource %s in the state", address)
	}
	return strconv.ParseInt(rs.Primary.Attributes["id"], 10, 64)
}

// expectRouterGroups checks that the custom groups of the router have exactly the names want.
func expectRouterGroups(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		groups, err := client.WireGuardClient.ListGroups(context.Background())
		if err != nil {
			return err
		}
		var got []string
		for _, g := range groups {
			if g.Type == glinet.WireGuardClientGroupCustom && g.ID != wgCustomGroupID {
				got = append(got, g.Name)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("router has the new groups %q, want %q", got, want)
		}
		return nil
	}
}

func TestWireGuardClientGroupResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	adds := recordCalls(t, srv, "wg-client", "add_group")
	renames := recordCalls(t, srv, "wg-client", "set_group")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroups(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wgGroupConfig("test-group"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGGroup("id", positiveInt64),
					expectWGGroup("name", knownvalue.StringExact("test-group")),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterGroups(client, "test-group"),
					adds.expect(`{"group_name":"test-group"}`),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + wgGroupConfig("renamed"),
				ConfigPlanChecks:  expectResourceAction(wgGroupAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectWGGroup("name", knownvalue.StringExact("renamed"))},
				Check: resource.ComposeTestCheckFunc(
					expectRouterGroups(client, "renamed"),
					adds.expect(),
					func(s *terraform.State) error {
						id, err := stateID(s, wgGroupAddress)
						if err != nil {
							return err
						}
						return renames.expect(fmt.Sprintf(`{"group_id":%d,"group_name":"renamed"}`, id))(s)
					},
				),
			},
			{
				ResourceName:      wgGroupAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestWireGuardClientGroupResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	config := fakeRouterConfig(srv) + wgGroupConfig("test-group")
	var id int64

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) (err error) {
					id, err = stateID(s, wgGroupAddress)
					return err
				},
			},
			{
				PreConfig: func() {
					if err := client.WireGuardClient.RenameGroup(context.Background(), id, "other"); err != nil {
						t.Fatal(err)
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(wgGroupAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectWGGroup("name", knownvalue.StringExact("test-group"))},
				Check:             expectRouterGroups(client, "test-group"),
			},
			{
				PreConfig: func() {
					if err := client.WireGuardClient.RemoveGroup(context.Background(), id); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgGroupAddress, plancheck.ResourceActionCreate),
				Check:            expectRouterGroups(client, "test-group"),
			},
		},
	})
}

// TestWireGuardClientGroupResourceDeleteWithConfigs checks that destroy does not remove a group with a configuration
// that Terraform does not manage.
func TestWireGuardClientGroupResourceDeleteWithConfigs(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	removes := recordCalls(t, srv, "wg-client", "remove_group")
	var groupID, peerID int64

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroups(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wgGroupConfig("test-group"),
				Check: func(s *terraform.State) (err error) {
					groupID, err = stateID(s, wgGroupAddress)
					if err != nil {
						return err
					}
					peerID, err = client.WireGuardClient.AddPeer(context.Background(), groupID, glinet.WireGuardClientPeer{Name: "manual"})
					return err
				},
			},
			{
				Config:      fakeRouterConfig(srv),
				ExpectError: regexp.MustCompile(`(?s)WireGuard Client Group Has Configurations.*` + wrapped(`configurations "manual" (ID`)),
			},
			{
				PreConfig: func() {
					if got := removes.take(); len(got) != 0 {
						t.Errorf("wg-client.remove_group was called with %q", got)
					}
					if err := client.WireGuardClient.RemovePeer(context.Background(), groupID, peerID); err != nil {
						t.Fatal(err)
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

func TestWireGuardClientGroupResourceImport(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	id, err := client.WireGuardClient.AddGroup(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	config := fakeRouterConfig(srv) + wgGroupConfig("existing")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       wgGroupAddress,
				ImportState:        true,
				ImportStateId:      strconv.FormatInt(id, 10),
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{expectWGGroup("id", knownvalue.Int64Exact(id))},
			},
		},
	})
}

func TestWireGuardClientGroupResourceImportErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	tests := []struct {
		id, error string
	}{
		{strconv.Itoa(wgProviderGroupID), `(?s)Not a Custom WireGuard Client Group.*"AzireVPN"`},
		{"99999", `Cannot import non-existent remote object`},
	}
	for _, id := range []string{"group", "0", "-1", "01", "2082/1"} {
		tests = append(tests, struct{ id, error string }{id, `(?s)Invalid Import ID.*must be the group ID`})
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + wgGroupConfig("test-group"),
					ResourceName:  wgGroupAddress,
					ImportState:   true,
					ImportStateId: tt.id,
					ExpectError:   regexp.MustCompile(tt.error),
				},
			},
		})
	}
}

func TestWireGuardClientGroupResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-client", "add_group")

	for _, name := range []string{"", "   ", ".hidden", "a~b", strings.Repeat("a", 65), strings.Repeat("é", 65)} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + wgGroupConfig(name),
					ExpectError: regexp.MustCompile(`name must have 1 to 64 characters`),
				},
			},
		})
	}

	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-client.add_group was called with %q", got)
	}
}

func TestWireGuardClientGroupResourceValidNames(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, name := range []string{"a", strings.Repeat("é", 64), "my vpn.group"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            fakeRouterConfig(srv) + wgGroupConfig(name),
					ConfigStateChecks: []statecheck.StateCheck{expectWGGroup("name", knownvalue.StringExact(name))},
				},
			},
		})
	}
}

func TestWireGuardClientGroupResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := []struct {
		method, error string
		step          int
	}{
		{"add_group", "Unable to Create WireGuard Client Group", 0},
		{"get_group_list", "Unable to Read WireGuard Client Groups", 1},
		{"set_group", "Unable to Update WireGuard Client Group", 1},
		{"remove_group", "Unable to Delete WireGuard Client Group", 2},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			configs := []string{
				fakeRouterConfig(srv) + wgGroupConfig("test-group"),
				fakeRouterConfig(srv) + wgGroupConfig("renamed"),
				fakeRouterConfig(srv),
			}
			steps := make([]resource.TestStep, tt.step+1)
			for i := range steps {
				steps[i].Config = configs[i]
			}
			steps[tt.step].PreConfig = func() { failFirstCall(srv, "wg-client", tt.method) }
			steps[tt.step].ExpectError = regexp.MustCompile(`(?s)` + wrapped(tt.error) + `.*Method not found`)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps:                    steps,
			})
		})
	}
}

// TestWireGuardClientGroupResourceCreateListError fails the read of the groups after add_group, so that the router
// has a group that is not in the state.
func TestWireGuardClientGroupResourceCreateListError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	failNthCall(srv, "wg-client", "get_group_list", 2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + wgGroupConfig("test-group"),
				ExpectError: regexp.MustCompile(`(?s)Unable to Create WireGuard Client Group.*` + createErrorNote("group")),
			},
		},
	})

	if err := expectRouterGroups(client, "test-group")(nil); err != nil {
		t.Error(err)
	}
}
