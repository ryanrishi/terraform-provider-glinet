package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
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
	wgConfigAddress = "glinet_wireguard_client_config.test"

	// Made-up keys in the WireGuard key format.
	testPrivateKey   = "TEST+PRIVATE+KEY+0000000000000000000000000A="
	testPublicKey    = "TEST+PUBLIC+KEY+00000000000000000000000000A="
	testPresharedKey = "TEST+PRESHARED+KEY+00000000000000000000000A="
)

// wgClientConfig is a configuration of glinet_wireguard_client_config. The empty fields are left out.
type wgClientConfig struct {
	group                                                                     string
	name, addressV4, addressV6, privateKey, publicKey, presharedKey, endpoint string
	allowedIPs, dns                                                           []string
	listenPort, keepalive, mtu                                                int
}

var fullWGClientConfig = wgClientConfig{
	name:         "test-config",
	addressV4:    "10.0.0.2/32",
	addressV6:    "fd00::2/128",
	privateKey:   testPrivateKey,
	publicKey:    testPublicKey,
	presharedKey: testPresharedKey,
	endpoint:     "vpn.example.com:51820",
	allowedIPs:   []string{"0.0.0.0/0", "::/0"},
	dns:          []string{"10.0.0.1", "fd00::1"},
	listenPort:   51820,
	keepalive:    25,
	mtu:          1420,
}

var minimalWGClientConfig = wgClientConfig{
	name:       "test-config",
	addressV4:  "10.0.0.2",
	privateKey: testPrivateKey,
	publicKey:  testPublicKey,
	endpoint:   "192.0.2.1:51820",
}

func (c wgClientConfig) with(change func(*wgClientConfig)) wgClientConfig {
	change(&c)
	return c
}

// config returns the configuration with the group glinet_wireguard_client_group.test, which the configuration
// uses when group is empty.
func (c wgClientConfig) config() string {
	group := c.group
	if group == "" {
		group = "glinet_wireguard_client_group.test.id"
	}

	var b strings.Builder
	b.WriteString(wgGroupConfig("test-group"))
	fmt.Fprintf(&b, "\nresource \"glinet_wireguard_client_config\" \"test\" {\n  group_id = %s\n", group)
	for _, a := range []struct{ name, value string }{
		{"name", c.name},
		{"address_v4", c.addressV4},
		{"address_v6", c.addressV6},
		{"private_key", c.privateKey},
		{"public_key", c.publicKey},
		{"preshared_key", c.presharedKey},
		{"endpoint", c.endpoint},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "  %s = %q\n", a.name, a.value)
		}
	}
	for _, a := range []struct {
		name  string
		value []string
	}{{"allowed_ips", c.allowedIPs}, {"dns", c.dns}} {
		if a.value != nil {
			fmt.Fprintf(&b, "  %s = %s\n", a.name, hclList(a.value))
		}
	}
	for _, a := range []struct {
		name  string
		value int
	}{{"listen_port", c.listenPort}, {"persistent_keepalive", c.keepalive}, {"mtu", c.mtu}} {
		if a.value != 0 {
			fmt.Fprintf(&b, "  %s = %d\n", a.name, a.value)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func hclList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func expectWGConfig(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgConfigAddress, tfjsonpath.New(attribute), value)
}

func stringListExact(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = knownvalue.StringExact(v)
	}
	return knownvalue.ListExact(checks)
}

// expectIDCalls is expect of calls, with GROUP_ID and PEER_ID in want replaced by the IDs of the state.
func expectIDCalls(calls *callRecorder, want ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		groupID, err := stateID(s, wgGroupAddress)
		if err != nil {
			return err
		}
		peerID, _ := stateID(s, wgConfigAddress)
		params := make([]string, len(want))
		for i, w := range want {
			params[i] = strings.NewReplacer("GROUP_ID", strconv.FormatInt(groupID, 10), "PEER_ID", strconv.FormatInt(peerID, 10)).Replace(w)
		}
		return calls.expect(params...)(s)
	}
}

// wgConfigIDs returns the group ID and the configuration ID of the state s.
func wgConfigIDs(s *terraform.State) (int64, int64, error) {
	groupID, err := stateID(s, wgGroupAddress)
	if err != nil {
		return 0, 0, err
	}
	peerID, err := stateID(s, wgConfigAddress)
	return groupID, peerID, err
}

