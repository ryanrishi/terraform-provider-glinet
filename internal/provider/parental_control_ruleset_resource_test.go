package provider

import (
	"context"
	"encoding/json"
	"fmt"
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

const rulesetAddress = "glinet_parental_control_ruleset.test"

func expectRuleset(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(rulesetAddress, tfjsonpath.New(attribute), value)
}

// expectRouterRulesets checks that the router has exactly the rule sets with the names want, after the presets.
func expectRouterRulesets(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.ParentalControl.GetConfig(context.Background())
		if err != nil {
			return err
		}
		var got []string
		for _, r := range config.Rules[2:] {
			got = append(got, r.Name)
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("router has the rule sets %q, want %q", got, want)
		}
		return nil
	}
}

// renameRouterItem gives the router group or rule set with the ID id the new ID newID and the name name.
func renameRouterItem(t *testing.T, srv *glinettest.Server, key, id, newID, name string) {
	editParentalControl(t, srv, func(config *glinet.ParentalControlConfig) {
		for i := range config.Rules {
			if r := &config.Rules[i]; key == "rules" && r.ID == id {
				r.ID, r.Name = newID, name
				return
			}
		}
		for i := range config.Groups {
			if g := &config.Groups[i]; key == "groups" && g.ID == id {
				g.ID, g.Name = newID, name
				return
			}
		}
		t.Fatalf("router has no %s with the ID %q", key, id)
	})
}

func TestParentalControlRulesetResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	adds := recordCalls(t, srv, "parental-control", "add_rule")
	sets := recordCalls(t, srv, "parental-control", "set_rule")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterRulesets(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name      = "homework"
  color     = "#4B9BD9"
  apps      = [8009, 1001]
  blacklist = ["games.example.com", "[tcp;;;www.example.com;;]"]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectRuleset("id", knownvalue.StringExact("cfg000001")),
					expectRuleset("name", knownvalue.StringExact("homework")),
					expectRuleset("color", knownvalue.StringExact("#4B9BD9")),
					expectRuleset("apps", knownvalue.SetExact([]knownvalue.Check{knownvalue.Int64Exact(1001), knownvalue.Int64Exact(8009)})),
					expectRuleset("blacklist", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("games.example.com"), knownvalue.StringExact("[tcp;;;www.example.com;;]"),
					})),
					expectRuleset("url", knownvalue.Null()),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterRulesets(client, "homework"),
					adds.expect(`{"name":"homework","color":"#4B9BD9","apps":[1001,8009],"manual":true,"blacklist":["games.example.com","[tcp;;;www.example.com;;]"]}`),
				),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name = "night"
  url  = "https://blocklist.example.com/night.txt"
}
`,
				ConfigPlanChecks: expectResourceAction(rulesetAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectRuleset("id", knownvalue.StringExact("cfg000001")),
					expectRuleset("color", knownvalue.StringExact("#4B9BD9")),
					expectRuleset("apps", knownvalue.Null()),
					expectRuleset("blacklist", knownvalue.Null()),
					expectRuleset("url", knownvalue.StringExact("https://blocklist.example.com/night.txt")),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterRulesets(client, "night"),
					adds.expect(),
					sets.expect(`{"id":"cfg000001","name":"night","color":"#4B9BD9","apps":[],"manual":false,"url":"https://blocklist.example.com/night.txt"}`),
				),
			},
			{
				ResourceName:      rulesetAddress,
				ImportState:       true,
				ImportStateId:     "night",
				ImportStateVerify: true,
			},
		},
	})
}

func TestParentalControlRulesetResourceDefaults(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "parental-control", "add_rule")
	sets := recordCalls(t, srv, "parental-control", "set_rule")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name      = "empty"
  apps      = []
  blacklist = []
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectRuleset("color", knownvalue.StringExact(defaultRulesetColor)),
					expectRuleset("apps", knownvalue.SetExact([]knownvalue.Check{})),
					expectRuleset("blacklist", knownvalue.ListExact([]knownvalue.Check{})),
				},
				Check: adds.expect(`{"name":"empty","color":"#808080","apps":[],"manual":true,"blacklist":[]}`),
			},
			{
				PreConfig: func() {
					editParentalControl(t, srv, func(config *glinet.ParentalControlConfig) { config.Rules[2].Color = "#123456" })
				},
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name = "renamed"
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectRuleset("color", knownvalue.StringExact("#123456")),
					expectRuleset("apps", knownvalue.Null()),
					expectRuleset("blacklist", knownvalue.Null()),
				},
				Check: sets.expect(`{"id":"cfg000001","name":"renamed","color":"#123456","apps":[],"manual":true,"blacklist":[]}`),
			},
		},
	})
}

// TestParentalControlRulesetResourceIDChange checks the search by name when the router gives the rule set another ID,
// and that an ID of a rule set with another name does not match.
func TestParentalControlRulesetResourceIDChange(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	sets := recordCalls(t, srv, "parental-control", "set_rule")
	config := fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name      = "homework"
  blacklist = ["games.example.com"]
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { renameRouterItem(t, srv, "rules", "cfg000001", "cfg0000aa", "homework") },
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{expectRuleset("id", knownvalue.StringExact("cfg0000aa"))},
			},
			{
				PreConfig: func() { renameRouterItem(t, srv, "rules", "cfg0000aa", "cfg0000aa", "other") },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(rulesetAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterRulesets(client, "other", "homework"),
					sets.expect(),
				),
			},
		},
	})
}

func TestParentalControlRulesetResourceExists(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	if _, err := client.ParentalControl.AddRule(context.Background(), glinet.ParentalControlRuleSettings{Name: "homework", Manual: true}); err != nil {
		t.Fatal(err)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name = "homework"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Parental Control Rule Set Exists.*` + wrapped(`Import it with the ID "homework"`)),
			},
		},
	})
}

