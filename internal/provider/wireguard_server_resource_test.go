package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	wgServerAddress = "glinet_wireguard_server.test"

	fakeServerPrivateKey = "MADE+UP+SERVER+PRIVATE+KEY+FOR+TESTS+0000000="
	testServerPrivateKey = "TEST+SERVER+PRIVATE+KEY+000000000000000000A="
)

// wgServer is a configuration of glinet_wireguard_server. The nil fields are left out.
type wgServer struct {
	addressV4, addressV6, privateKey     *string
	listenPort, mtu                      *int
	clientToClient, localAccess, masquer *bool
}

var fullWGServer = wgServer{
	addressV4:      ptr("10.0.0.1/24"),
	addressV6:      ptr("fd00:db8::1/64"),
	listenPort:     ptr(51821),
	clientToClient: ptr(true),
	localAccess:    ptr(true),
	masquer:        ptr(false),
	mtu:            ptr(1420),
}

func (s wgServer) with(change func(*wgServer)) wgServer {
	change(&s)
	return s
}

func (s wgServer) config() string {
	var b strings.Builder
	b.WriteString("\nresource \"glinet_wireguard_server\" \"test\" {\n")
	for _, a := range []struct {
		name  string
		value *string
	}{{"address_v4", s.addressV4}, {"address_v6", s.addressV6}, {"private_key", s.privateKey}} {
		if a.value != nil {
			fmt.Fprintf(&b, "  %s = %q\n", a.name, *a.value)
		}
	}
	for _, a := range []struct {
		name  string
		value *int
	}{{"listen_port", s.listenPort}, {"mtu", s.mtu}} {
		if a.value != nil {
			fmt.Fprintf(&b, "  %s = %d\n", a.name, *a.value)
		}
	}
	for _, a := range []struct {
		name  string
		value *bool
	}{{"client_to_client", s.clientToClient}, {"local_access", s.localAccess}, {"masquerade", s.masquer}} {
		if a.value != nil {
			fmt.Fprintf(&b, "  %s = %t\n", a.name, *a.value)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func expectWGServer(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(wgServerAddress, tfjsonpath.New(attribute), value)
}

// wgServerCalls records the writes of the WireGuard server configuration and options.
type wgServerCalls struct {
	generateKey, setConfig, setSetting *callRecorder
}

func recordWGServerCalls(t *testing.T, srv *glinettest.Server) wgServerCalls {
	return wgServerCalls{
		generateKey: recordCalls(t, srv, "wg-server", "generate_key"),
		setConfig:   recordCalls(t, srv, "wg-server", "set_config"),
		setSetting:  recordCalls(t, srv, "wg-server", "set_setting"),
	}
}

func (c wgServerCalls) expectNone() resource.TestCheckFunc {
	return resource.ComposeTestCheckFunc(c.generateKey.expect(), c.setConfig.expect(), c.setSetting.expect())
}

func TestWireGuardServerResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordWGServerCalls(t, srv)
	unset := wgServer{}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             calls.expectNone(),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fullWGServer.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("id", knownvalue.StringExact("wireguard_server")),
					expectWGServer("address_v4", knownvalue.StringExact("10.0.0.1/24")),
					expectWGServer("address_v6", knownvalue.StringExact("fd00:db8::1/64")),
					expectWGServer("listen_port", knownvalue.Int64Exact(51821)),
					expectWGServer("private_key", knownvalue.StringExact(fakeServerPrivateKey)),
					expectWGServer("public_key", knownvalue.StringExact(fakeServerPublicKey)),
					expectWGServer("client_to_client", knownvalue.Bool(true)),
					expectWGServer("local_access", knownvalue.Bool(true)),
					expectWGServer("masquerade", knownvalue.Bool(false)),
					expectWGServer("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: resource.ComposeTestCheckFunc(
					calls.generateKey.expect(`{}`),
					calls.setConfig.expect(`{"address_v4":"10.0.0.1/24","address_v6":"fd00:db8::1/64","ipv6_enable":false,"port":51821,
						"private_key":"`+fakeServerPrivateKey+`"}`),
					calls.setSetting.expect(`{"client_to_client":true,"local_access":true,"masq":false,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + fullWGServer.with(func(s *wgServer) { s.masquer = ptr(true) }).config(),
				ConfigPlanChecks: expectResourceAction(wgServerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("masquerade", knownvalue.Bool(true)),
					expectWGServer("public_key", knownvalue.StringExact(fakeServerPublicKey)),
				},
				Check: resource.ComposeTestCheckFunc(
					calls.generateKey.expect(),
					calls.setConfig.expect(),
					calls.setSetting.expect(`{"client_to_client":true,"local_access":true,"masq":true,"mtu":1420}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + unset.config(),
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("address_v4", knownvalue.StringExact("10.0.0.1/24")),
					expectWGServer("mtu", knownvalue.Int64Exact(1420)),
				},
				Check: calls.expectNone(),
			},
			{
				Config:           fakeRouterConfig(srv) + unset.with(func(s *wgServer) { s.listenPort = ptr(51822) }).config(),
				ConfigPlanChecks: expectResourceAction(wgServerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("listen_port", knownvalue.Int64Exact(51822)),
					expectWGServer("masquerade", knownvalue.Bool(true)),
				},
				Check: resource.ComposeTestCheckFunc(
					calls.setConfig.expect(`{"address_v4":"10.0.0.1/24","address_v6":"fd00:db8::1/64","ipv6_enable":false,"port":51822,
						"private_key":"`+fakeServerPrivateKey+`"}`),
					calls.setSetting.expect(),
				),
			},
			{
				ResourceName:      wgServerAddress,
				ImportState:       true,
				ImportStateId:     "wireguard_server",
				ImportStateVerify: true,
			},
		},
	})
}

