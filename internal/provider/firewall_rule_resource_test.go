package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const ruleAddress = "glinet_firewall_rule.test"

// testRule returns an enabled tcp rule from the zone wan to forwardIP in the zone lan.
func testRule(destPort, target, name string) glinet.FirewallRule {
	return glinet.FirewallRule{
		Name:     name,
		Src:      "wan",
		Proto:    "tcp",
		Dest:     "lan",
		DestIP:   forwardIP,
		DestPort: destPort,
		Target:   target,
		Enabled:  true,
	}
}

// ruleResource returns the configuration of r. It leaves out the fields that are empty, and enabled when it is true.
func ruleResource(label string, r glinet.FirewallRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nresource \"glinet_firewall_rule\" %q {\n", label)
	for _, a := range []struct{ name, value string }{
		{"source_zone", r.Src},
		{"source_ip", r.SrcIP},
		{"source_mac", r.SrcMAC},
		{"source_port", r.SrcPort},
		{"protocol", r.Proto},
		{"destination_zone", r.Dest},
		{"destination_ip", r.DestIP},
		{"destination_port", r.DestPort},
		{"action", r.Target},
		{"name", r.Name},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "  %s = %q\n", a.name, a.value)
		}
	}
	if !r.Enabled {
		b.WriteString("  enabled = false\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func ruleConfig(r glinet.FirewallRule) string {
	return ruleResource("test", r)
}

func fakeRule(r glinet.FirewallRule) glinettest.FirewallRule {
	return glinettest.FirewallRule{
		Name:     r.Name,
		Src:      r.Src,
		SrcIP:    r.SrcIP,
		SrcMAC:   r.SrcMAC,
		SrcPort:  r.SrcPort,
		Proto:    r.Proto,
		Dest:     r.Dest,
		DestIP:   r.DestIP,
		DestPort: r.DestPort,
		Target:   r.Target,
		Enabled:  r.Enabled,
	}
}

func fakeRules(rules ...glinet.FirewallRule) []glinettest.FirewallRule {
	res := []glinettest.FirewallRule{}
	for _, r := range rules {
		res = append(res, fakeRule(r))
	}
	return res
}

func expectRule(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(ruleAddress, tfjsonpath.New(attribute), value)
}

func expectRuleState(r glinet.FirewallRule) []statecheck.StateCheck {
	value := func(s string) knownvalue.Check {
		if s == "" {
			return knownvalue.Null()
		}
		return knownvalue.StringExact(s)
	}

	return []statecheck.StateCheck{
		expectRule("source_zone", value(r.Src)),
		expectRule("source_ip", value(r.SrcIP)),
		expectRule("source_mac", value(r.SrcMAC)),
		expectRule("source_port", value(r.SrcPort)),
		expectRule("protocol", value(r.Proto)),
		expectRule("destination_zone", value(r.Dest)),
		expectRule("destination_ip", value(r.DestIP)),
		expectRule("destination_port", value(r.DestPort)),
		expectRule("action", value(r.Target)),
		expectRule("name", value(r.Name)),
		expectRule("enabled", knownvalue.Bool(r.Enabled)),
	}
}

func expectRuleAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(ruleAddress, action),
		},
	}
}

// expectFakeRules checks that the rules of srv are want, in this order.
func expectFakeRules(srv *glinettest.Server, want ...glinet.FirewallRule) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(want...)) {
			return fmt.Errorf("router rules are %v, want %v", got, fakeRules(want...))
		}
		return nil
	}
}

// ruleTestRouter returns the provider block and a client for the router of the test.
// On the real router, the test fails when one of the identities ids has no destination port, or a rule has one of the
// identities ids or a name that starts with tf-test-rule.
// When the test ends, it removes the rules with the identities ids one at a time and checks that the rules are as before.
func ruleTestRouter(t *testing.T, ids ...ruleIdentity) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	for _, id := range ids {
		if id.destPort == "" {
			t.Fatalf("the rule with %s has no destination port, which firmware 4.8.3 cannot remove", id)
		}
	}
	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.Firewall.ListRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range initial {
		if slices.ContainsFunc(ids, func(id ruleIdentity) bool { return id.matches(r) }) || strings.HasPrefix(r.Name, "tf-test-rule") {
			t.Fatalf("the router has the rule %s, which the test uses", describeRule(r))
		}
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range ids {
			for {
				rules, err := client.Firewall.ListRules(ctx)
				if err != nil {
					t.Fatalf("read rules: %v", err)
				}
				i := slices.IndexFunc(rules, id.matches)
				if i < 0 {
					break
				}
				if err := client.Firewall.RemoveRule(ctx, rules[i].ID); err != nil {
					t.Fatalf("remove rule %s: %v", describeRule(rules[i]), err)
				}
			}
		}

		rules, err := client.Firewall.ListRules(ctx)
		if err != nil {
			t.Fatalf("read rules: %v", err)
		}
		if !slices.Equal(rules, initial) {
			t.Errorf("rules are %v, want %v", rules, initial)
		}
	})

	return `provider "glinet" {}`, client
}

// expectRouterRule checks that the first rule on the router with the identity id is want, without its ID.
// A nil want checks that the router has no rule with the identity id.
func expectRouterRule(client *glinet.Client, id ruleIdentity, want *glinet.FirewallRule) resource.TestCheckFunc {
	return func(*terraform.State) error {
		rules, err := client.Firewall.ListRules(context.Background())
		if err != nil {
			return err
		}

		i := slices.IndexFunc(rules, id.matches)
		switch {
		case want == nil && i >= 0:
			return fmt.Errorf("router has rule %v, want none", rules[i])
		case want != nil && i < 0:
			return fmt.Errorf("router has no rule with %s", id)
		case want != nil:
			got := rules[i]
			got.ID = ""
			if got != *want {
				return fmt.Errorf("router rule is %v, want %v", got, *want)
			}
		}
		return nil
	}
}

