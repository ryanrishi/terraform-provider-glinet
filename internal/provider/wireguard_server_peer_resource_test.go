package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	wgPeerAddress = "glinet_wireguard_server_peer.test"

	testPeerPresharedKey = "TEST+SERVER+PRESHARED+KEY+0000000000000000A="

	fakeServerPublicKey = "MADE+UP+SERVER+PUBLIC+KEY+FOR+TESTS+00000000="
	fakePeerPrivateKey  = "MADE+UP+PEER+PRIVATE+KEY+FOR+TESTS+000000000="
)

// wgServerPeer is a configuration of glinet_wireguard_server_peer. The empty fields are left out.
type wgServerPeer struct {
	name, presharedKey string
	allowedIPs, dns    []string
	keepalive, mtu     int
}

var fullWGServerPeer = wgServerPeer{
	name:         "test-peer",
	presharedKey: testPeerPresharedKey,
	allowedIPs:   []string{"0.0.0.0/0", "::/0"},
	dns:          []string{"192.168.8.1"},
	keepalive:    25,
	mtu:          1420,
}

func (p wgServerPeer) with(change func(*wgServerPeer)) wgServerPeer {
	change(&p)
	return p
}

func (p wgServerPeer) config() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nresource \"glinet_wireguard_server_peer\" \"test\" {\n  name = %q\n", p.name)
	if p.presharedKey != "" {
		fmt.Fprintf(&b, "  preshared_key = %q\n", p.presharedKey)
	}
	for _, a := range []struct {
		name  string
		value []string
	}{{"allowed_ips", p.allowedIPs}, {"dns", p.dns}} {
		if a.value != nil {
			fmt.Fprintf(&b, "  %s = %s\n", a.name, hclList(a.value))
		}
	}
	for _, a := range []struct {
		name  string
		value int
	}{{"persistent_keepalive", p.keepalive}, {"mtu", p.mtu}} {
		if a.value != 0 {
			fmt.Fprintf(&b, "  %s = %d\n", a.name, a.value)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func expectWGPeer(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgPeerAddress, tfjsonpath.New(attribute), value)
}

// wgServerTestRouter returns a fake router with an initialized WireGuard server and a client for it.
func wgServerTestRouter(t *testing.T) (*glinettest.Server, *glinet.Client) {
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	if err := client.WireGuardServer.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

// expectRouterPeers checks that the WireGuard server has peers with exactly the names want.
func expectRouterPeers(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		peers, err := client.WireGuardServer.ListPeers(context.Background())
		if err != nil {
			return err
		}
		var got []string
		for _, p := range peers {
			got = append(got, p.Name)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("router has the WireGuard server peers %q, want %q", got, want)
		}
		return nil
	}
}

// expectPeerCalls is expect of calls, with PEER_ID in want replaced by the ID of the state.
func expectPeerCalls(calls *callRecorder, want ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id, err := stateID(s, wgPeerAddress)
		if err != nil {
			return err
		}
		params := make([]string, len(want))
		for i, w := range want {
			params[i] = strings.ReplaceAll(w, "PEER_ID", strconv.FormatInt(id, 10))
		}
		return calls.expect(params...)(s)
	}
}

func expectWGPeerClientConfig(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgPeerAddress, tfjsonpath.New("client_config").AtMapKey(attribute), value)
}

