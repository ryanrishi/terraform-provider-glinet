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

	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	forwardAddress = "glinet_firewall_port_forward.test"
	forwardIP      = "192.168.8.250"
	otherForwardIP = "192.168.8.251"
)

// forwardTestPorts are the external ports that the tests use on the real router.
const forwardTestPorts = "50001-50010"

// forward returns a port forward from the zone wan to forwardIP in the zone lan.
func forward(proto, srcDport, destPort, name string) glinet.PortForward {
	return glinet.PortForward{
		Name:     name,
		Proto:    proto,
		Src:      "wan",
		SrcDport: srcDport,
		Dest:     "lan",
		DestIP:   forwardIP,
		DestPort: destPort,
		Enabled:  true,
	}
}

// forwardResource returns the configuration of f. It leaves out name when it is empty and enabled when it is true.
func forwardResource(label string, f glinet.PortForward) string {
	var optional strings.Builder
	if f.Name != "" {
		fmt.Fprintf(&optional, "  name      = %q\n", f.Name)
	}
	if !f.Enabled {
		optional.WriteString("  enabled   = false\n")
	}

	return fmt.Sprintf(`
resource "glinet_firewall_port_forward" %q {
  src       = %q
  proto     = %q
  src_dport = %q
  dest      = %q
  dest_ip   = %q
  dest_port = %q
%s}
`, label, f.Src, f.Proto, f.SrcDport, f.Dest, f.DestIP, f.DestPort, optional.String())
}

func forwardConfig(f glinet.PortForward) string {
	return forwardResource("test", f)
}

func fake(f glinet.PortForward) glinettest.PortForward {
	return glinettest.PortForward{
		Name:     f.Name,
		Proto:    f.Proto,
		Src:      f.Src,
		SrcDport: f.SrcDport,
		Dest:     f.Dest,
		DestIP:   f.DestIP,
		DestPort: f.DestPort,
		Enabled:  f.Enabled,
	}
}

func fakes(forwards ...glinet.PortForward) []glinettest.PortForward {
	res := []glinettest.PortForward{}
	for _, f := range forwards {
		res = append(res, fake(f))
	}
	return res
}

func identityOf(f glinet.PortForward) portForwardIdentity {
	return portForwardIdentity{src: f.Src, proto: f.Proto, srcDport: f.SrcDport}
}

func expectForward(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(forwardAddress, tfjsonpath.New(attribute), value)
}

func expectForwardState(f glinet.PortForward) []statecheck.StateCheck {
	var name knownvalue.Check = knownvalue.Null()
	if f.Name != "" {
		name = knownvalue.StringExact(f.Name)
	}

	return []statecheck.StateCheck{
		expectForward("src", knownvalue.StringExact(f.Src)),
		expectForward("proto", knownvalue.StringExact(f.Proto)),
		expectForward("src_dport", knownvalue.StringExact(f.SrcDport)),
		expectForward("dest", knownvalue.StringExact(f.Dest)),
		expectForward("dest_ip", knownvalue.StringExact(f.DestIP)),
		expectForward("dest_port", knownvalue.StringExact(f.DestPort)),
		expectForward("name", name),
		expectForward("enabled", knownvalue.Bool(f.Enabled)),
	}
}

func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(forwardAddress, action),
		},
	}
}

// expectFakeForwards checks that the port forwards of srv are want, in this order.
func expectFakeForwards(srv *glinettest.Server, want ...glinet.PortForward) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Firewall.PortForwards(); !slices.Equal(got, fakes(want...)) {
			return fmt.Errorf("router port forwards are %v, want %v", got, fakes(want...))
		}
		return nil
	}
}