// TestFirewallRuleResource runs on the real router when TF_ACC and GLINET_HOST are set. The rule accepts only
// tcp traffic from the zone wan to port 50001 of the unused address forwardIP, so it cannot cut access to the router.
func TestFirewallRuleResource(t *testing.T) {
	parallel(t)
	created := testRule("50001", "ACCEPT", "tf-test-rule")
	updated := testRule("50001", "ACCEPT", "tf-test-rule-renamed")
	updated.Enabled = false
	id := identityOfRule(created)
	provider, client := ruleTestRouter(t, id)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterRule(client, id, nil),
		Steps: []resource.TestStep{
			{
				Config:            provider + ruleConfig(created),
				ConfigStateChecks: expectRuleState(created),
				Check:             expectRouterRule(client, id, &created),
			},
			{
				Config:            provider + ruleConfig(updated),
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(updated),
				Check:             expectRouterRule(client, id, &updated),
			},
			{
				ResourceName:                         ruleAddress,
				ImportState:                          true,
				ImportStateId:                        updated.Name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func TestFirewallRuleResourceCreate(t *testing.T) {
	parallel(t)
	minimal := glinet.FirewallRule{Src: "wan", Target: "DROP", Enabled: true}
	full := glinet.FirewallRule{
		Name:     "full",
		Src:      "wan",
		SrcIP:    "203.0.113.0/24",
		SrcMAC:   "02:00:00:aa:bb:01",
		SrcPort:  "1024-65535",
		Proto:    "udp",
		Dest:     "lan",
		DestIP:   forwardIP,
		DestPort: "50004-50005",
		Target:   "REJECT",
	}

	for name, r := range map[string]glinet.FirewallRule{"minimal": minimal, "full": full} {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeRules(srv),
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + ruleConfig(r),
						ConfigStateChecks: expectRuleState(r),
						Check:             expectFakeRules(srv, r),
					},
					{
						Config: fakeRouterConfig(srv) + ruleConfig(r),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
					},
				},
			})
		})
	}
}

// TestFirewallRuleResourceValues creates rules with values that were tested on a router with firmware 4.8.3.
// The router keeps each value as written.
func TestFirewallRuleResourceValues(t *testing.T) {
	parallel(t)
	with := func(change func(*glinet.FirewallRule)) glinet.FirewallRule {
		r := testRule("50001", "DROP", "values")
		change(&r)
		return r
	}

	tests := map[string]glinet.FirewallRule{
		"IPv6 source address":      with(func(r *glinet.FirewallRule) { r.SrcIP = "2001:db8::10" }),
		"IPv6 source prefix":       with(func(r *glinet.FirewallRule) { r.SrcIP = "2001:db8:1::/48" }),
		"IPv4 source prefix":       with(func(r *glinet.FirewallRule) { r.SrcIP = "198.51.100.0/24" }),
		"IPv6 destination address": with(func(r *glinet.FirewallRule) { r.DestIP = "2001:db8::20" }),
		"protocol esp":             with(func(r *glinet.FirewallRule) { r.Proto = "esp" }),
		"protocol icmpv6":          with(func(r *glinet.FirewallRule) { r.Proto = "icmpv6" }),
		"protocol number":          with(func(r *glinet.FirewallRule) { r.Proto = "50" }),
		"protocol ipv6-icmp":       with(func(r *glinet.FirewallRule) { r.Proto = "ipv6-icmp" }),
		"protocol ax.25":           with(func(r *glinet.FirewallRule) { r.Proto = "ax.25" }),
		"protocol tp++":            with(func(r *glinet.FirewallRule) { r.Proto = "tp++" }),
		"protocol iso-tp4":         with(func(r *glinet.FirewallRule) { r.Proto = "iso-tp4" }),
		"source MAC upper case":    with(func(r *glinet.FirewallRule) { r.SrcMAC = "02:00:5E:00:53:A2" }),
	}

	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeRules(srv),
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + ruleConfig(r),
						ConfigStateChecks: expectRuleState(r),
						Check:             expectFakeRules(srv, r),
					},
					{
						Config:           fakeRouterConfig(srv) + ruleConfig(r),
						ConfigPlanChecks: expectEmptyPlan,
					},
				},
			})
		})
	}
}

// TestFirewallRuleResourceMACLetterCase finds the rule without letter case of the source MAC address.
// The state keeps the configured letter case, and a change of only the letter case updates the rule in place.
func TestFirewallRuleResourceMACLetterCase(t *testing.T) {
	parallel(t)
	upper := testRule("50001", "DROP", "mac")
	upper.SrcMAC = "02:00:5E:00:53:A2"
	lower := upper
	lower.SrcMAC = "02:00:5e:00:53:a2"
	mixed := upper
	mixed.SrcMAC = "02:00:5e:00:53:A2"

	t.Run("router letter case", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			CheckDestroy:             expectFakeRules(srv),
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + ruleConfig(upper),
					Check:  expectFakeRules(srv, upper),
				},
				{
					PreConfig:         func() { srv.Firewall.SetRules(fakeRules(lower)) },
					Config:            fakeRouterConfig(srv) + ruleConfig(upper),
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: expectRuleState(upper),
				},
				{
					Config:            fakeRouterConfig(srv) + ruleConfig(mixed),
					ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
					ConfigStateChecks: expectRuleState(mixed),
					Check:             expectFakeRules(srv, mixed),
				},
			},
		})
	})

	t.Run("exists", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Firewall.SetRules(fakeRules(lower))

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ruleConfig(upper),
					ExpectError: regexp.MustCompile(`Firewall Rule Already Exists`),
				},
			},
		})

		if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(lower)) {
			t.Errorf("rules are %v, want %v", got, fakeRules(lower))
		}
	})
}

func TestFirewallRuleResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	noName := testRule("50001", "ACCEPT", "")
	named := testRule("50001", "ACCEPT", "first")
	renamedDrop := testRule("50001", "DROP", "second")
	rejectNoName := testRule("50001", "REJECT", "")
	disabled := rejectNoName
	disabled.Enabled = false

	var steps []resource.TestStep
	for i, r := range []glinet.FirewallRule{noName, named, renamedDrop, rejectNoName, disabled, rejectNoName} {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + ruleConfig(r),
			ConfigStateChecks: expectRuleState(r),
			Check:             expectFakeRules(srv, r),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectRuleAction(plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv),
		Steps:                    steps,
	})
}