func TestWireGuardServerPeerResource(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_peer")
	sets := recordCalls(t, srv, "wg-server", "set_peer")
	generates := recordCalls(t, srv, "wg-server", "generate_peer")
	updated := fullWGServerPeer.with(func(p *wgServerPeer) {
		p.name, p.presharedKey, p.allowedIPs = "renamed", "", []string{"10.0.0.0/24"}
	})
	unset := updated.with(func(p *wgServerPeer) {
		p.allowedIPs, p.dns, p.keepalive, p.mtu = nil, nil, 0, 0
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterPeers(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fullWGServerPeer.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("id", positiveInt64),
					expectWGPeer("name", knownvalue.StringExact("test-peer")),
					expectWGPeer("preshared_key", knownvalue.StringExact(testPeerPresharedKey)),
					expectWGPeer("allowed_ips", stringListExact("0.0.0.0/0", "::/0")),
					expectWGPeer("dns", stringListExact("192.168.8.1")),
					expectWGPeer("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGPeer("mtu", knownvalue.Int64Exact(1420)),
					expectWGPeer("client_ip", knownvalue.StringExact("10.0.0.2")),
					expectWGPeerClientConfig("address", knownvalue.StringExact("10.0.0.2")),
					expectWGPeerClientConfig("private_key", knownvalue.StringExact(fakePeerPrivateKey)),
					expectWGPeerClientConfig("dns", stringListExact("192.168.8.1")),
					expectWGPeerClientConfig("mtu", knownvalue.Int64Exact(1420)),
					expectWGPeerClientConfig("listen_port", knownvalue.Null()),
					expectWGPeerClientConfig("public_key", knownvalue.StringExact(fakeServerPublicKey)),
					expectWGPeerClientConfig("preshared_key", knownvalue.StringExact(testPeerPresharedKey)),
					expectWGPeerClientConfig("allowed_ips", stringListExact("0.0.0.0/0", "::/0")),
					expectWGPeerClientConfig("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGPeerClientConfig("domain_endpoint", knownvalue.StringExact("router.example.com:51820")),
					expectWGPeerClientConfig("ip_endpoint", knownvalue.StringExact("203.0.113.1:51820")),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterPeers(client, "test-peer"),
					expectPeerCalls(generates, `{"peer_id":PEER_ID}`),
					adds.expect(`{"name":"test-peer","presharedkey_enable":true,"presharedkey":"`+testPeerPresharedKey+`",
						"allowed_ips":"0.0.0.0/0, ::/0","dns":"192.168.8.1","persistent_keepalive":25,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + updated.config(),
				ConfigPlanChecks: expectResourceAction(wgPeerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("name", knownvalue.StringExact("renamed")),
					expectWGPeer("preshared_key", knownvalue.Null()),
					expectWGPeer("allowed_ips", stringListExact("10.0.0.0/24")),
					expectWGPeer("client_ip", knownvalue.StringExact("10.0.0.2")),
					expectWGPeerClientConfig("allowed_ips", stringListExact("10.0.0.0/24")),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterPeers(client, "renamed"),
					adds.expect(),
					expectPeerCalls(generates, `{"peer_id":PEER_ID}`),
					expectPeerCalls(sets, `{"peer_id":PEER_ID,"name":"renamed","presharedkey_enable":false,"presharedkey":"",
						"allowed_ips":"10.0.0.0/24","dns":"192.168.8.1","persistent_keepalive":25,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + unset.config(),
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("allowed_ips", stringListExact("10.0.0.0/24")),
					expectWGPeer("dns", stringListExact("192.168.8.1")),
					expectWGPeer("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGPeer("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: generates.expect(),
			},
			{
				ResourceName:      wgPeerAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestWireGuardServerPeerResourceClientConfigNotRefreshed checks that a refresh does not call generate_peer,
// so that client_config keeps its value when the router changes the server, and that a read calls it when the
// state has no client_config.
func TestWireGuardServerPeerResourceClientConfigNotRefreshed(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	generates := recordCalls(t, srv, "wg-server", "generate_peer")
	config := fakeRouterConfig(srv) + fullWGServerPeer.config()
	var id int64
	saveID := func(s *terraform.State) (err error) {
		id, err = stateID(s, wgPeerAddress)
		return err
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  saveID,
			},
			{
				PreConfig: func() {
					generates.take()
					srv.WireGuardServer.SetConfig(json.RawMessage(`{"initialization":true,"port":51821,"public_key":"OTHER+MADE+UP+SERVER+PUBLIC+KEY+FOR+TESTS+0=","private_key":""}`))
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeerClientConfig("public_key", knownvalue.StringExact(fakeServerPublicKey)),
					expectWGPeerClientConfig("domain_endpoint", knownvalue.StringExact("router.example.com:51820")),
				},
				Check: generates.expect(),
			},
			{
				ResourceName:            wgPeerAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_config"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if got := states[0].Attributes["client_config.public_key"]; got != "OTHER+MADE+UP+SERVER+PUBLIC+KEY+FOR+TESTS+0=" {
						return fmt.Errorf("client_config.public_key after import is %q", got)
					}
					return generates.expect(fmt.Sprintf(`{"peer_id":%d}`, id))(nil)
				},
			},
		},
	})
}

func TestWireGuardServerPeerResourceMinimal(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_peer")
	config := fakeRouterConfig(srv) + wgServerPeer{name: "test-peer"}.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("preshared_key", knownvalue.Null()),
					expectWGPeer("allowed_ips", knownvalue.Null()),
					expectWGPeer("dns", knownvalue.Null()),
					expectWGPeer("persistent_keepalive", knownvalue.Null()),
					expectWGPeer("mtu", knownvalue.Null()),
					expectWGPeer("client_ip", knownvalue.StringExact("10.0.0.2")),
				},
				Check: adds.expect(`{"name":"test-peer","presharedkey_enable":false,"presharedkey":"","persistent_keepalive":"","mtu":""}`),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestWireGuardServerPeerResourceNotConfigured sends back the router values of the attributes that are not in the
// configuration, also after the router values change, and "" for a number that the router does not have.
func TestWireGuardServerPeerResourceNotConfigured(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	sets := recordCalls(t, srv, "wg-server", "set_peer")
	ctx := context.Background()
	setRouterPeer := func(change func(*glinet.WireGuardServerPeer)) func() {
		return func() {
			peers, err := client.WireGuardServer.ListPeers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			change(&peers[0])
			if err := client.WireGuardServer.SetPeer(ctx, peers[0]); err != nil {
				t.Fatal(err)
			}
			sets.take()
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wgServerPeer{name: "test-peer"}.config(),
			},
			{
				PreConfig: setRouterPeer(func(p *glinet.WireGuardServerPeer) {
					p.AllowedIPs, p.DNS, p.PersistentKeepalive, p.MTU = "10.0.0.0/24", "192.168.8.1", 25, 1420
				}),
				Config:           fakeRouterConfig(srv) + wgServerPeer{name: "renamed"}.config(),
				ConfigPlanChecks: expectResourceAction(wgPeerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("allowed_ips", stringListExact("10.0.0.0/24")),
					expectWGPeer("dns", stringListExact("192.168.8.1")),
					expectWGPeer("persistent_keepalive", knownvalue.Int64Exact(25)),
					expectWGPeer("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: expectPeerCalls(sets, `{"peer_id":PEER_ID,"name":"renamed","presharedkey_enable":false,"presharedkey":"",
					"allowed_ips":"10.0.0.0/24","dns":"192.168.8.1","persistent_keepalive":25,"mtu":1420}`),
			},
			{
				PreConfig:        setRouterPeer(func(p *glinet.WireGuardServerPeer) { p.PersistentKeepalive, p.MTU = 0, 0 }),
				Config:           fakeRouterConfig(srv) + wgServerPeer{name: "test-peer"}.config(),
				ConfigPlanChecks: expectResourceAction(wgPeerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("allowed_ips", stringListExact("10.0.0.0/24")),
					expectWGPeer("dns", stringListExact("192.168.8.1")),
					expectWGPeer("persistent_keepalive", knownvalue.Null()),
					expectWGPeer("mtu", knownvalue.Null()),
				},
				Check: expectPeerCalls(sets, `{"peer_id":PEER_ID,"name":"test-peer","presharedkey_enable":false,"presharedkey":"",
					"allowed_ips":"10.0.0.0/24","dns":"192.168.8.1","persistent_keepalive":"","mtu":""}`),
			},
		},
	})
}

func TestWireGuardServerPeerResourceNotInitialized(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "wg-server", "add_peer")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + fullWGServerPeer.config(),
				ExpectError: regexp.MustCompile(`(?s)WireGuard Server Not Initialized.*` + wrapped("Initialize it with glinet_wireguard_server or in the web UI")),
			},
		},
	})

	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-server.add_peer was called with %q", got)
	}
}

func TestWireGuardServerPeerResourceDrift(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	config := fakeRouterConfig(srv) + fullWGServerPeer.config()
	var id int64
	saveID := func(s *terraform.State) (err error) {
		id, err = stateID(s, wgPeerAddress)
		return err
	}
	ctx := context.Background()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  saveID,
			},
			{
				PreConfig: func() {
					peers, err := client.WireGuardServer.ListPeers(ctx)
					if err != nil {
						t.Fatal(err)
					}
					peer := peers[0]
					peer.Name, peer.DNS, peer.PresharedKeyEnabled = "other", "10.0.0.1", false
					if err := client.WireGuardServer.SetPeer(ctx, peer); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgPeerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGPeer("name", knownvalue.StringExact("test-peer")),
					expectWGPeer("dns", stringListExact("192.168.8.1")),
					expectWGPeer("preshared_key", knownvalue.StringExact(testPeerPresharedKey)),
				},
			},
			{
				PreConfig: func() {
					if err := client.WireGuardServer.RemovePeer(ctx, id); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgPeerAddress, plancheck.ResourceActionCreate),
				Check:            expectRouterPeers(client, "test-peer"),
			},
		},
	})
}

func TestWireGuardServerPeerResourceImportErrors(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)

	tests := []struct {
		id, error string
	}{
		{"99999", `Cannot import non-existent remote object`},
	}
	for _, id := range []string{"peer", "0", "-1", "01", "1/2"} {
		tests = append(tests, struct{ id, error string }{id, `(?s)Invalid Import ID.*must be the peer ID`})
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + fullWGServerPeer.config(),
					ResourceName:  wgPeerAddress,
					ImportState:   true,
					ImportStateId: tt.id,
					ExpectError:   regexp.MustCompile(tt.error),
				},
			},
		})
	}
}

func TestWireGuardServerPeerResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	adds := recordCalls(t, srv, "wg-server", "add_peer")
	p := fullWGServerPeer

	type test struct {
		peer  wgServerPeer
		error string
	}
	tests := []test{
		{p.with(func(p *wgServerPeer) { p.name = ".peer" }), `name must have 1 to 64 characters`},
		{p.with(func(p *wgServerPeer) { p.presharedKey = "short=" }), `preshared_key must be a WireGuard key`},
		{p.with(func(p *wgServerPeer) { p.allowedIPs = []string{} }), `allowed_ips list must contain at least 1 elements and at most 5`},
		{p.with(func(p *wgServerPeer) {
			p.allowedIPs = []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24", "10.0.5.0/24"}
		}), `allowed_ips list must contain at least 1 elements and at most 5`},
		{p.with(func(p *wgServerPeer) { p.allowedIPs = []string{"10.0.0.1"} }), `allowed_ips\[0\] must be an IP address with a prefix length`},
		{p.with(func(p *wgServerPeer) { p.dns = []string{} }), `dns list must contain at least 1 elements`},
		{p.with(func(p *wgServerPeer) { p.dns = []string{"dns.example.com"} }), `dns\[0\] must be an IP address`},
		{p.with(func(p *wgServerPeer) { p.keepalive = 65536 }), `persistent_keepalive value must be between 1 and\s+65535`},
		{p.with(func(p *wgServerPeer) { p.mtu = 575 }), `mtu value must be between 576 and 65535`},
		{p.with(func(p *wgServerPeer) { p.mtu = 65536 }), `mtu value must be between 576 and 65535`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.peer.config(),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}
	if got := adds.take(); len(got) != 0 {
		t.Errorf("wg-server.add_peer was called with %q", got)
	}
}

func TestWireGuardServerPeerResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := []struct {
		method, error string
		step          int
	}{
		{"get_config", "Unable to Read WireGuard Server Configuration", 0},
		{"add_peer", "Unable to Create WireGuard Server Peer", 0},
		{"get_peer_list", "Unable to Read WireGuard Server Peers", 1},
		{"set_peer", "Unable to Update WireGuard Server Peer", 1},
		{"remove_peer", "Unable to Delete WireGuard Server Peer", 2},
		{"generate_peer", "Unable to Read WireGuard Server Peer Client Configuration", 0},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			srv, _ := wgServerTestRouter(t)
			configs := []string{
				fakeRouterConfig(srv) + fullWGServerPeer.config(),
				fakeRouterConfig(srv) + fullWGServerPeer.with(func(p *wgServerPeer) { p.name = "renamed" }).config(),
				fakeRouterConfig(srv),
			}
			steps := make([]resource.TestStep, tt.step+1)
			for i := range steps {
				steps[i].Config = configs[i]
			}
			steps[tt.step].PreConfig = func() { failFirstCall(srv, "wg-server", tt.method) }
			steps[tt.step].ExpectError = regexp.MustCompile(`(?s)` + wrapped(tt.error) + `.*Method not found`)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps:                    steps,
			})
		})
	}
}

// TestWireGuardSecretsNotInDiagnostics runs the validators of the sensitive attributes with an invalid secret
// and checks that no diagnostic shows it.
func TestWireGuardSecretsNotInDiagnostics(t *testing.T) {
	parallel(t)
	const secret = "SECRET-VALUE-THAT-IS-NOT-A-KEY"
	ctx := context.Background()

	for _, tt := range []struct {
		resource  fwresource.Resource
		sensitive []string
	}{
		{NewWireGuardClientConfigResource(), []string{"preshared_key", "private_key"}},
		{NewWireGuardServerPeerResource(), []string{"preshared_key"}},
		{NewWireGuardServerResource(), []string{"private_key"}},
	} {
		r := tt.resource
		var resp fwresource.SchemaResponse
		r.Schema(ctx, fwresource.SchemaRequest{}, &resp)

		var sensitive []string
		for name, a := range resp.Schema.Attributes {
			s, ok := a.(schema.StringAttribute)
			if !ok || !s.Sensitive {
				continue
			}
			sensitive = append(sensitive, name)
			for _, v := range s.Validators {
				vresp := &validator.StringResponse{}
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root(name), ConfigValue: types.StringValue(secret)}, vresp)
				if !vresp.Diagnostics.HasError() {
					t.Errorf("%s: no error for an invalid value", name)
				}
				for _, d := range vresp.Diagnostics {
					if strings.Contains(d.Summary()+d.Detail(), secret) {
						t.Errorf("%s: the diagnostic shows the value: %s: %s", name, d.Summary(), d.Detail())
					}
				}
			}
		}
		slices.Sort(sensitive)
		if !slices.Equal(sensitive, tt.sensitive) {
			t.Errorf("%T has the sensitive attributes %q, want %q", r, sensitive, tt.sensitive)
		}
	}
}

func TestWireGuardServerPeerClientConfigSecretsSensitive(t *testing.T) {
	parallel(t)
	var resp fwresource.SchemaResponse
	NewWireGuardServerPeerResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	clientConfig, ok := resp.Schema.Attributes["client_config"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("client_config is %T, want a single nested attribute", resp.Schema.Attributes["client_config"])
	}
	var sensitive []string
	for name, a := range clientConfig.Attributes {
		if a.IsSensitive() {
			sensitive = append(sensitive, name)
		}
	}
	slices.Sort(sensitive)
	if want := []string{"preshared_key", "private_key"}; !slices.Equal(sensitive, want) {
		t.Errorf("client_config has the sensitive attributes %q, want %q", sensitive, want)
	}
}