func wgConfigImportID(s *terraform.State) (string, error) {
	groupID, peerID, err := wgConfigIDs(s)
	return fmt.Sprintf("%d/%d", groupID, peerID), err
}

// expectRouterWGConfigs checks that the group of the state has the configurations with the names want.
func expectRouterWGConfigs(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		groupID, err := stateID(s, wgGroupAddress)
		if err != nil {
			return err
		}
		peers, err := client.WireGuardClient.ListPeers(context.Background(), groupID)
		if err != nil {
			return err
		}
		var got []string
		for _, p := range peers {
			got = append(got, p.Name)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("router group has the configurations %q, want %q", got, want)
		}
		return nil
	}
}

func TestWireGuardClientConfigResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	adds := recordCalls(t, srv, "wg-client", "add_config")
	sets := recordCalls(t, srv, "wg-client", "set_config")
	updated := fullWGClientConfig.with(func(c *wgClientConfig) {
		c.name, c.addressV6, c.presharedKey, c.allowedIPs = "renamed", "", "", []string{"10.0.0.0/24"}
	})
	unset := updated.with(func(c *wgClientConfig) {
		c.dns, c.listenPort, c.keepalive, c.mtu = nil, 0, 0, 0
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroups(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fullWGClientConfig.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("id", positiveInt64),
					expectWGConfig("name", knownvalue.StringExact("test-config")),
					expectWGConfig("address_v4", knownvalue.StringExact("10.0.0.2/32")),
					expectWGConfig("address_v6", knownvalue.StringExact("fd00::2/128")),
					expectWGConfig("private_key", knownvalue.StringExact(testPrivateKey)),
					expectWGConfig("public_key", knownvalue.StringExact(testPublicKey)),
					expectWGConfig("preshared_key", knownvalue.StringExact(testPresharedKey)),
					expectWGConfig("endpoint", knownvalue.StringExact("vpn.example.com:51820")),
					expectWGConfig("allowed_ips", stringListExact("0.0.0.0/0", "::/0")),
					expectWGConfig("dns", stringListExact("10.0.0.1", "fd00::1")),
					expectWGConfig("listen_port", knownvalue.Int64Exact(51820)),
					expectWGConfig("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGConfig("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterWGConfigs(client, "test-config"),
					expectIDCalls(adds, `{"group_id":GROUP_ID,"name":"test-config","address_v4":"10.0.0.2/32","address_v6":"fd00::2/128","ipv6_enable":true,
						"private_key":"`+testPrivateKey+`","public_key":"`+testPublicKey+`","allowed_ips":"0.0.0.0/0, ::/0","end_point":"vpn.example.com:51820",
						"presharedkey_enable":true,"preshared_key":"`+testPresharedKey+`","dns":"10.0.0.1, fd00::1","listen_port":51820,"persistent_keepalive":25,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + updated.config(),
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("name", knownvalue.StringExact("renamed")),
					expectWGConfig("address_v6", knownvalue.Null()),
					expectWGConfig("preshared_key", knownvalue.Null()),
					expectWGConfig("allowed_ips", stringListExact("10.0.0.0/24")),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterWGConfigs(client, "renamed"),
					adds.expect(),
					expectIDCalls(sets, `{"group_id":GROUP_ID,"peer_id":PEER_ID,"name":"renamed","address_v4":"10.0.0.2/32","address_v6":"","ipv6_enable":false,
						"private_key":"`+testPrivateKey+`","public_key":"`+testPublicKey+`","allowed_ips":"10.0.0.0/24","end_point":"vpn.example.com:51820",
						"presharedkey_enable":false,"dns":"10.0.0.1, fd00::1","listen_port":51820,"persistent_keepalive":25,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + unset.config(),
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("dns", stringListExact("10.0.0.1", "fd00::1")),
					expectWGConfig("listen_port", knownvalue.Int64Exact(51820)),
					expectWGConfig("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGConfig("mtu", knownvalue.Int64Exact(1420)),
				},
			},
			{
				ResourceName:      wgConfigAddress,
				ImportState:       true,
				ImportStateIdFunc: wgConfigImportID,
				ImportStateVerify: true,
			},
		},
	})
}

func TestWireGuardClientConfigResourceMinimal(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-client", "add_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("address_v4", knownvalue.StringExact("10.0.0.2")),
					expectWGConfig("address_v6", knownvalue.Null()),
					expectWGConfig("preshared_key", knownvalue.Null()),
					expectWGConfig("allowed_ips", knownvalue.Null()),
					expectWGConfig("dns", knownvalue.Null()),
					expectWGConfig("listen_port", knownvalue.Null()),
					expectWGConfig("persistent_keepalive", knownvalue.Null()),
					expectWGConfig("mtu", knownvalue.Null()),
				},
				Check: expectIDCalls(adds, `{"group_id":GROUP_ID,"name":"test-config","address_v4":"10.0.0.2","address_v6":"","ipv6_enable":false,
					"private_key":"`+testPrivateKey+`","public_key":"`+testPublicKey+`","allowed_ips":"","end_point":"192.0.2.1:51820","presharedkey_enable":false}`),
			},
			{
				Config:           fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestWireGuardClientConfigResourceNotConfigured sends back the router values of the attributes that are not in the
// configuration, also after the router values change, and "" for a number that the router does not have.
func TestWireGuardClientConfigResourceNotConfigured(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	sets := recordCalls(t, srv, "wg-client", "set_config")
	var groupID, peerID int64
	saveIDs := func(s *terraform.State) (err error) {
		groupID, peerID, err = wgConfigIDs(s)
		return err
	}
	ctx := context.Background()
	setRouterPeer := func(change func(*glinet.WireGuardClientPeer)) func() {
		return func() {
			peer, err := client.WireGuardClient.FindPeer(ctx, groupID, peerID)
			if err != nil {
				t.Fatal(err)
			}
			change(peer)
			if err := client.WireGuardClient.SetPeer(ctx, groupID, *peer); err != nil {
				t.Fatal(err)
			}
			sets.take()
		}
	}
	renamed := minimalWGClientConfig.with(func(c *wgClientConfig) { c.name = "renamed" })
	const params = `{"group_id":GROUP_ID,"peer_id":PEER_ID,"name":%q,"address_v4":"10.0.0.2","address_v6":"","ipv6_enable":false,
		"private_key":"` + testPrivateKey + `","public_key":"` + testPublicKey + `","allowed_ips":"","end_point":"192.0.2.1:51820",
		"presharedkey_enable":false,"dns":"10.0.0.1",%s}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				Check:  saveIDs,
			},
			{
				PreConfig: setRouterPeer(func(p *glinet.WireGuardClientPeer) {
					p.DNS, p.ListenPort, p.PersistentKeepalive, p.MTU = "10.0.0.1", 51820, 25, 1420
				}),
				Config:           fakeRouterConfig(srv) + renamed.config(),
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("dns", stringListExact("10.0.0.1")),
					expectWGConfig("listen_port", knownvalue.Int64Exact(51820)),
					expectWGConfig("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGConfig("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: expectIDCalls(sets, fmt.Sprintf(params, "renamed", `"listen_port":51820,"persistent_keepalive":25,"mtu":1420`)),
			},
			{
				PreConfig:        setRouterPeer(func(p *glinet.WireGuardClientPeer) { p.ListenPort, p.PersistentKeepalive, p.MTU = 0, 0, 0 }),
				Config:           fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("dns", stringListExact("10.0.0.1")),
					expectWGConfig("listen_port", knownvalue.Null()),
					expectWGConfig("persistent_keepalive", knownvalue.Null()),
					expectWGConfig("mtu", knownvalue.Null()),
				},
				Check: expectIDCalls(sets, fmt.Sprintf(params, "test-config", `"listen_port":"","persistent_keepalive":"","mtu":""`)),
			},
		},
	})
}

func TestWireGuardClientConfigResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	config := fakeRouterConfig(srv) + fullWGClientConfig.config()
	var groupID, peerID int64
	saveIDs := func(s *terraform.State) (err error) {
		groupID, peerID, err = wgConfigIDs(s)
		return err
	}
	ctx := context.Background()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  saveIDs,
			},
			{
				PreConfig: func() {
					peer, err := client.WireGuardClient.FindPeer(ctx, groupID, peerID)
					if err != nil {
						t.Fatal(err)
					}
					peer.Name, peer.MTU, peer.PresharedKeyEnabled = "other", 1280, false
					if err := client.WireGuardClient.SetPeer(ctx, groupID, *peer); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("name", knownvalue.StringExact("test-config")),
					expectWGConfig("mtu", knownvalue.Int64Exact(1420)),
					expectWGConfig("preshared_key", knownvalue.StringExact(testPresharedKey)),
				},
			},
			{
				PreConfig: func() {
					if err := client.WireGuardClient.RemovePeer(ctx, groupID, peerID); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionCreate),
				Check:            resource.ComposeTestCheckFunc(expectRouterWGConfigs(client, "test-config"), saveIDs),
			},
			{
				PreConfig: func() {
					if err := client.WireGuardClient.RemoveGroup(ctx, groupID); err != nil {
						t.Fatal(err)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(wgGroupAddress, plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(wgConfigAddress, plancheck.ResourceActionCreate),
					},
				},
				Check: expectRouterWGConfigs(client, "test-config"),
			},
		},
	})
}

func TestWireGuardClientConfigResourceChangeGroup(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	other := `
resource "glinet_wireguard_client_group" "other" {
  name = "other-group"
}
`
	moved := minimalWGClientConfig.with(func(c *wgClientConfig) { c.group = "glinet_wireguard_client_group.other.id" })

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + other + minimalWGClientConfig.config(),
				Check:  expectRouterWGConfigs(client, "test-config"),
			},
			{
				Config:           fakeRouterConfig(srv) + other + moved.config(),
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeTestCheckFunc(
					expectRouterWGConfigs(client),
					func(s *terraform.State) error {
						otherID, err := stateID(s, "glinet_wireguard_client_group.other")
						if err != nil {
							return err
						}
						peers, err := client.WireGuardClient.ListPeers(context.Background(), otherID)
						if err != nil {
							return err
						}
						if len(peers) != 1 || peers[0].Name != "test-config" {
							return fmt.Errorf("the other group has the configurations %v, want test-config", peers)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestWireGuardClientConfigResourceGroupErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-client", "add_config")

	tests := []struct {
		group, error string
	}{
		{strconv.Itoa(wgProviderGroupID), `(?s)Not a Custom WireGuard Client Group.*"AzireVPN"`},
		{"99999", `(?s)WireGuard Client Group Not Found.*` + wrapped("no WireGuard client group with the ID 99999")},
	}
	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + minimalWGClientConfig.with(func(c *wgClientConfig) { c.group = tt.group }).config(),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-client.add_config was called with %q", got)
	}
}

// TestWireGuardClientConfigResourceImportOtherGroup imports a configuration of a group that Terraform does not manage.
func TestWireGuardClientConfigResourceImportOtherGroup(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + `
resource "glinet_wireguard_client_config" "test" {
  group_id      = 2082
  name          = "peer-3"
  address_v4    = "10.66.0.5/32"
  private_key   = "1vNwIUbtnIB7aNVTb0R72qFGGZ6LYiyXH+4KziIx69M="
  public_key    = "MT+jBdps4HbiE4tJps6QErMhRacT1tEZ87/WR+VQabk="
  preshared_key = "8oc0HGcTlTA+KGRAwSI1jbLhf70Pmnns3jMhjTPqgOY="
  endpoint      = "vpn-1.example.com:51820"
  allowed_ips   = ["0.0.0.0/0", "::/0"]
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       wgConfigAddress,
				ImportState:        true,
				ImportStateId:      "2082/3578",
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGConfig("dns", stringListExact("10.66.0.4")),
					expectWGConfig("listen_port", knownvalue.Int64Exact(51820)),
					expectWGConfig("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGConfig("mtu", knownvalue.Int64Exact(1420)),
				},
			},
		},
	})
}

func TestWireGuardClientConfigResourceImportErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	tests := []struct {
		id, error string
	}{
		{"2082/99999", `Cannot import non-existent remote object`},
		{"99999/3578", `Cannot import non-existent remote object`},
	}
	for _, id := range []string{"3578", "2082/", "/3578", "2082/3578/1", "a/b", "0/3578", "2082/-1"} {
		tests = append(tests, struct{ id, error string }{id, `(?s)Invalid Import ID.*must be <group_id>/<id>`})
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + minimalWGClientConfig.config(),
					ResourceName:  wgConfigAddress,
					ImportState:   true,
					ImportStateId: tt.id,
					ExpectError:   regexp.MustCompile(tt.error),
				},
			},
		})
	}
}

func TestWireGuardClientConfigResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-client", "add_config")
	c := minimalWGClientConfig

	type test struct {
		config wgClientConfig
		error  string
	}
	tests := []test{
		{c.with(func(c *wgClientConfig) { c.name = "~" }), `name must have 1 to 64 characters`},
		{c.with(func(c *wgClientConfig) { c.allowedIPs = []string{} }), `allowed_ips list must contain at least 1 elements`},
		{c.with(func(c *wgClientConfig) { c.dns = []string{} }), `dns list must contain at least 1 elements`},
		{c.with(func(c *wgClientConfig) { c.listenPort = 65536 }), `listen_port value must be between 1 and 65535`},
		{c.with(func(c *wgClientConfig) { c.keepalive = 65536 }), `persistent_keepalive value must be between 1 and\s+65535`},
		{c.with(func(c *wgClientConfig) { c.mtu = 575 }), `mtu value must be between 576 and 65535`},
		{c.with(func(c *wgClientConfig) { c.mtu = 65536 }), `mtu value must be between 576 and 65535`},
	}
	for _, v := range []string{"10.0.0.256", "fd00::2", "10.0.0.2/33", "10.0.0.0/8/8", "::ffff:10.0.0.2"} {
		tests = append(tests, test{c.with(func(c *wgClientConfig) { c.addressV4 = v }), `address_v4 must be an IPv4 address`})
	}
	for _, v := range []string{"10.0.0.2", "fd00::2/129", "fe80::1%eth0", "10.0.0.0/8"} {
		tests = append(tests, test{c.with(func(c *wgClientConfig) { c.addressV6 = v }), `address_v6 must be an IPv6 address`})
	}
	invalidKeys := []string{"short=", strings.TrimSuffix(testPrivateKey, "=") + "A", testPrivateKey + "=", "TEST-PRIVATE-KEY-0000000000000000000000000A=", " " + testPrivateKey[1:]}
	for _, v := range invalidKeys {
		tests = append(tests,
			test{c.with(func(c *wgClientConfig) { c.privateKey = v }), `private_key must be a WireGuard key`},
			test{c.with(func(c *wgClientConfig) { c.publicKey = v }), `public_key must be a WireGuard key`},
			test{c.with(func(c *wgClientConfig) { c.presharedKey = v }), `preshared_key must be a WireGuard key`},
		)
	}
	for _, v := range []string{"vpn.example.com", "vpn.example.com:0", "vpn.example.com:65536", "vpn.example.com:051820", ":51820",
		"fd00::1:51820", "[10.0.0.1]:51820", "-vpn.example.com:51820", "vpn_1.example.com:51820", "vpn.example.com:http"} {
		tests = append(tests, test{c.with(func(c *wgClientConfig) { c.endpoint = v }), `endpoint must be host:port`})
	}
	for _, v := range []string{"10.0.0.1", "0.0.0.0/33", "any"} {
		tests = append(tests, test{c.with(func(c *wgClientConfig) { c.allowedIPs = []string{"10.0.0.0/24", v} }), `allowed_ips\[1\] must be an IP address with a prefix length`})
	}
	for _, v := range []string{"dns.example.com", "10.0.0.0/24"} {
		tests = append(tests, test{c.with(func(c *wgClientConfig) { c.dns = []string{v} }), `dns\[0\] must be an IP address`})
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.config.config(),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-client.add_config was called with %q", got)
	}
}

func TestWireGuardClientConfigResourceValidValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	c := minimalWGClientConfig

	for _, config := range []wgClientConfig{
		c.with(func(c *wgClientConfig) { c.endpoint, c.addressV6 = "[2001:db8::1]:1", "fd00::2" }),
		c.with(func(c *wgClientConfig) { c.endpoint, c.listenPort, c.keepalive, c.mtu = "vpn:65535", 1, 1, 576 }),
		c.with(func(c *wgClientConfig) {
			c.endpoint, c.listenPort, c.keepalive, c.mtu = "VPN-1.Example.com:51820", 65535, 65535, 65535
		}),
	} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            fakeRouterConfig(srv) + config.config(),
					ConfigStateChecks: []statecheck.StateCheck{expectWGConfig("endpoint", knownvalue.StringExact(config.endpoint))},
				},
			},
		})
	}
}

func TestWireGuardClientConfigResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := []struct {
		method, error string
		step          int
	}{
		{"add_config", "Unable to Create WireGuard Client Configuration", 0},
		{"get_config_list", "Unable to Read WireGuard Client Configuration", 1},
		{"set_config", "Unable to Update WireGuard Client Configuration", 1},
		{"remove_config", "Unable to Delete WireGuard Client Configuration", 2},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			configs := []string{
				fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				fakeRouterConfig(srv) + minimalWGClientConfig.with(func(c *wgClientConfig) { c.name = "renamed" }).config(),
				fakeRouterConfig(srv) + wgGroupConfig("test-group"),
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

// TestWireGuardClientConfigResourceReadErrorAfterCreate checks that a configuration that the router added stays in the
// state when the read after the write fails.
func TestWireGuardClientConfigResourceReadErrorAfterCreate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	// add_config is between the first and the second call; the third call is the read-back.
	failNthCall(srv, "wg-client", "get_config_list", 3)
	config := fakeRouterConfig(srv) + minimalWGClientConfig.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroups(client),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read WireGuard Client Configuration.*Method not found`),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgConfigAddress, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            expectRouterWGConfigs(client, "test-config"),
			},
		},
	})
}

// failNthCall makes the nth call of module.method on srv return "Method not found".
// The other calls go to the method that served it before.
func failNthCall(srv *glinettest.Server, module, method string, n int32) {
	next := srv.Handler(module, method)
	var calls atomic.Int32
	srv.Handle(module, method, func(params json.RawMessage) (any, error) {
		if calls.Add(1) == n {
			return nil, glinettest.ErrMethodNotFound
		}
		return next(params)
	})
}

// createErrorNote is the pattern of the note of a create error after which the router may have the object.
func createErrorNote(object string) string {
	return `(?s)Method not found.*` + wrapped("The router may have created the "+object+" before the error. "+
		"Check the router in the web UI, and remove or import the "+object+" before the next apply.")
}

// TestWireGuardClientConfigResourceCreateListError fails the read of the configurations after add_config, so that
// the router has a configuration that is not in the state.
func TestWireGuardClientConfigResourceCreateListError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	failNthCall(srv, "wg-client", "get_config_list", 2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + minimalWGClientConfig.config(),
				ExpectError: regexp.MustCompile(`(?s)Unable to Create WireGuard Client Configuration.*` + createErrorNote("configuration")),
			},
			{
				Config: fakeRouterConfig(srv) + wgGroupConfig("test-group"),
				Check:  expectRouterWGConfigs(client, "test-config"),
			},
			{
				PreConfig: func() {
					groups, err := client.WireGuardClient.ListGroups(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					for _, g := range groups {
						if g.Name == "test-group" {
							if err := client.WireGuardClient.RemoveAllPeers(context.Background(), g.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

// reportGroupAsPreset makes get_group_list of srv report the group name as a preset of a VPN service provider while preset is true.
func reportGroupAsPreset(t *testing.T, srv *glinettest.Server, name string, preset *atomic.Bool) {
	next := srv.Handler("wg-client", "get_group_list")
	srv.Handle("wg-client", "get_group_list", func(params json.RawMessage) (any, error) {
		res, err := next(params)
		if err != nil || !preset.Load() {
			return res, err
		}
		var list struct {
			Groups []map[string]any `json:"groups"`
		}
		raw, ok := res.(json.RawMessage)
		if !ok {
			t.Fatalf("get_group_list result is %T", res)
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatal(err)
		}
		for _, g := range list.Groups {
			if g["group_name"] == name {
				g["group_type"] = glinet.WireGuardClientGroupProvider
			}
		}
		return list, nil
	})
}

func TestWireGuardClientConfigResourcePresetGroupOnRefresh(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	removes := recordCalls(t, srv, "wg-client", "remove_config")
	var preset atomic.Bool
	reportGroupAsPreset(t, srv, "test-group", &preset)
	config := fakeRouterConfig(srv) + minimalWGClientConfig.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:   func() { preset.Store(true) },
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Not a Custom WireGuard Client Group.*"test-group"`),
			},
			{
				PreConfig: func() {
					if got := removes.take(); len(got) != 0 {
						t.Errorf("wg-client.remove_config was called with %q", got)
					}
					preset.Store(false)
				},
				Config: config,
			},
		},
	})
}

func TestWireGuardClientConfigResourceImportPresetGroup(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	removes := recordCalls(t, srv, "wg-client", "remove_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + minimalWGClientConfig.with(func(c *wgClientConfig) {
					c.group = "9859"
				}).config(),
				ResourceName:  wgConfigAddress,
				ImportState:   true,
				ImportStateId: "9859/8913",
				ExpectError:   regexp.MustCompile(`(?s)Not a Custom WireGuard Client Group.*"group-1"`),
			},
		},
	})

	if got := removes.take(); len(got) != 0 {
		t.Errorf("wg-client.remove_config was called with %q", got)
	}
}