// TestFirewallRuleResourceReplace changes each match attribute. The replaced rule goes to the end of the list.
func TestFirewallRuleResourceReplace(t *testing.T) {
	parallel(t)
	initial := testRule("50001", "ACCEPT", "test")
	with := func(change func(*glinet.FirewallRule)) glinet.FirewallRule {
		r := initial
		change(&r)
		return r
	}
	before := testRule("50101", "ACCEPT", "before")
	after := testRule("50102", "ACCEPT", "after")

	tests := []struct {
		name    string
		changed glinet.FirewallRule
	}{
		{"source_zone", with(func(r *glinet.FirewallRule) { r.Src = "guest" })},
		{"source_ip", with(func(r *glinet.FirewallRule) { r.SrcIP = "203.0.113.5" })},
		{"source_mac", with(func(r *glinet.FirewallRule) { r.SrcMAC = "02:00:00:aa:bb:01" })},
		{"source_port", with(func(r *glinet.FirewallRule) { r.SrcPort = "1024" })},
		{"protocol", with(func(r *glinet.FirewallRule) { r.Proto = "tcp udp" })},
		{"destination_zone", with(func(r *glinet.FirewallRule) { r.Dest = "guest" })},
		{"destination_ip", with(func(r *glinet.FirewallRule) { r.DestIP = "" })},
		{"destination_port", with(func(r *glinet.FirewallRule) { r.DestPort = "50002-50003" })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetRules(fakeRules(before))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeRules(srv, before, after),
				Steps: []resource.TestStep{
					{
						Config: fakeRouterConfig(srv) + ruleConfig(initial),
						Check:  expectFakeRules(srv, before, initial),
					},
					{
						PreConfig:         func() { srv.Firewall.SetRules(fakeRules(before, initial, after)) },
						Config:            fakeRouterConfig(srv) + ruleConfig(tt.changed),
						ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionDestroyBeforeCreate),
						ConfigStateChecks: expectRuleState(tt.changed),
						Check:             expectFakeRules(srv, before, after, tt.changed),
					},
				},
			})
		})
	}
}

// TestFirewallRuleResourceReplaceTriggeredBy shows the pattern of the documentation that keeps the order of two rules
// when the earlier rule is replaced.
func TestFirewallRuleResourceReplaceTriggeredBy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	first := testRule("50001", "ACCEPT", "first")
	first.SrcIP = "203.0.113.0/24"
	changed := first
	changed.SrcIP = "198.51.100.0/24"
	second := testRule("50001", "DROP", "second")
	config := func(first glinet.FirewallRule) string {
		return fakeRouterConfig(srv) + ruleResource("first", first) +
			strings.TrimSuffix(ruleResource("second", second), "}\n") +
			"  lifecycle {\n    replace_triggered_by = [glinet_firewall_rule.first]\n  }\n}\n"
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv),
		Steps: []resource.TestStep{
			{
				Config: config(first),
				Check:  expectFakeRules(srv, first, second),
			},
			{
				Config: config(changed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("glinet_firewall_rule.first", plancheck.ResourceActionDestroyBeforeCreate),
						plancheck.ExpectResourceAction("glinet_firewall_rule.second", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: expectFakeRules(srv, changed, second),
			},
		},
	})
}

func TestFirewallRuleResourceImport(t *testing.T) {
	parallel(t)
	imported := glinet.FirewallRule{
		Name:    "imported",
		Src:     "wan",
		SrcIP:   "203.0.113.0/24",
		SrcPort: "1024-65535",
		Proto:   "udp",
		Dest:    "lan",
		DestIP:  forwardIP,
		Target:  "DROP",
	}
	other := testRule("50001", "ACCEPT", "other")
	srv := glinettest.NewServer(t)
	srv.Firewall.SetRules(fakeRules(other, imported))
	config := fakeRouterConfig(srv) + ruleConfig(imported)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, other),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       ruleAddress,
				ImportState:        true,
				ImportStateId:      "imported",
				ImportStatePersist: true,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: expectRuleState(imported),
			},
		},
	})
}