// forwardTestRouter returns the provider block and a client for the router of the test.
// On the real router, the test fails when a port forward uses one of forwardTestPorts. When the test ends,
// it removes the port forwards with the identities ids one at a time and checks that the port forwards are as before.
func forwardTestRouter(t *testing.T, ids ...portForwardIdentity) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.Firewall.ListPortForwards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	testLo, testHi, _ := parsePorts(forwardTestPorts)
	for _, f := range initial {
		if lo, hi, ok := parsePorts(f.SrcDport); ok && lo <= testHi && testLo <= hi {
			t.Fatalf("the router has a port forward %s on the test ports %s", describePortForward(f), forwardTestPorts)
		}
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range ids {
			for {
				forwards, err := client.Firewall.ListPortForwards(ctx)
				if err != nil {
					t.Fatalf("read port forwards: %v", err)
				}
				i := slices.IndexFunc(forwards, id.matches)
				if i < 0 {
					break
				}
				if err := client.Firewall.RemovePortForward(ctx, forwards[i].ID); err != nil {
					t.Fatalf("remove port forward %s: %v", describePortForward(forwards[i]), err)
				}
			}
		}

		forwards, err := client.Firewall.ListPortForwards(ctx)
		if err != nil {
			t.Fatalf("read port forwards: %v", err)
		}
		if !slices.Equal(forwards, initial) {
			t.Errorf("port forwards are %v, want %v", forwards, initial)
		}
	})

	return `provider "glinet" {}`, client
}

// expectRouterForward checks that the first port forward on the router with the identity id is want, without its ID.
// A nil want checks that the router has no port forward with the identity id.
func expectRouterForward(client *glinet.Client, id portForwardIdentity, want *glinet.PortForward) resource.TestCheckFunc {
	return func(*terraform.State) error {
		forwards, err := client.Firewall.ListPortForwards(context.Background())
		if err != nil {
			return err
		}

		i := slices.IndexFunc(forwards, id.matches)
		switch {
		case want == nil && i >= 0:
			return fmt.Errorf("router has port forward %v, want none", forwards[i])
		case want != nil && i < 0:
			return fmt.Errorf("router has no port forward with %s", id)
		case want != nil:
			got := forwards[i]
			got.ID = ""
			if got != *want {
				return fmt.Errorf("router port forward is %v, want %v", got, *want)
			}
		}
		return nil
	}
}

func TestFirewallPortForwardResource(t *testing.T) {
	created := forward("tcp", "50001", "8080", "tf-test-forward")
	updated := forward("tcp", "50001", "8081", "tf-test-renamed")
	updated.Enabled = false
	id := identityOf(created)
	provider, client := forwardTestRouter(t, id)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterForward(client, id, nil),
		Steps: []resource.TestStep{
			{
				Config:            provider + forwardConfig(created),
				ConfigStateChecks: expectForwardState(created),
				Check:             expectRouterForward(client, id, &created),
			},
			{
				Config:            provider + forwardConfig(updated),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectForwardState(updated),
				Check:             expectRouterForward(client, id, &updated),
			},
			{
				ResourceName:                         forwardAddress,
				ImportState:                          true,
				ImportStateId:                        "wan/tcp/50001",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "src_dport",
			},
		},
	})
}

func TestFirewallPortForwardResourceUpdate(t *testing.T) {
	srv := glinettest.NewServer(t)
	noName := forward("tcp", "50001", "8080", "")
	named := forward("tcp", "50001", "8080", "first")
	renamed := forward("tcp", "50001", "8080", "second")
	renamed.DestIP = otherForwardIP
	disabled := forward("tcp", "50001", "8081", "")
	disabled.DestIP = otherForwardIP
	disabled.Enabled = false
	enabled := disabled
	enabled.Enabled = true

	var steps []resource.TestStep
	for i, f := range []glinet.PortForward{noName, named, renamed, disabled, enabled} {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + forwardConfig(f),
			ConfigStateChecks: expectForwardState(f),
			Check:             expectFakeForwards(srv, f),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectAction(plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv),
		Steps:                    steps,
	})
}

