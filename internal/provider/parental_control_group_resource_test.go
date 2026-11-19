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
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	groupAddress = "glinet_parental_control_group.test"

	groupRulesetConfig = `
resource "glinet_parental_control_ruleset" "homework" {
  name      = "homework"
  block_list = ["games.example.com"]
}
`
)

func expectGroup(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(groupAddress, tfjsonpath.New(attribute), value)
}

func scheduleCheck(days []int64, begin, end, ruleset string) knownvalue.Check {
	dayChecks := make([]knownvalue.Check, len(days))
	for i, d := range days {
		dayChecks[i] = knownvalue.Int64Exact(d)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"days":       knownvalue.SetExact(dayChecks),
		"start_time": knownvalue.StringExact(begin),
		"end_time":   knownvalue.StringExact(end),
		"ruleset":    knownvalue.StringExact(ruleset),
	})
}

// expectRouterGroupNames checks that the router has exactly the groups with the names want.
func expectRouterGroupNames(client *glinet.Client, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.ParentalControl.GetConfig(context.Background())
		if err != nil {
			return err
		}
		var got []string
		for _, g := range config.Groups {
			got = append(got, g.Name)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("router has the groups %q, want %q", got, want)
		}
		return nil
	}
}

// editRouterGroup changes the first group of the router.
func editRouterGroup(t *testing.T, srv *glinettest.Server, edit func(group *glinet.ParentalControlGroup)) {
	editParentalControl(t, srv, func(config *glinet.ParentalControlConfig) { edit(&config.Groups[0]) })
}

func TestParentalControlGroupResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	adds := recordCalls(t, srv, "parental-control", "add_group")
	sets := recordCalls(t, srv, "parental-control", "set_group")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroupNames(client),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  macs            = ["02:00:00:00:00:0A", "02:00:00:00:00:01"]

  schedules = [
    {
      days    = [2, 1]
      start_time   = "16:00"
      end_time     = "18:00"
      ruleset = glinet_parental_control_ruleset.homework.id
    },
    {
      days    = [0]
      start_time   = "21:00"
      end_time     = "24:00"
      ruleset = "drop"
    },
  ]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("id", knownvalue.StringExact("cfg000002")),
					expectGroup("name", knownvalue.StringExact("kids")),
					expectGroup("default_ruleset", knownvalue.StringExact("accept")),
					expectGroup("macs", knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("02:00:00:00:00:0A"), knownvalue.StringExact("02:00:00:00:00:01"),
					})),
					expectGroup("schedules", knownvalue.SetExact([]knownvalue.Check{
						scheduleCheck([]int64{1, 2}, "16:00", "18:00", "cfg000001"),
						scheduleCheck([]int64{0}, "21:00", "24:00", "drop"),
					})),
				},
				Check: resource.ComposeTestCheckFunc(
					expectRouterGroupNames(client, "kids"),
					adds.expect(`{"name":"kids","default_rule":"accept","macs":["02:00:00:00:00:01","02:00:00:00:00:0A"]}`),
					sets.expect(`{"id":"cfg000002","schedules":[
						{"week":0,"begin":"21:00","end":"24:00","rule":"drop"},
						{"week":1,"begin":"16:00","end":"18:00","rule":"cfg000001"},
						{"week":2,"begin":"16:00","end":"18:00","rule":"cfg000001"}]}`),
				),
			},
			{
				// The router returns the MAC addresses in another letter case, the times with seconds and the end of the day as 23:59.
				PreConfig: func() {
					editRouterGroup(t, srv, func(group *glinet.ParentalControlGroup) {
						group.MACs = []string{"02:00:00:00:00:01", "02:00:00:00:00:0a"}
						for i := range group.Schedules {
							schedule := &group.Schedules[i]
							schedule.Begin += ":00"
							if schedule.End == "24:00" {
								schedule.End = "23:59"
							}
						}
					})
				},
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  macs            = ["02:00:00:00:00:0A", "02:00:00:00:00:01"]

  schedules = [
    {
      days    = [1, 2]
      start_time   = "16:00"
      end_time     = "18:00"
      ruleset = glinet_parental_control_ruleset.homework.id
    },
    {
      days    = [0]
      start_time   = "21:00"
      end_time     = "24:00"
      ruleset = "drop"
    },
  ]
}
`,
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "children"
  default_ruleset = "drop"
  macs            = ["02:00:00:00:00:01"]

  schedules = [
    {
      days    = [1, 2]
      start_time   = "16:00"
      end_time     = "18:00"
      ruleset = glinet_parental_control_ruleset.homework.id
    },
    {
      days    = [6]
      start_time   = "20:00"
      end_time     = "24:00"
      ruleset = "accept"
    },
  ]
}
`,
				ConfigPlanChecks: expectResourceAction(groupAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("id", knownvalue.StringExact("cfg000002")),
					expectGroup("name", knownvalue.StringExact("children")),
					expectGroup("schedules", knownvalue.SetExact([]knownvalue.Check{
						scheduleCheck([]int64{1, 2}, "16:00", "18:00", "cfg000001"),
						scheduleCheck([]int64{6}, "20:00", "24:00", "accept"),
					})),
				},
				Check: resource.ComposeTestCheckFunc(
					adds.expect(),
					sets.expect(
						`{"id":"cfg000002","name":"children","default_rule":"drop","macs":["02:00:00:00:00:01"]}`,
						`{"id":"cfg000002","schedules":[
							{"id":"cfg000004","week":1,"begin":"16:00","end":"18:00","rule":"cfg000001"},
							{"id":"cfg000005","week":2,"begin":"16:00","end":"18:00","rule":"cfg000001"},
							{"week":6,"begin":"20:00","end":"24:00","rule":"accept"}]}`,
					),
				),
			},
			{
				ResourceName:      groupAddress,
				ImportState:       true,
				ImportStateId:     "children",
				ImportStateVerify: true,
			},
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "children"
  default_ruleset = "drop"
  macs            = ["02:00:00:00:00:01"]
}
`,
				ConfigPlanChecks: expectResourceAction(groupAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("schedules", knownvalue.Null()),
				},
				Check: sets.expect(`{"id":"cfg000002","schedules":[]}`),
			},
		},
	})
}

func TestParentalControlGroupResourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "parental-control", "add_group")
	sets := recordCalls(t, srv, "parental-control", "set_group")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "guests"
  default_ruleset = "accept"
  macs            = []
  schedules       = []
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("macs", knownvalue.SetExact([]knownvalue.Check{})),
					expectGroup("schedules", knownvalue.SetExact([]knownvalue.Check{})),
				},
				Check: resource.ComposeTestCheckFunc(
					adds.expect(`{"name":"guests","default_rule":"accept","macs":[]}`),
					sets.expect(),
				),
			},
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "guests"
  default_ruleset = "accept"
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("macs", knownvalue.Null()),
					expectGroup("schedules", knownvalue.Null()),
				},
				Check: sets.expect(),
			},
		},
	})
}

// TestParentalControlGroupResourceIDChange checks the search by name when the router gives the group another ID,
// and the error when more than one group has the name.
func TestParentalControlGroupResourceIDChange(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	config := fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:         func() { renameRouterItem(t, srv, "groups", "cfg000001", "cfg0000aa", "kids") },
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{expectGroup("id", knownvalue.StringExact("cfg0000aa"))},
			},
			{
				PreConfig: func() {
					renameRouterItem(t, srv, "groups", "cfg0000aa", "cfg0000bb", "kids")
					if _, err := client.ParentalControl.AddGroup(context.Background(), glinet.ParentalControlGroupSettings{Name: "kids", DefaultRule: "accept"}); err != nil {
						t.Fatal(err)
					}
				},
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Find Parental Control Group.*` + wrapped(`router has 2 groups with the name "kids"`)),
			},
			{
				PreConfig: func() {
					if err := client.ParentalControl.RemoveGroup(context.Background(), "cfg0000bb"); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestParentalControlGroupResourceRemoved(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	config := fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					if err := client.ParentalControl.RemoveGroup(context.Background(), "cfg000001"); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(groupAddress, plancheck.ResourceActionCreate),
				Check:            expectRouterGroupNames(client, "kids"),
			},
		},
	})
}