func TestFirewallRuleResourceImportErrors(t *testing.T) {
	parallel(t)
	first := testRule("50001", "ACCEPT", "first")
	duplicate := testRule("50001", "DROP", "duplicate")
	same := testRule("50002", "ACCEPT", "same")

	tests := []struct {
		name     string
		importID string
		error    string
	}{
		{"no such name", "missing", `(?s)Firewall Rule Not Found.*no rule with the name\s+"missing"`},
		{"name is case sensitive", "FIRST", `Firewall Rule Not Found`},
		{"ambiguous name", "same", `(?s)Ambiguous Firewall Rule Name.*2 rules with the name\s+"same"`},
		{"earlier duplicate", "duplicate", `(?s)Firewall Rule Has an Earlier Duplicate.*"duplicate".*"first"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetRules(fakeRules(first, duplicate, same, same))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        fakeRouterConfig(srv) + ruleConfig(first),
						ResourceName:  ruleAddress,
						ImportState:   true,
						ImportStateId: tt.importID,
						ExpectError:   regexp.MustCompile(tt.error),
					},
				},
			})
		})
	}
}

// TestFirewallRuleImportEmptyID calls ImportState directly, because the test framework replaces an empty import ID.
func TestFirewallRuleImportEmptyID(t *testing.T) {
	parallel(t)
	ctx := context.Background()
	r := &firewallRuleResource{}
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	resp := fwresource.ImportStateResponse{
		State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)},
	}

	r.ImportState(ctx, fwresource.ImportStateRequest{ID: ""}, &resp)

	errs := resp.Diagnostics.Errors()
	if len(errs) != 1 || errs[0].Summary() != "Invalid Import ID" || !strings.Contains(errs[0].Detail(), "A rule with no name cannot be imported") {
		t.Errorf("diagnostics are %v, want one Invalid Import ID error", resp.Diagnostics)
	}
}

func TestFirewallRuleResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	want := testRule("50001", "ACCEPT", "test")
	config := fakeRouterConfig(srv) + ruleConfig(want)
	changed := want
	changed.Target = "DROP"
	changed.Enabled = false
	renamed := want
	renamed.Name = "renamed"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv),
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { srv.Firewall.SetRules(fakeRules(changed)) },
				Config:            config,
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(want),
				Check:             expectFakeRules(srv, want),
			},
			{
				PreConfig:         func() { srv.Firewall.SetRules(fakeRules(renamed)) },
				Config:            config,
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(want),
				Check:             expectFakeRules(srv, want),
			},
			{
				PreConfig:         func() { srv.Firewall.SetRules(nil) },
				Config:            config,
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectRuleState(want),
				Check:             expectFakeRules(srv, want),
			},
		},
	})
}

// TestFirewallRuleResourceIDShift removes a rule and a port forward before the managed rule on the router and adds
// rules after it, so the router ID of the managed rule when it was created is now the ID of an added rule.
func TestFirewallRuleResourceIDShift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	earlierRule := testRule("50101", "ACCEPT", "earlier")
	earlierForward := forward("tcp", "50201", "8080", "earlier")
	keptForward := forward("tcp", "50202", "8080", "kept")
	added := []glinet.FirewallRule{testRule("50102", "ACCEPT", "added-1"), testRule("50103", "DROP", "added-2")}
	managed := testRule("50001", "ACCEPT", "managed")
	updated := managed
	updated.Target = "REJECT"
	srv.Firewall.SetRules(fakeRules(earlierRule))
	srv.Firewall.SetPortForwards(fakes(earlierForward, keptForward))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			expectFakeRules(srv, added...),
			expectFakeForwards(srv, keptForward),
		),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(managed),
				Check: func(*terraform.State) error {
					rules, err := client.Firewall.ListRules(context.Background())
					if err != nil {
						return err
					}
					if len(rules) != 2 || rules[1].ID != glinettest.RuleID(3) {
						return fmt.Errorf("rules are %v, want the managed rule with the ID %s", rules, glinettest.RuleID(3))
					}
					return nil
				},
			},
			{
				PreConfig: func() {
					ctx := context.Background()
					if err := client.Firewall.RemoveRule(ctx, glinettest.RuleID(0)); err != nil {
						t.Fatal(err)
					}
					if err := client.Firewall.RemovePortForward(ctx, glinettest.PortForwardID(0)); err != nil {
						t.Fatal(err)
					}
					for _, r := range added {
						if _, err := client.Firewall.AddRule(ctx, r); err != nil {
							t.Fatal(err)
						}
					}
					rules, err := client.Firewall.ListRules(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if rules[2].ID != glinettest.RuleID(3) || rules[2].Name != "added-2" {
						t.Fatalf("rules are %v, want %q with the ID %s", rules, "added-2", glinettest.RuleID(3))
					}
				},
				Config:            fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(updated),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectFakeRules(srv, append([]glinet.FirewallRule{updated}, added...)...),
					expectFakeForwards(srv, keptForward),
				),
			},
		},
	})
}

func TestFirewallRuleResourceExists(t *testing.T) {
	parallel(t)
	tests := []struct {
		name     string
		existing glinet.FirewallRule
		error    string
	}{
		{"named", testRule("50001", "DROP", "existing"),
			`(?s)Firewall Rule Already Exists.*"existing".*Import it with the ID\s+"existing"`},
		{"no name", testRule("50001", "DROP", ""),
			`(?s)Firewall Rule Already Exists.*It has no\s+name, so it cannot be\s+imported`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetRules(fakeRules(tt.existing))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + ruleConfig(testRule("50001", "ACCEPT", "new")),
						ExpectError: regexp.MustCompile(tt.error),
					},
				},
			})

			if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(tt.existing)) {
				t.Errorf("rules are %v, want %v", got, fakeRules(tt.existing))
			}
		})
	}
}

// TestFirewallRuleResourceOverlapNotChecked shows that create checks only the exact identity: rules that match
// some of the same traffic do not stop the create.
func TestFirewallRuleResourceOverlapNotChecked(t *testing.T) {
	parallel(t)
	noDestIP := testRule("50001", "DROP", "no destination IP")
	noDestIP.DestIP = ""
	existing := []glinet.FirewallRule{
		testRule("50000-50010", "DROP", "range"),
		noDestIP,
	}
	managed := testRule("50001", "ACCEPT", "")
	srv := glinettest.NewServer(t)
	srv.Firewall.SetRules(fakeRules(existing...))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, existing...),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(managed),
				Check:  expectFakeRules(srv, append(slices.Clone(existing), managed)...),
			},
		},
	})
}

func TestFirewallRuleResourceDuplicate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	first := testRule("50001", "ACCEPT", "first")
	duplicate := testRule("50001", "DROP", "duplicate")
	srv.Firewall.SetRules(fakeRules(first, duplicate))
	updated := first
	updated.Target = "REJECT"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, duplicate),
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + ruleConfig(first),
				ResourceName:       ruleAddress,
				ImportState:        true,
				ImportStateId:      "first",
				ImportStatePersist: true,
			},
			{
				Config:            fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(updated),
				Check:             expectFakeRules(srv, updated, duplicate),
			},
		},
	})
}

func TestFirewallRuleFindDuplicates(t *testing.T) {
	parallel(t)
	first := testRule("50001", "ACCEPT", "first")
	duplicate := testRule("50001", "DROP", "duplicate")
	other := testRule("50002", "ACCEPT", "other")

	tests := []struct {
		name   string
		rules  []glinet.FirewallRule
		want   glinet.FirewallRule
		wantID string
		err    string
	}{
		{"one match with other values", []glinet.FirewallRule{other, first}, duplicate, glinettest.RuleID(1), ""},
		{"the only equal match", []glinet.FirewallRule{other, first, duplicate}, duplicate, glinettest.RuleID(2), ""},
		{"no equal match", []glinet.FirewallRule{other, first, duplicate}, testRule("50001", "REJECT", "first"), "",
			`the router has 2 rules with source zone wan, .*, at these positions in the rule list: ` +
				`position 2: name "first", action ACCEPT, enabled true; position 3: name "duplicate", action DROP, enabled true. ` +
				`0 of them have the name "first", the action REJECT and enabled true of the state`},
		{"more than one equal match", []glinet.FirewallRule{first, other, first}, first, "",
			`position 1: name "first", action ACCEPT, enabled true; position 3: name "first", action ACCEPT, enabled true. ` +
				`2 of them have the name "first"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetRules(fakeRules(tt.rules...))
			r := &firewallRuleResource{client: testClient(t, srv.URL, srv.Username, srv.Password)}

			got, err := r.find(context.Background(), identityOfRule(first), tt.want)
			if tt.err != "" {
				if err == nil || !regexp.MustCompile(tt.err).MatchString(err.Error()) {
					t.Fatalf("find returned %v and the error %v, want an error that matches %q", got, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || got.ID != tt.wantID {
				t.Errorf("find returned %v, want the rule with the ID %s", got, tt.wantID)
			}
		})
	}
}

// TestFirewallRuleResourceEarlierDuplicate adds a rule with the same match fields before the rule of the resource.
// Refresh, update and destroy keep the rule of the state. When two rules are equal to the state, they fail.
func TestFirewallRuleResourceEarlierDuplicate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	removes := recordCalls(t, srv, "firewall", "remove_rule")
	sets := recordCalls(t, srv, "firewall", "set_rule")
	managed := testRule("50001", "DROP", "managed")
	earlier := testRule("50001", "ACCEPT", "earlier")
	updated := managed
	updated.Target = "REJECT"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, earlier),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(managed),
			},
			{
				PreConfig:         func() { srv.Firewall.SetRules(fakeRules(earlier, managed)) },
				Config:            fakeRouterConfig(srv) + ruleConfig(managed),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: expectRuleState(managed),
			},
			{
				Config:            fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(updated),
				Check: resource.ComposeTestCheckFunc(
					expectFakeRules(srv, earlier, updated),
					sets.expect(`{"id":"`+glinettest.RuleID(1)+`","name":"managed","src":"wan","src_ip":"","src_mac":"","src_port":"","proto":"tcp","dest":"lan","dest_ip":"`+forwardIP+`","dest_port":"50001","target":"REJECT","enabled":true}`),
				),
			},
			{
				PreConfig:   func() { srv.Firewall.SetRules(fakeRules(earlier, updated, updated)) },
				Config:      fakeRouterConfig(srv) + ruleConfig(updated),
				ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Rule.*` + wrapped(`2 of them have the name "managed", the action REJECT and enabled true of the state`)),
			},
			{
				Config:      fakeRouterConfig(srv) + ruleConfig(updated),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Unable to Read Firewall Rule`),
			},
			{
				PreConfig: func() {
					if got := append(removes.take(), sets.take()...); len(got) != 0 {
						t.Errorf("the router got the writes %q", got)
					}
					srv.Firewall.SetRules(fakeRules(earlier, updated))
				},
				Config:           fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// TestFirewallRuleResourceUpdateReadBackError updates a rule that has a duplicate, and the read after the write fails.
// The state gets the written values, so the next apply still selects the rule.
func TestFirewallRuleResourceUpdateReadBackError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	managed := testRule("50001", "DROP", "managed")
	earlier := testRule("50001", "ACCEPT", "earlier")
	updated := managed
	updated.Target = "REJECT"
	updated.Name = "renamed"
	updated.Enabled = false

	var failNextList atomic.Bool
	list := srv.Handler("firewall", "get_rule_list")
	srv.Handle("firewall", "get_rule_list", func(params json.RawMessage) (any, error) {
		if failNextList.CompareAndSwap(true, false) {
			return nil, &glinettest.Error{Code: -32603, Message: "Internal error"}
		}
		return list(params)
	})
	set := srv.Handler("firewall", "set_rule")
	srv.Handle("firewall", "set_rule", func(params json.RawMessage) (any, error) {
		res, err := set(params)
		failNextList.Store(err == nil)
		return res, err
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, earlier),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(managed),
			},
			{
				PreConfig:   func() { srv.Firewall.SetRules(fakeRules(earlier, managed)) },
				Config:      fakeRouterConfig(srv) + ruleConfig(updated),
				ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Rule.*Internal error`),
			},
			{
				Config:            fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: expectRuleState(updated),
				Check:             expectFakeRules(srv, earlier, updated),
			},
		},
	})
}

// TestFirewallRuleResourceNoDestinationPort creates, updates and destroys a rule with no destination port,
// which the router can remove only after a destination port is written to it.
func TestFirewallRuleResourceNoDestinationPort(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := testRule("50101", "ACCEPT", "before")
	after := testRule("50102", "ACCEPT", "after")
	srv.Firewall.SetRules(fakeRules(before))
	srv.Firewall.SetPortForwards(fakes(forward("tcp", "50201", "8080", "forward")))
	created := glinet.FirewallRule{Name: "no port", Src: "wan", Dest: "lan", DestIP: forwardIP, Target: "DROP", Enabled: true}
	updated := created
	updated.Target = "REJECT"
	updated.Name = ""

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			expectFakeRules(srv, before, after),
			expectFakeForwards(srv, forward("tcp", "50201", "8080", "forward")),
		),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + ruleConfig(created),
				ConfigStateChecks: expectRuleState(created),
				Check:             expectFakeRules(srv, before, created),
			},
			{
				PreConfig:         func() { srv.Firewall.SetRules(fakeRules(before, created, after)) },
				Config:            fakeRouterConfig(srv) + ruleConfig(updated),
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRuleState(updated),
				Check:             expectFakeRules(srv, before, updated, after),
			},
		},
	})
}

// TestFirewallRuleResourceNoDestinationPortFirmware483 makes the router fail like firmware 4.8.3: set_rule and
// remove_rule of a rule with no destination port return HTTP status 500. Update and destroy then fail with the
// explanation and the uci command for the router ID of the rule. When the rule is removed outside Terraform, destroy completes.
func TestFirewallRuleResourceNoDestinationPortFirmware483(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := testRule("50101", "ACCEPT", "before")
	srv.Firewall.SetRules(fakeRules(before))
	created := glinet.FirewallRule{Name: "no port", Src: "wan", Proto: "esp", Dest: "lan", DestIP: forwardIP, Target: "DROP", Enabled: true}
	changed := created
	changed.Target = "REJECT"

	var faulty atomic.Bool
	for _, method := range []string{"set_rule", "remove_rule"} {
		handler := srv.Handler("firewall", method)
		srv.Handle("firewall", method, func(params json.RawMessage) (any, error) {
			if faulty.Load() {
				return nil, glinettest.ErrInternalServerError
			}
			return handler(params)
		})
	}
	expectUnchanged := func() {
		if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(before, created)) {
			t.Errorf("rules after the failed write are %v, want %v", got, fakeRules(before, created))
		}
	}
	fault := `no\s+destination\s+port.*Firmware\s+4\.8\.3\s+cannot\s+change\s+or\s+remove.*HTTP\s+status\s+500.*` +
		`uci\s+delete\s+firewall\.` + glinettest.RuleID(1) + `\s+&&\s+uci\s+commit\s+firewall.*HTTP\s+status\s+500\s+Internal\s+Server\s+Error`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv, before),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(created),
				Check:  expectFakeRules(srv, before, created),
			},
			{
				PreConfig:   func() { faulty.Store(true) },
				Config:      fakeRouterConfig(srv) + ruleConfig(changed),
				ExpectError: regexp.MustCompile(`(?s)Unable to Update Firewall Rule.*` + fault),
			},
			{
				PreConfig:   expectUnchanged,
				Config:      fakeRouterConfig(srv),
				ExpectError: regexp.MustCompile(`(?s)Unable to Delete Firewall Rule.*writes\s+the\s+destination\s+port\s+65535.*` + fault),
			},
			{
				PreConfig: func() {
					expectUnchanged()
					srv.Firewall.SetRules(fakeRules(before))
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

// TestFirewallRuleResourceNoDestinationPortOtherError makes set_rule of a rule with no destination port return
// a JSON-RPC error one time. The errors of update and destroy do not tell how to remove the rule with uci, because
// that is only for the HTTP status 500 of firmware 4.8.3.
func TestFirewallRuleResourceNoDestinationPortOtherError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	created := glinet.FirewallRule{Name: "no port", Src: "wan", Dest: "lan", DestIP: forwardIP, Target: "DROP", Enabled: true}
	changed := created
	changed.Target = "REJECT"

	var fail atomic.Bool
	set := srv.Handler("firewall", "set_rule")
	srv.Handle("firewall", "set_rule", func(params json.RawMessage) (any, error) {
		if fail.Swap(false) {
			return nil, &glinettest.Error{Code: -32602, Message: "Invalid params"}
		}
		return set(params)
	})
	var errs []error

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv),
		ErrorCheck: func(err error) error {
			errs = append(errs, err)
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(created),
			},
			{
				PreConfig: func() { fail.Store(true) },
				Config:    fakeRouterConfig(srv) + ruleConfig(changed),
			},
			{
				PreConfig: func() { fail.Store(true) },
				Config:    fakeRouterConfig(srv),
			},
			{
				Config: fakeRouterConfig(srv),
			},
		},
	})

	if len(errs) != 2 {
		t.Fatalf("errors are %v, want the update error and the destroy error", errs)
	}
	for i, summary := range []string{"Unable to Update Firewall Rule", "Unable to Delete Firewall Rule"} {
		if msg := errs[i].Error(); !strings.Contains(msg, summary) || !strings.Contains(msg, "Invalid params") || strings.Contains(msg, "uci") {
			t.Errorf("error %d is %q, want %q with the router error and without uci", i, msg, summary)
		}
	}
}

func TestFirewallRuleModifyPlanNoDestinationPortWarning(t *testing.T) {
	parallel(t)
	ctx := context.Background()
	r := &firewallRuleResource{}
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	null := tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)
	withPort := testRule("50001", "DROP", "")
	noPort := testRule("", "DROP", "")
	changed := noPort
	changed.Target = "REJECT"

	plan := func(rule *glinet.FirewallRule) tfsdk.Plan {
		p := tfsdk.Plan{Schema: schemaResp.Schema, Raw: null}
		if rule != nil {
			if diags := p.Set(ctx, ruleModel(rule)); diags.HasError() {
				t.Fatal(diags)
			}
		}
		return p
	}
	state := func(rule *glinet.FirewallRule) tfsdk.State {
		s := tfsdk.State{Schema: schemaResp.Schema, Raw: null}
		if rule != nil {
			if diags := s.Set(ctx, ruleModel(rule)); diags.HasError() {
				t.Fatal(diags)
			}
		}
		return s
	}

	tests := []struct {
		name           string
		planned, prior *glinet.FirewallRule
		warn           bool
	}{
		{"create with port", &withPort, nil, false},
		{"create without port", &noPort, nil, true},
		{"update without port", &changed, &noPort, true},
		{"no change without port", &noPort, &noPort, false},
		{"destroy with port", nil, &withPort, false},
		{"destroy without port", nil, &noPort, true},
		{"replace to a port", &withPort, &noPort, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fwresource.ModifyPlanRequest{Plan: plan(tt.planned), State: state(tt.prior)}
			resp := fwresource.ModifyPlanResponse{Plan: req.Plan}
			r.ModifyPlan(ctx, req, &resp)

			warnings := resp.Diagnostics.Warnings()
			if resp.Diagnostics.HasError() || len(warnings) != map[bool]int{false: 0, true: 1}[tt.warn] {
				t.Fatalf("diagnostics are %v, want warning %t", resp.Diagnostics, tt.warn)
			}
			if tt.warn && !strings.Contains(warnings[0].Detail(), "Firmware 4.8.3 cannot change or remove") {
				t.Errorf("warning is %q", warnings[0].Detail())
			}
		})
	}
}

// TestFirewallRuleResourceNoDestinationPortRemoveError makes the remove that follows the write of the destination
// port fail one time. The next destroy finds the rule by the identity of the state, unless the restore failed too.
func TestFirewallRuleResourceNoDestinationPortRemoveError(t *testing.T) {
	parallel(t)
	before := testRule("50101", "ACCEPT", "before")
	created := glinet.FirewallRule{Name: "no port", Src: "wan", Dest: "lan", Target: "DROP", Enabled: true}
	removable := created
	removable.DestPort = removablePort
	removable.Enabled = false

	tests := []struct {
		name        string
		failRestore bool
		error       string
		left        glinet.FirewallRule
	}{
		{"restore", false,
			`(?s)disabled\s+it\s+and\s+set\s+its\s+destination\s+port\s+to\s+65535.*restored\s+the\s+original\s+values,\s+and\s+the\s+rule\s+is\s+unchanged\s+on\s+the\s+router.*500`,
			created},
		{"restore fails", true,
			`(?s)the\s+restore\s+of\s+the\s+original\s+values\s+also\s+failed,\s+so\s+the\s+router\s+can\s+have\s+the\s+rule\s+"no\s+port"\s+\(action\s+DROP,\s+enabled\s+false,.*destination\s+port\s+65535\).*Remove\s+it\s+on\s+the\s+router.*500`,
			removable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Firewall.SetRules(fakeRules(before))
			var failRemove, failSet atomic.Bool
			remove := srv.Handler("firewall", "remove_rule")
			srv.Handle("firewall", "remove_rule", func(params json.RawMessage) (any, error) {
				if failRemove.Swap(false) {
					failSet.Store(tt.failRestore)
					return nil, glinettest.ErrInternalServerError
				}
				return remove(params)
			})
			set := srv.Handler("firewall", "set_rule")
			srv.Handle("firewall", "set_rule", func(params json.RawMessage) (any, error) {
				if failSet.Swap(false) {
					return nil, glinettest.ErrInternalServerError
				}
				return set(params)
			})
			provider := fakeRouterConfig(srv)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				CheckDestroy:             expectFakeRules(srv, before),
				Steps: []resource.TestStep{
					{
						Config: provider + ruleConfig(created),
						Check:  expectFakeRules(srv, before, created),
					},
					{
						PreConfig:   func() { failRemove.Store(true) },
						Config:      provider,
						ExpectError: regexp.MustCompile(tt.error),
					},
					{
						PreConfig: func() {
							if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(before, tt.left)) {
								t.Errorf("rules after the failed delete are %v, want %v", got, fakeRules(before, tt.left))
							}
							if tt.failRestore {
								srv.Firewall.SetRules(fakeRules(before))
							}
						},
						Config: provider,
						Check:  expectFakeRules(srv, before),
					},
				},
			})
		})
	}
}

func TestFirewallRuleResourceDeleteAlreadyGone(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	removeAfterNextList := false
	list := srv.Handler("firewall", "get_rule_list")
	srv.Handle("firewall", "get_rule_list", func(params json.RawMessage) (any, error) {
		res, err := list(params)
		if removeAfterNextList {
			removeAfterNextList = false
			srv.Firewall.SetRules(nil)
		}
		return res, err
	})
	for _, method := range []string{"set_rule", "remove_rule"} {
		srv.Handle("firewall", method, func(json.RawMessage) (any, error) {
			t.Errorf("%s called", method)
			return json.RawMessage(`[]`), nil
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + ruleConfig(testRule("", "DROP", "")),
			},
			{
				PreConfig: func() { removeAfterNextList = true },
				Config:    fakeRouterConfig(srv),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(ruleAddress, plancheck.ResourceActionDestroy),
					},
				},
			},
		},
	})
}

func TestFirewallRuleResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	valid := testRule("50001", "ACCEPT", "")
	with := func(change func(*glinet.FirewallRule)) string {
		r := valid
		change(&r)
		return ruleConfig(r)
	}

	tests := []struct {
		name   string
		config string
		error  string
	}{
		{"source_zone missing", with(func(r *glinet.FirewallRule) { r.Src = "" }), `The argument "source_zone" is required`},
		{"source_zone empty", strings.Replace(ruleConfig(valid), `source_zone = "wan"`, `source_zone = ""`, 1), `string length must be at least 1`},
		{"destination_zone empty", strings.Replace(ruleConfig(valid), `destination_zone = "lan"`, `destination_zone = ""`, 1), `string length must be at least 1`},
		{"action missing", with(func(r *glinet.FirewallRule) { r.Target = "" }), `The argument "action" is required`},
		{"action lower case", with(func(r *glinet.FirewallRule) { r.Target = "accept" }), `value must be one of`},
		{"action MARK", with(func(r *glinet.FirewallRule) { r.Target = "MARK" }), `value must be one of`},
		{"protocol upper case", with(func(r *glinet.FirewallRule) { r.Proto = "TCP" }), `must be tcp udp, or one protocol name`},
		{"protocol udp tcp", with(func(r *glinet.FirewallRule) { r.Proto = "udp tcp" }), `must be tcp udp, or one protocol name`},
		{"protocol comma", with(func(r *glinet.FirewallRule) { r.Proto = "tcp,udp" }), `must be tcp udp, or one protocol name`},
		{"protocol leading dot", with(func(r *glinet.FirewallRule) { r.Proto = ".esp" }), `must be tcp udp, or one protocol name`},
		{"protocol space", with(func(r *glinet.FirewallRule) { r.Proto = "tcp icmp" }), `must be tcp udp, or one protocol name`},
		{"protocol upper case with dot", with(func(r *glinet.FirewallRule) { r.Proto = "AX.25" }), `must be tcp udp, or one protocol name`},
		{"source_ip out of range", with(func(r *glinet.FirewallRule) { r.SrcIP = "203.0.113.256" }), `source_ip must be an IPv4 or IPv6 address, or a CIDR block`},
		{"source_ip prefix too long", with(func(r *glinet.FirewallRule) { r.SrcIP = "203.0.113.0/33" }), `source_ip must be an IPv4 or IPv6 address`},
		{"source_ip IPv6 prefix too long", with(func(r *glinet.FirewallRule) { r.SrcIP = "2001:db8::/129" }), `source_ip must be an IPv4 or IPv6 address`},
		{"source_ip IPv6 zone", with(func(r *glinet.FirewallRule) { r.SrcIP = "fe80::1%br-lan" }), `source_ip must be an IPv4 or IPv6 address`},
		{"destination_ip host name", with(func(r *glinet.FirewallRule) { r.DestIP = "printer" }), `destination_ip must be an IPv4 or IPv6 address`},
		{"source_mac dashes", with(func(r *glinet.FirewallRule) { r.SrcMAC = "02-00-00-aa-bb-01" }), `must be a MAC address`},
		{"source_port 0", with(func(r *glinet.FirewallRule) { r.SrcPort = "0" }), `source_port must be a port from 1 to 65535`},
		{"destination_port 65536", with(func(r *glinet.FirewallRule) { r.DestPort = "65536" }), `destination_port must be a port`},
		{"destination_port reversed range", with(func(r *glinet.FirewallRule) { r.DestPort = "50010-50001" }), `destination_port must be a port`},
		{"destination_port name", with(func(r *glinet.FirewallRule) { r.DestPort = "http" }), `destination_port must be a port`},
		{"name empty", strings.Replace(ruleConfig(valid), "}", "  name = \"\"\n}", 1), `string length must be at least 1`},
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

	if got := srv.Firewall.Rules(); len(got) != 0 {
		t.Errorf("rules are %v, want none", got)
	}
}

func TestFirewallRuleResourceReadErrorAfterCreate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failed := false
	list := srv.Handler("firewall", "get_rule_list")
	srv.Handle("firewall", "get_rule_list", func(params json.RawMessage) (any, error) {
		if len(srv.Firewall.Rules()) > 0 && !failed {
			failed = true
			return nil, glinettest.ErrMethodNotFound
		}
		return list(params)
	})
	want := testRule("50001", "ACCEPT", "")
	config := fakeRouterConfig(srv) + ruleConfig(want)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectFakeRules(srv),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Rule.*Method not found`),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectRuleAction(plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: expectRuleState(want),
				Check:             expectFakeRules(srv, want),
			},
		},
	})
}