func TestFirewallPortForwardResourceReplace(t *testing.T) {
	initial := forward("tcp", "50001", "8080", "test")
	guest := initial
	guest.Src = "guest"

	tests := []struct {
		name    string
		changed glinet.PortForward
	}{
		{"src", guest},
		{"proto", forward("tcp udp", "50001", "8080", "test")},
		{"src_dport", forward("tcp", "50002-50003", "8080", "test")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeForwards(srv),
				Steps: []resource.TestStep{
					{
						Config: fakeRouterConfig(srv) + forwardConfig(initial),
						Check:  expectFakeForwards(srv, initial),
					},
					{
						Config:            fakeRouterConfig(srv) + forwardConfig(tt.changed),
						ConfigPlanChecks:  expectAction(plancheck.ResourceActionDestroyBeforeCreate),
						ConfigStateChecks: expectForwardState(tt.changed),
						Check:             expectFakeForwards(srv, tt.changed),
					},
				},
			})
		})
	}
}

func TestFirewallPortForwardResourceImport(t *testing.T) {
	disabled := forward("udp", "50002", "8080", "")
	disabled.Enabled = false

	tests := []struct {
		importID string
		existing glinet.PortForward
	}{
		{"wan/tcp/50001", forward("tcp", "50001", "8080", "tcp")},
		{"wan/udp/50002", disabled},
		{"wan/tcp+udp/50003", forward("tcp udp", "50003", "8080", "both")},
		{"wan/tcp/50004-50005", forward("tcp", "50004-50005", "50004-50005", "range")},
	}

	for _, tt := range tests {
		t.Run(tt.importID, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			other := forward("tcp", "50010", "8080", "other")
			srv.Firewall.SetPortForwards(fakes(other, tt.existing))
			config := fakeRouterConfig(srv) + forwardConfig(tt.existing)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeForwards(srv, other),
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       forwardAddress,
						ImportState:        true,
						ImportStateId:      tt.importID,
						ImportStatePersist: true,
					},
					{
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						ConfigStateChecks: expectForwardState(tt.existing),
					},
				},
			})
		})
	}
}

func TestFirewallPortForwardResourceImportInvalidID(t *testing.T) {
	srv := glinettest.NewServer(t)

	for _, id := range []string{
		"wan/tcp",
		"wan/tcp/8080/x",
		"/tcp/8080",
		"wan/tcp udp/8080",
		"wan/TCP/8080",
		"wan/icmp/8080",
		"wan/tcp/",
		"wan/tcp/0",
		"wan/tcp/65536",
		"wan/tcp/8010-8000",
		"wan/tcp/http",
	} {
		t.Run(id, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        fakeRouterConfig(srv) + forwardConfig(forward("tcp", "8080", "8080", "")),
						ResourceName:  forwardAddress,
						ImportState:   true,
						ImportStateId: id,
						ExpectError:   regexp.MustCompile(`Invalid Import ID`),
					},
				},
			})
		})
	}
}

func TestFirewallPortForwardResourceImportNotFound(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(forward("udp", "50001", "8080", "")))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + forwardConfig(forward("tcp", "50001", "8080", "")),
				ResourceName:  forwardAddress,
				ImportState:   true,
				ImportStateId: "wan/tcp/50001",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

func TestFirewallPortForwardResourceDrift(t *testing.T) {
	srv := glinettest.NewServer(t)
	want := forward("tcp", "50001", "8080", "test")
	config := fakeRouterConfig(srv) + forwardConfig(want)
	changed := want
	changed.DestIP = otherForwardIP
	changed.DestPort = "9090"
	changed.Enabled = false
	renamed := want
	renamed.Name = "renamed"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv),
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { srv.Firewall.SetPortForwards(fakes(changed)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectForwardState(want),
				Check:             expectFakeForwards(srv, want),
			},
			{
				PreConfig:         func() { srv.Firewall.SetPortForwards(fakes(renamed)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectForwardState(want),
				Check:             expectFakeForwards(srv, want),
			},
			{
				PreConfig:         func() { srv.Firewall.SetPortForwards(nil) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectForwardState(want),
				Check:             expectFakeForwards(srv, want),
			},
		},
	})
}