func TestParentalControlGroupResourceExists(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	if _, err := client.ParentalControl.AddGroup(context.Background(), glinet.ParentalControlGroupSettings{Name: "kids", DefaultRule: "accept"}); err != nil {
		t.Fatal(err)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Parental Control Group Exists.*` + wrapped(`Import it with the ID "kids"`)),
			},
		},
	})
}

func TestParentalControlGroupResourceUnknownRuleset(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "parental-control", "add_group")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1], start_time = "08:00", end_time = "09:00", ruleset = "cfg0000ff" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unknown Parental Control Rule Set.*` + wrapped(`no parental control rule set with the ID "cfg0000ff"`)),
			},
			{
				PreConfig: func() {
					if got := adds.take(); len(got) != 0 {
						t.Errorf("parental-control.add_group was called with %q", got)
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

func TestParentalControlGroupResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for config, want := range map[string]string{
		`macs = ["02:00:00:00:00:0a", "02:00:00:00:00:0A"]`:                                        `Duplicate MAC Address`,
		`macs = ["02-00-00-00-00-01"]`:                                                             `must be a MAC address`,
		`schedules = [{ days = [1], start_time = "08:00", end_time = "08:10", ruleset = "drop" }]`: `shorter than 15 minutes`,
		`schedules = [{ days = [1], start_time = "09:00", end_time = "08:00", ruleset = "drop" }]`: `shorter than 15 minutes`,
		`schedules = [{ days = [1], start_time = "08:00", end_time = "23:59", ruleset = "drop" }]`: `24:00 instead of 23:59`,
		`schedules = [{ days = [1], start_time = "24:00", end_time = "24:00", ruleset = "drop" }]`: `must be a time from 00:00 to 23:59`,
		`schedules = [{ days = [7], start_time = "08:00", end_time = "09:00", ruleset = "drop" }]`: `must be between 0 and 6`,
		`schedules = [
    { days = [1, 2], start_time = "08:00", end_time = "10:00", ruleset = "drop" },
    { days = [2, 3], start_time = "09:45", end_time = "11:00", ruleset = "accept" },
  ]`: `On day 2, the schedule from 08:00 to 10:00 overlaps the schedule from 09:45 to 11:00`,
		`schedules = [
    { days = [1], start_time = "08:00", end_time = "10:00", ruleset = "drop" },
    { days = [2], start_time = "08:00", end_time = "10:00", ruleset = "drop" },
  ]`: `Make them one schedule with all their days`,
	} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  ` + config + `
}
`,
					ExpectError: regexp.MustCompile(wrapped(want)),
				},
			},
		})
	}
}

func TestParentalControlGroupResourceAdjacentSchedules(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1], start_time = "08:00", end_time = "10:00", ruleset = "drop" },
    { days = [1], start_time = "10:00", end_time = "24:00", ruleset = "accept" },
  ]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("schedules", knownvalue.SetExact([]knownvalue.Check{
						scheduleCheck([]int64{1}, "08:00", "10:00", "drop"),
						scheduleCheck([]int64{1}, "10:00", "24:00", "accept"),
					})),
				},
			},
		},
	})
}

func TestParentalControlGroupResourceSchedulesError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	failFirstCall(srv, "parental-control", "set_group")
	config := fakeRouterConfig(srv) + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1], start_time = "08:00", end_time = "10:00", ruleset = "drop" },
  ]
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroupNames(client),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Set Parental Control Group Schedules.*Method not found`),
			},
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{expectGroup("id", knownvalue.StringExact("cfg000002"))},
				Check:             expectRouterGroupNames(client, "kids"),
			},
		},
	})
}