func TestFirewallRuleResourceRouterErrors(t *testing.T) {
	parallel(t)
	want := testRule("50001", "ACCEPT", "")
	changed := want
	changed.Target = "DROP"

	t.Run("list", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "get_rule_list")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ruleConfig(want),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Firewall Rules.*Method not found`),
				},
			},
		})
	})

	t.Run("import list", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "get_rule_list")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:        fakeRouterConfig(srv) + ruleConfig(want),
					ResourceName:  ruleAddress,
					ImportState:   true,
					ImportStateId: "test",
					ExpectError:   regexp.MustCompile(`(?s)Unable to Read Firewall Rules.*Method not found`),
				},
			},
		})
	})

	t.Run("add", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failMethod(srv, "add_rule")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + ruleConfig(want),
					ExpectError: regexp.MustCompile(`(?s)Unable to Create Firewall Rule.*Method not found`),
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			CheckDestroy:             expectFakeRules(srv),
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + ruleConfig(want),
				},
				{
					PreConfig:   failMethod(srv, "set_rule"),
					Config:      fakeRouterConfig(srv) + ruleConfig(changed),
					ExpectError: regexp.MustCompile(`(?s)Unable to Update Firewall Rule.*Method not found`),
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
					Config: fakeRouterConfig(srv) + ruleConfig(want),
				},
				{
					PreConfig:   failMethod(srv, "remove_rule"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Delete Firewall Rule.*Method not found`),
				},
				{
					PreConfig: func() {
						if got := srv.Firewall.Rules(); !slices.Equal(got, fakeRules(want)) {
							t.Errorf("rules after the failed delete are %v, want %v", got, fakeRules(want))
						}
						srv.Firewall.SetRules(nil)
					},
					Config: fakeRouterConfig(srv),
				},
			},
		})
	})
}