// TestFirewallPortForwardResourceIDShift removes a port forward before the managed one on the router and
// adds one after it, so the router ID of the managed port forward when it was created is now the ID of the added one.
func TestFirewallPortForwardResourceIDShift(t *testing.T) {
	srv := glinettest.NewServer(t)
	first := forward("tcp", "50101", "8080", "first")
	second := forward("tcp", "50102", "8080", "second")
	added := forward("tcp", "50103", "8080", "added")
	managed := forward("tcp", "50001", "8080", "managed")
	updated := managed
	updated.DestPort = "8081"
	srv.Firewall.SetPortForwards(fakes(first, second))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, second, added),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + forwardConfig(managed),
				Check:  expectFakeForwards(srv, first, second, managed),
			},
			{
				PreConfig: func() {
					srv.Firewall.SetPortForwards(fakes(second, managed, added))
				},
				Config:            fakeRouterConfig(srv) + forwardConfig(updated),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectForwardState(updated),
				Check:             expectFakeForwards(srv, second, updated, added),
			},
		},
	})
}

func TestFirewallPortForwardResourceOverlap(t *testing.T) {
	tests := []struct {
		name     string
		proto    string
		existing glinet.PortForward
		error    string
	}{
		{"same identity", "tcp", forward("tcp", "50001", "9090", "existing"),
			`(?s)Firewall Port Forward Already Exists.*"existing".*Import it with the ID\s+"wan/tcp/50001"`},
		{"same identity tcp udp", "tcp udp", forward("tcp udp", "50001", "9090", ""),
			`(?s)Firewall Port Forward Already Exists.*"wan/tcp\+udp/50001"`},
		{"protocol intersects", "tcp", forward("tcp udp", "50001", "9090", "existing"),
			`(?s)Firewall Port Forward Overlaps.*"existing".*protocol\s+"tcp udp"`},
		{"range intersects", "tcp", forward("tcp", "50000-50010", "9090", ""),
			`(?s)Firewall Port Forward Overlaps.*external\s+port\s+50000-50010`},
		{"no protocol", "udp", forward("", "50001", "9090", "existing"),
			`Firewall Port Forward Overlaps`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetPortForwards(fakes(tt.existing))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + forwardConfig(forward(tt.proto, "50001", "8080", "")),
						ExpectError: regexp.MustCompile(tt.error),
					},
				},
			})

			if got := srv.Firewall.PortForwards(); !slices.Equal(got, fakes(tt.existing)) {
				t.Errorf("port forwards are %v, want %v", got, fakes(tt.existing))
			}
		})
	}
}

func TestFirewallPortForwardResourceNoOverlap(t *testing.T) {
	guest := forward("tcp", "50001", "9090", "guest")
	guest.Src = "guest"
	existing := []glinet.PortForward{
		forward("udp", "50001", "9090", "udp"),
		forward("tcp", "50002-50010", "9090", "range"),
		guest,
	}
	managed := forward("tcp", "50001", "8080", "")
	srv := glinettest.NewServer(t)
	srv.Firewall.SetPortForwards(fakes(existing...))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, existing...),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + forwardConfig(managed),
				Check:  expectFakeForwards(srv, append(slices.Clone(existing), managed)...),
			},
		},
	})
}

func TestFirewallPortForwardResourceDuplicate(t *testing.T) {
	srv := glinettest.NewServer(t)
	first := forward("tcp", "50001", "8080", "first")
	duplicate := forward("tcp", "50001", "8080", "duplicate")
	duplicate.DestIP = otherForwardIP
	srv.Firewall.SetPortForwards(fakes(first, duplicate))
	updated := first
	updated.DestPort = "8081"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv, duplicate),
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + forwardConfig(first),
				ResourceName:       forwardAddress,
				ImportState:        true,
				ImportStateId:      "wan/tcp/50001",
				ImportStatePersist: true,
			},
			{
				Config:            fakeRouterConfig(srv) + forwardConfig(updated),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectForwardState(updated),
				Check:             expectFakeForwards(srv, updated, duplicate),
			},
		},
	})
}