// TestParentalControlGroupResourceUnknownValues checks the schedules when the rule set or a day is not known at plan time.
func TestParentalControlGroupResourceUnknownValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	adds := recordCalls(t, srv, "parental-control", "add_group")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1], start_time = "08:00", end_time = "08:10", ruleset = glinet_parental_control_ruleset.homework.id },
  ]
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`shorter than 15 minutes`),
			},
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1, 2], start_time = "08:00", end_time = "10:00", ruleset = glinet_parental_control_ruleset.homework.id },
    { days = [2], start_time = "09:00", end_time = "11:00", ruleset = "drop" },
  ]
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)Overlapping Schedules.*` + wrapped(`On day 2, the schedule from 08:00 to 10:00`)),
			},
			{
				Config: fakeRouterConfig(srv) + groupRulesetConfig + `
resource "glinet_parental_control_group" "test" {
  name            = "kids"
  default_ruleset = "accept"
  schedules = [
    { days = [1, length(glinet_parental_control_ruleset.homework.id) > 0 ? 2 : 3], start_time = "08:00", end_time = "10:00", ruleset = "accept" },
    { days = [2], start_time = "09:00", end_time = "11:00", ruleset = "drop" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Overlapping Schedules.*` + wrapped(`On day 2, the schedule from 08:00 to 10:00`)),
			},
			{
				PreConfig: func() {
					if got := adds.take(); len(got) != 0 {
						t.Errorf("parental-control.add_group was called with %q", got)
					}
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})
}

// TestParentalControlGroupResourceRenameSchedulesError checks the state when the rename succeeds and the schedule write fails.
func TestParentalControlGroupResourceRenameSchedulesError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	adds := recordCalls(t, srv, "parental-control", "add_group")
	group := func(name, begin string) string {
		return fakeRouterConfig(srv) + fmt.Sprintf(`
resource "glinet_parental_control_group" "test" {
  name            = %q
  default_ruleset = "accept"
  schedules = [
    { days = [1], start_time = %q, end_time = "10:00", ruleset = "drop" },
  ]
}
`, name, begin)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterGroupNames(client),
		Steps: []resource.TestStep{
			{
				Config: group("kids", "08:00"),
				Check:  adds.expect(`{"name":"kids","default_rule":"accept","macs":[]}`),
			},
			{
				PreConfig: func() {
					next := srv.Handler("parental-control", "set_group")
					srv.Handle("parental-control", "set_group", func(params json.RawMessage) (any, error) {
						if strings.Contains(string(params), "schedules") {
							return nil, glinettest.ErrMethodNotFound
						}
						return next(params)
					})
				},
				Config:      group("children", "09:00"),
				ExpectError: regexp.MustCompile(`(?s)Unable to Set Parental Control Group Schedules.*Method not found`),
			},
			{
				Config:           group("children", "08:00"),
				ConfigPlanChecks: expectEmptyPlan,
				ConfigStateChecks: []statecheck.StateCheck{
					expectGroup("id", knownvalue.StringExact("cfg000001")),
					expectGroup("name", knownvalue.StringExact("children")),
				},
				Check: resource.ComposeTestCheckFunc(
					adds.expect(),
					expectRouterGroupNames(client, "children"),
				),
			},
		},
	})
}