// expectFakeRuleSet checks that the rules of srv are want, in any order. The names must be different.
func expectFakeRuleSet(srv *glinettest.Server, want ...glinet.FirewallRule) resource.TestCheckFunc {
	byName := func(a, b glinettest.FirewallRule) int { return strings.Compare(a.Name, b.Name) }
	return func(*terraform.State) error {
		got := srv.Firewall.Rules()
		w := fakeRules(want...)
		slices.SortFunc(got, byName)
		slices.SortFunc(w, byName)
		if !slices.Equal(got, w) {
			return fmt.Errorf("router rules are %v, want %v", got, w)
		}
		return nil
	}
}

// TestFirewallRuleResourceParallel creates, changes and destroys many rules, port forwards and a DMZ in the same applies.
// Rules and port forwards share the positions that make their router IDs, so a remove between the list and the
// write of another resource moves its rule. Some rules have no destination port and need two writes to be removed.
func TestFirewallRuleResourceParallel(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	slowLists(srv, "get_rule_list", "get_port_forward_list")
	provider := fakeRouterConfig(srv)
	existingRule := testRule("50100", "ACCEPT", "existing")
	existingForward := forward("tcp", "50200", "8080", "existing")
	srv.Firewall.SetRules(fakeRules(existingRule))
	srv.Firewall.SetPortForwards(fakes(existingForward))

	var rules, changedRules []glinet.FirewallRule
	for i := range 12 {
		r := testRule(strconv.Itoa(50001+i), "ACCEPT", fmt.Sprintf("r%02d", i))
		if i%4 == 0 {
			r.DestPort = ""
			r.SrcPort = strconv.Itoa(40001 + i)
		}
		rules = append(rules, r)
		if i%3 == 0 {
			continue
		}
		if i%2 == 1 {
			r.Target = "DROP"
		}
		changedRules = append(changedRules, r)
	}
	var forwards, changedForwards []glinet.PortForward
	for i := range 6 {
		f := forward("tcp", strconv.Itoa(50101+i), "8080", fmt.Sprintf("f%d", i))
		forwards = append(forwards, f)
		if i%2 == 0 {
			f.DestPort = "9090"
			changedForwards = append(changedForwards, f)
		}
	}
	config := func(dmz string, rules []glinet.FirewallRule, forwards []glinet.PortForward) string {
		res := provider + dmz
		for _, r := range rules {
			res += ruleResource(r.Name, r)
		}
		for _, f := range forwards {
			res += forwardResource(f.Name, f)
		}
		return res
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			expectFakeRules(srv, existingRule),
			expectFakeForwards(srv, existingForward),
			expectFakeDMZ(srv, glinettest.DMZ{Priority: noPriority}),
		),
		Steps: []resource.TestStep{
			{
				Config: config(dmzConfig(dmzIP, true), rules, forwards),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectFakeRuleSet(srv, append([]glinet.FirewallRule{existingRule}, rules...)...),
					expectFakeForwardSet(srv, append([]glinet.PortForward{existingForward}, forwards...)...),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: true, DestIP: dmzIP, Priority: noPriority}),
				),
			},
			{
				Config: config(dmzConfig(otherDMZIP, false), changedRules, changedForwards),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectFakeRuleSet(srv, append([]glinet.FirewallRule{existingRule}, changedRules...)...),
					expectFakeForwardSet(srv, append([]glinet.PortForward{existingForward}, changedForwards...)...),
					expectFakeDMZ(srv, glinettest.DMZ{Enabled: false, DestIP: otherDMZIP, Priority: noPriority}),
				),
			},
		},
	})
}