func TestFirewallPortForwardFindDuplicateWarning(t *testing.T) {
	srv := glinettest.NewServer(t)
	first := forward("tcp", "50001", "8080", "first")
	duplicate := forward("tcp", "50001", "9090", "duplicate")
	srv.Firewall.SetPortForwards(fakes(forward("udp", "50001", "8080", "udp"), first, duplicate))
	r := &firewallPortForwardResource{client: testClient(t, srv.URL, srv.Username, srv.Password)}

	var diags diag.Diagnostics
	got, err := r.find(context.Background(), identityOf(first), &diags)
	if err != nil {
		t.Fatal(err)
	}

	if got == nil || got.Name != "first" || got.ID != glinettest.PortForwardID(1) {
		t.Errorf("find returned %v, want the port forward %q with the ID %s", got, "first", glinettest.PortForwardID(1))
	}
	warnings := diags.Warnings()
	if diags.HasError() || len(warnings) != 1 {
		t.Fatalf("diagnostics are %v, want one warning", diags)
	}
	if detail := warnings[0].Detail(); !strings.Contains(detail, `"first"`) || !strings.Contains(detail, `"duplicate"`) {
		t.Errorf("warning is %q, want it to name %q and %q", detail, "first", "duplicate")
	}
}

// portForwardList returns the get_port_forward_list result of the fake router for forwards.
func portForwardList(forwards []glinettest.PortForward) any {
	res := make([]map[string]any, len(forwards))
	for i, f := range forwards {
		res[i] = map[string]any{
			"id": glinettest.PortForwardID(i), "name": f.Name, "proto": f.Proto, "src": f.Src, "src_dport": f.SrcDport,
			"dest": f.Dest, "dest_ip": f.DestIP, "dest_port": f.DestPort, "enabled": f.Enabled,
		}
	}
	return map[string]any{"res": res}
}

func TestFirewallPortForwardResourceDeleteAlreadyGone(t *testing.T) {
	srv := glinettest.NewServer(t)
	removeAfterNextList := false
	srv.Handle("firewall", "get_port_forward_list", func(json.RawMessage) (any, error) {
		forwards := srv.Firewall.PortForwards()
		if removeAfterNextList {
			removeAfterNextList = false
			srv.Firewall.SetPortForwards(nil)
		}
		return portForwardList(forwards), nil
	})
	srv.Handle("firewall", "remove_port_forward", func(json.RawMessage) (any, error) {
		t.Error("remove_port_forward called")
		return json.RawMessage(`[]`), nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + forwardConfig(forward("tcp", "50001", "8080", "")),
			},
			{
				PreConfig: func() { removeAfterNextList = true },
				Config:    fakeRouterConfig(srv),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(forwardAddress, plancheck.ResourceActionDestroy),
					},
				},
			},
		},
	})
}