// TestWireGuardServerResourceMinimal initializes the server and sends no other write when no attribute is set.
func TestWireGuardServerResourceMinimal(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordWGServerCalls(t, srv)
	config := fakeRouterConfig(srv) + wgServer{}.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("address_v4", knownvalue.StringExact("10.66.0.9/24")),
					expectWGServer("address_v6", knownvalue.StringExact("fd00:66::1/64")),
					expectWGServer("listen_port", knownvalue.Int64Exact(51820)),
					expectWGServer("private_key", knownvalue.StringExact(fakeServerPrivateKey)),
					expectWGServer("public_key", knownvalue.StringExact(fakeServerPublicKey)),
					expectWGServer("client_to_client", knownvalue.Bool(false)),
					expectWGServer("local_access", knownvalue.Bool(false)),
					expectWGServer("masquerade", knownvalue.Bool(true)),
					expectWGServer("mtu", knownvalue.Int64Exact(0)),
				},
				Check: resource.ComposeTestCheckFunc(calls.generateKey.expect(`{}`), calls.setConfig.expect(), calls.setSetting.expect()),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestWireGuardServerResourceTakeOver does not initialize an initialized server and sends no write when the
// configuration has the router values.
func TestWireGuardServerResourceTakeOver(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)
	routerValues := wgServer{
		addressV4:      ptr("10.66.0.9/24"),
		addressV6:      ptr("fd00:66::1/64"),
		listenPort:     ptr(51820),
		clientToClient: ptr(false),
		localAccess:    ptr(false),
		masquer:        ptr(true),
		mtu:            ptr(0),
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + routerValues.config(),
				Check:  calls.expectNone(),
			},
		},
	})
}