func TestParentalControlRulesetResourceDeleteInUse(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	removes := recordCalls(t, srv, "parental-control", "remove_rule")
	var groupID string
	config := fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name = "homework"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterRulesets(client),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(*terraform.State) (err error) {
					groupID, err = client.ParentalControl.AddGroup(context.Background(), glinet.ParentalControlGroupSettings{Name: "kids", DefaultRule: "cfg000001"})
					return err
				},
			},
			{
				Config:      fakeRouterConfig(srv),
				ExpectError: regexp.MustCompile(`(?s)Parental Control Rule Set In Use.*` + wrapped(`group "kids" (ID "cfg000002") uses it as default rule set`)),
			},
			{
				PreConfig: func() {
					ctx := context.Background()
					if err := client.ParentalControl.SetGroup(ctx, groupID, glinet.ParentalControlGroupSettings{Name: "kids", DefaultRule: "accept"}); err != nil {
						t.Fatal(err)
					}
					if err := client.ParentalControl.SetSchedules(ctx, groupID, []glinet.ParentalControlSchedule{{Week: 1, Begin: "08:00", End: "09:00", Rule: "cfg000001"}}); err != nil {
						t.Fatal(err)
					}
				},
				Config:      fakeRouterConfig(srv),
				ExpectError: regexp.MustCompile(`(?s)Parental Control Rule Set In Use.*` + wrapped(`group "kids" (ID "cfg000002") uses it in a schedule`)),
			},
			{
				PreConfig: func() {
					ctx := context.Background()
					if err := client.ParentalControl.SetSchedules(ctx, groupID, nil); err != nil {
						t.Fatal(err)
					}
					if err := client.ParentalControl.StartBrief(ctx, groupID, "cfg000001", ""); err != nil {
						t.Fatal(err)
					}
				},
				Config:      fakeRouterConfig(srv),
				ExpectError: regexp.MustCompile(`(?s)Unable to Delete Parental Control Rule Set.*-3.*` + wrapped(`as its temporary rule set`)),
			},
			{
				PreConfig: func() {
					if got := removes.take(); len(got) != 1 {
						t.Errorf("parental-control.remove_rule was called with %q, want one call", got)
					}
					if err := client.ParentalControl.StopBrief(context.Background(), groupID); err != nil {
						t.Fatal(err)
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

func TestParentalControlRulesetResourceImportPreset(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, id := range []string{"drop", "accept_all"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name = "homework"
}
`,
					ResourceName:  rulesetAddress,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Preset Parental Control Rule Set`),
				},
			},
		})
	}
}

func TestParentalControlRulesetResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for config, want := range map[string]string{
		`name = "drop_all"`: `value must be none of`,
		`name = "a"
  color = "blue"`: `must be a color in the format #rrggbb`,
		`name = "a"
  blacklist = ["example.com", ""]`: `must not be empty`,
		`name = "a"
  blacklist = ["exa mple.com"]`: `can have only the characters`,
		`name = "a"
  blacklist = ["example.com"]
  url = "https://example.com/list.txt"`: `Invalid Attribute Combination`,
		`name = "a"
  url = "example.com/list.txt"`: `must be a URL`,
		`name = "a"
  apps = [0]`: `must be at least 1`,
	} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + "resource \"glinet_parental_control_ruleset\" \"test\" {\n  " + config + "\n}\n",
					ExpectError: regexp.MustCompile(want),
				},
			},
		})
	}
}

func TestParentalControlRulesetResourceWriteError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("parental-control", "add_rule", func(json.RawMessage) (any, error) {
		return map[string]any{"err_code": -2, "err_msg": "invalid domain", "valid_list": []string{"example.com"}}, nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_ruleset" "test" {
  name      = "homework"
  blacklist = ["example..com"]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to Create Parental Control Rule Set.*` + wrapped(`rejects a domain of the block list`)),
			},
		},
	})
}