func TestFirewallPortForwardResourceInvalidConfig(t *testing.T) {
	srv := glinettest.NewServer(t)
	valid := forward("tcp", "50001", "8080", "")
	with := func(change func(*glinet.PortForward)) string {
		f := valid
		change(&f)
		return forwardConfig(f)
	}

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"protocol upper case", with(func(f *glinet.PortForward) { f.Proto = "TCP" }), `value must be one of`},
		{"protocol tcpudp", with(func(f *glinet.PortForward) { f.Proto = "tcpudp" }), `value must be one of`},
		{"protocol icmp", with(func(f *glinet.PortForward) { f.Proto = "icmp" }), `value must be one of`},
		{"src empty", with(func(f *glinet.PortForward) { f.Src = "" }), `string length must be at least 1`},
		{"dest empty", with(func(f *glinet.PortForward) { f.Dest = "" }), `string length must be at least 1`},
		{"src_dport 0", with(func(f *glinet.PortForward) { f.SrcDport = "0" }), `src_dport must be a port from 1 to 65535`},
		{"src_dport 65536", with(func(f *glinet.PortForward) { f.SrcDport = "65536" }), `src_dport must be a port`},
		{"src_dport reversed range", with(func(f *glinet.PortForward) { f.SrcDport = "50010-50001" }), `src_dport must be a port`},
		{"src_dport leading zero", with(func(f *glinet.PortForward) { f.SrcDport = "08080" }), `src_dport must be a port`},
		{"src_dport name", with(func(f *glinet.PortForward) { f.SrcDport = "http" }), `src_dport must be a port`},
		{"src_dport space", with(func(f *glinet.PortForward) { f.SrcDport = "50001 " }), `src_dport must be a port`},
		{"dest_port range end too large", with(func(f *glinet.PortForward) { f.DestPort = "8080-70000" }), `dest_port must be a port`},
		{"dest_port empty", with(func(f *glinet.PortForward) { f.DestPort = "" }), `dest_port must be a port`},
		{"dest_ip out of range", with(func(f *glinet.PortForward) { f.DestIP = "192.168.8.256" }), `dest_ip must be an IPv4 address`},
		{"dest_ip IPv6", with(func(f *glinet.PortForward) { f.DestIP = "fd00::1" }), `dest_ip must be an IPv4 address`},
		{"dest_ip host name", with(func(f *glinet.PortForward) { f.DestIP = "printer" }), `dest_ip must be an IPv4 address`},
		{"name empty", strings.Replace(forwardConfig(valid), "}", "  name = \"\"\n}", 1), `string length must be at least 1`},
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

	if got := srv.Firewall.PortForwards(); len(got) != 0 {
		t.Errorf("port forwards are %v, want none", got)
	}
}

func TestFirewallPortForwardResourceReadErrorAfterCreate(t *testing.T) {
	srv := glinettest.NewServer(t)
	failed := false
	srv.Handle("firewall", "get_port_forward_list", func(json.RawMessage) (any, error) {
		if len(srv.Firewall.PortForwards()) > 0 && !failed {
			failed = true
			return nil, glinettest.ErrMethodNotFound
		}
		return portForwardList(srv.Firewall.PortForwards()), nil
	})
	want := forward("tcp", "50001", "8080", "")
	config := fakeRouterConfig(srv) + forwardConfig(want)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeForwards(srv),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Port Forward.*Method not found`),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: expectForwardState(want),
				Check:             expectFakeForwards(srv, want),
			},
		},
	})
}

func failMethod(srv *glinettest.Server, method string) func() {
	return func() {
		srv.Handle("firewall", method, func(json.RawMessage) (any, error) {
			return nil, glinettest.ErrMethodNotFound
		})
	}
}

func TestFirewallPortForwardResourceRouterErrors(t *testing.T) {
	want := forward("tcp", "50001", "8080", "")
	changed := want
	changed.DestPort = "8081"

	t.Run("list", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "get_port_forward_list")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + forwardConfig(want),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Port Forwards.*Method not found`),
				},
			},
		})
	})

	t.Run("add", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "add_port_forward")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + forwardConfig(want),
					ExpectError: regexp.MustCompile(`(?s)Unable to Create Firewall Port Forward.*Method not found`),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			CheckDestroy:             expectFakeForwards(srv),
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + forwardConfig(want),
				},
				{
					PreConfig:   failMethod(srv, "set_port_forward"),
					Config:      fakeRouterConfig(srv) + forwardConfig(changed),
					ExpectError: regexp.MustCompile(`(?s)Unable to Update Firewall Port Forward.*Method not found`),
				},
			},
		})
	})

	t.Run("remove", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + forwardConfig(want),
				},
				{
					PreConfig:   failMethod(srv, "remove_port_forward"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Delete Firewall Port Forward.*Method not found`),
				},
				{
					PreConfig: func() {
						if got := srv.Firewall.PortForwards(); !slices.Equal(got, fakes(want)) {
							t.Errorf("port forwards after the failed delete are %v, want %v", got, fakes(want))
						}
						srv.Firewall.SetPortForwards(nil)
					},
					Config: fakeRouterConfig(srv),
				},
			},
		})
	})
}