func TestWireGuardServerResourcePrivateKey(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)
	const publicKey = "TEST+SERVER+PUBLIC+KEY+0000000000000000000A="
	srv.Handle("wg-server", "set_config", func(params json.RawMessage) (any, error) {
		srv.WireGuardServer.SetConfig(params)
		srv.WireGuardServer.SetConfig(json.RawMessage(`{"public_key":"` + publicKey + `"}`))
		return []any{}, nil
	})
	calls.setConfig = recordCalls(t, srv, "wg-server", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wgServer{privateKey: ptr(testServerPrivateKey)}.config(),
				ConfigStateChecks: []statecheck.StateCheck{
					expectWGServer("private_key", knownvalue.StringExact(testServerPrivateKey)),
					expectWGServer("public_key", knownvalue.StringExact(publicKey)),
				},
				Check: calls.setConfig.expect(`{"address_v4":"10.66.0.9/24","address_v6":"fd00:66::1/64","ipv6_enable":false,"port":51820,
					"private_key":"` + testServerPrivateKey + `"}`),
			},
			{
				Config:           fakeRouterConfig(srv) + wgServer{}.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

// TestWireGuardServerResourceNoIPv6 sends address_v6 "" without ipv6_enable when address_v6 is "".
func TestWireGuardServerResourceNoIPv6(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + wgServer{addressV6: ptr("")}.config(),
				ConfigStateChecks: []statecheck.StateCheck{expectWGServer("address_v6", knownvalue.StringExact(""))},
				Check: calls.setConfig.expect(`{"address_v4":"10.66.0.9/24","address_v6":"","port":51820,
					"private_key":"` + fakeServerPrivateKey + `"}`),
			},
		},
	})
}

// TestWireGuardServerResourceNoIPv6Enable sends no ipv6_enable when the router does not return it, as firmware 4.8.3 does.
func TestWireGuardServerResourceNoIPv6Enable(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	srv.WireGuardServer.RemoveConfigKey("ipv6_enable")
	calls := recordWGServerCalls(t, srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + wgServer{addressV6: ptr("fd00:db8::1/64")}.config(),
				ConfigStateChecks: []statecheck.StateCheck{expectWGServer("address_v6", knownvalue.StringExact("fd00:db8::1/64"))},
				Check: calls.setConfig.expect(`{"address_v4":"10.66.0.9/24","address_v6":"fd00:db8::1/64","port":51820,
					"private_key":"` + fakeServerPrivateKey + `"}`),
			},
		},
	})
}

// TestWireGuardServerResourceIPv6PrefixLengths accepts IPv6 prefix lengths other than 64, up to 126.
func TestWireGuardServerResourceIPv6PrefixLengths(t *testing.T) {
	parallel(t)
	srv, _ := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)

	var steps []resource.TestStep
	for _, address := range []string{"fd00:db8::1/48", "fd00:db8::1/126"} {
		steps = append(steps, resource.TestStep{
			Config:            fakeRouterConfig(srv) + wgServer{addressV6: ptr(address)}.config(),
			ConfigStateChecks: []statecheck.StateCheck{expectWGServer("address_v6", knownvalue.StringExact(address))},
			Check: calls.setConfig.expect(`{"address_v4":"10.66.0.9/24","address_v6":"` + address + `","ipv6_enable":false,"port":51820,
				"private_key":"` + fakeServerPrivateKey + `"}`),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

// TestWireGuardServerResourceRunning refuses a configuration write while the server runs, and permits an options write.
func TestWireGuardServerResourceRunning(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)
	start := func() {
		if err := client.WireGuardServer.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: start,
				Config:    fakeRouterConfig(srv) + wgServer{listenPort: ptr(51821)}.config(),
				ExpectError: regexp.MustCompile(`(?s)WireGuard Server Runs.*` +
					wrapped("Stop the server in the web UI, run terraform apply again, then start the server.")),
			},
			{
				Config: fakeRouterConfig(srv) + wgServer{masquer: ptr(false)}.config(),
				Check:  resource.ComposeTestCheckFunc(calls.setConfig.expect(), calls.setSetting.expect(`{"client_to_client":false,"local_access":false,"masq":false,"mtu":0}`)),
			},
		},
	})
}

func TestWireGuardServerResourceDrift(t *testing.T) {
	parallel(t)
	srv, client := wgServerTestRouter(t)
	calls := recordWGServerCalls(t, srv)
	config := fakeRouterConfig(srv) + fullWGServer.config()
	ctx := context.Background()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  func(*terraform.State) error { calls.setConfig.take(); calls.setSetting.take(); return nil },
			},
			{
				PreConfig: func() {
					setting, err := client.WireGuardServer.GetSetting(ctx)
					if err != nil {
						t.Fatal(err)
					}
					setting.MTU = 0
					if err := client.WireGuardServer.SetSetting(ctx, *setting); err != nil {
						t.Fatal(err)
					}
					calls.setSetting.take()
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(wgServerAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectWGServer("mtu", knownvalue.Int64Exact(1420))},
				Check: resource.ComposeTestCheckFunc(
					calls.setConfig.expect(),
					calls.setSetting.expect(`{"client_to_client":true,"local_access":true,"masq":false,"mtu":1420}`),
				),
			},
			{
				PreConfig: func() {
					srv.WireGuardServer.SetConfig(json.RawMessage(`{"initialization":false,"private_key":"","public_key":""}`))
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(wgServerAddress, plancheck.ResourceActionCreate),
				Check:            calls.generateKey.expect(`{}`),
			},
		},
	})
}

func TestWireGuardServerResourceImportErrors(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, tt := range []struct{ id, error string }{
		{"wireguard_server", `Cannot import non-existent remote object`},
		{"wg-server", `(?s)Invalid Import ID.*must be "wireguard_server"`},
	} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + wgServer{}.config(),
					ResourceName:  wgServerAddress,
					ImportState:   true,
					ImportStateId: tt.id,
					ExpectError:   regexp.MustCompile(tt.error),
				},
			},
		})
	}
}

func TestWireGuardServerResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordWGServerCalls(t, srv)

	tests := []struct {
		server wgServer
		error  string
	}{
		{wgServer{addressV4: ptr("10.0.0.1")}, wrapped("address_v4 must be a private IPv4 address with a prefix length up to 30")},
		{wgServer{addressV4: ptr("203.0.113.1/24")}, `address_v4 must be a private IPv4 address`},
		{wgServer{addressV4: ptr("10.0.0.1/31")}, `address_v4 must be a private IPv4 address`},
		{wgServer{addressV4: ptr("10.0.0.1/4")}, `address_v4 must be a private IPv4 address`},
		{wgServer{addressV4: ptr("172.16.0.1/11")}, `address_v4 must be a private IPv4 address`},
		{wgServer{addressV6: ptr("fd00:db8::1/127")}, wrapped(`address_v6 must be "" or an IPv6 address with a prefix length up to 126`)},
		{wgServer{addressV6: ptr("fd00:db8::1")}, wrapped(`address_v6 must be "" or an IPv6 address with a prefix length up to 126`)},
		{wgServer{addressV6: ptr("::ffff:10.0.0.1/120")}, wrapped(`address_v6 must be "" or an IPv6 address with a prefix length up to 126`)},
		{wgServer{listenPort: ptr(0)}, `listen_port value must be between 1 and 65535`},
		{wgServer{privateKey: ptr("short=")}, wrapped("private_key must be a WireGuard key")},
		{wgServer{mtu: ptr(67)}, `mtu value must be between 68 and 65535`},
		{wgServer{mtu: ptr(65536)}, `mtu value must be between 68 and 65535`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.server.config(),
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}
	if err := calls.expectNone()(nil); err != nil {
		t.Error(err)
	}
}

func TestWireGuardServerResourceRouterErrors(t *testing.T) {
	parallel(t)

	tests := []struct {
		method, error string
		step          int
	}{
		{"get_config", "Unable to Read WireGuard Server", 0},
		{"generate_key", "Unable to Initialize WireGuard Server", 0},
		{"get_setting", "Unable to Read WireGuard Server", 0},
		{"get_status", "Unable to Read WireGuard Server Status", 0},
		{"set_config", "Unable to Set WireGuard Server Configuration", 0},
		{"set_setting", "Unable to Set WireGuard Server Options", 0},
		{"get_config", "Unable to Read WireGuard Server", 1},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%d", tt.method, tt.step), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			steps := []resource.TestStep{
				{Config: fakeRouterConfig(srv) + fullWGServer.config()},
				{Config: fakeRouterConfig(srv) + fullWGServer.with(func(s *wgServer) { s.listenPort = ptr(51822) }).config()},
			}[:tt.step+1]
			steps[tt.step].PreConfig = func() { failFirstCall(srv, "wg-server", tt.method) }
			steps[tt.step].ExpectError = regexp.MustCompile(`(?s)` + wrapped(tt.error) + `.*Method not found`)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps:                    steps,
			})
		})
	}
}
