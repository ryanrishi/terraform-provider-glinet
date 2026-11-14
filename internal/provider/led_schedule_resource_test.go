package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
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

const ledScheduleAddress = "glinet_led_schedule.test"

var allWeekDays = []int{0, 1, 2, 3, 4, 5, 6}

// ledSchedule is the configuration of a glinet_led_schedule resource.
type ledSchedule struct {
	enabled bool
	on, off string
	days    []int
}

func (s ledSchedule) config() string {
	return fmt.Sprintf(`
resource "glinet_led_schedule" "test" {
  enabled       = %t
  turn_on_time  = %q
  turn_off_time = %q
  days          = %s
}
`, s.enabled, s.on, s.off, hclNumbers(s.days))
}

// router returns the LED schedule that the router has after an apply of s.
func (s ledSchedule) router() glinet.LEDSchedule {
	return glinet.LEDSchedule{
		Enabled:     s.enabled,
		TurnOnHour:  s.on[:2],
		TurnOnMin:   s.on[3:],
		TurnOffHour: s.off[:2],
		TurnOffMin:  s.off[3:],
		Week:        s.days,
	}
}

func hclNumbers(numbers []int) string {
	s := make([]string, len(numbers))
	for i, n := range numbers {
		s[i] = fmt.Sprint(n)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func expectDays(address string, days []int) statecheck.StateCheck {
	values := make([]knownvalue.Check, len(days))
	for i, d := range days {
		values[i] = knownvalue.Int64Exact(int64(d))
	}
	return statecheck.ExpectKnownValue(address, tfjsonpath.New("days"), knownvalue.SetExact(values))
}

func expectLEDScheduleState(s ledSchedule) []statecheck.StateCheck {
	expect := func(attribute string, value knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(ledScheduleAddress, tfjsonpath.New(attribute), value)
	}
	return []statecheck.StateCheck{
		expect("id", knownvalue.StringExact("led_schedule")),
		expect("enabled", knownvalue.Bool(s.enabled)),
		expect("turn_on_time", knownvalue.StringExact(s.on)),
		expect("turn_off_time", knownvalue.StringExact(s.off)),
		expectDays(ledScheduleAddress, s.days),
	}
}

// expectRouterLEDSchedule checks that the LED schedule of the router is want.
func expectRouterLEDSchedule(client *glinet.Client, want glinet.LEDSchedule) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.Timer.GetLEDSchedule(context.Background())
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(*got, want) {
			return fmt.Errorf("router LED schedule is %+v, want %+v", *got, want)
		}
		return nil
	}
}

func setFakeLEDSchedule(srv *glinettest.Server, s glinet.LEDSchedule) {
	srv.Timer.SetLEDSchedule(glinettest.LEDSchedule(s))
}

// restoreLEDSchedule writes initial to the router. The router keeps times and days only from an enabled write,
// so a disabled schedule is written enabled and then disabled.
func restoreLEDSchedule(ctx context.Context, client *glinet.Client, initial glinet.LEDSchedule) error {
	enabled := initial
	enabled.Enabled = true
	if err := client.Timer.SetLEDSchedule(ctx, enabled); err != nil {
		return err
	}
	if initial.Enabled {
		return nil
	}
	return client.Timer.SetLEDSchedule(ctx, glinet.LEDSchedule{Enabled: false})
}

// ledScheduleTestRouter returns the provider block and a client for the router of the test.
// On the real router, the LED schedule goes back to its initial value when the test ends.
func ledScheduleTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.Timer.GetLEDSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := restoreLEDSchedule(ctx, client, *initial); err != nil {
			t.Fatalf("restore LED schedule: %v", err)
		}

		restored, err := client.Timer.GetLEDSchedule(ctx)
		if err != nil {
			t.Fatalf("read restored LED schedule: %v", err)
		}
		if !reflect.DeepEqual(*restored, *initial) {
			t.Errorf("restored LED schedule is %+v, want %+v", *restored, *initial)
		}
	})

	return `provider "glinet" {}`, client
}

func TestLEDScheduleResource(t *testing.T) {
	provider, client := ledScheduleTestRouter(t)
	enabled := ledSchedule{enabled: true, on: "08:30", off: "21:15", days: []int{1, 3, 5}}
	disabled := ledSchedule{enabled: false, on: "06:45", off: "23:00", days: []int{0, 6}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLEDSchedule(client, disabled.router()),
		Steps: []resource.TestStep{
			{
				Config:            provider + enabled.config(),
				ConfigStateChecks: expectLEDScheduleState(enabled),
				Check:             expectRouterLEDSchedule(client, enabled.router()),
			},
			{
				Config:            provider + disabled.config(),
				ConfigPlanChecks:  expectResourceAction(ledScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLEDScheduleState(disabled),
				Check:             expectRouterLEDSchedule(client, disabled.router()),
			},
			{
				Config:           provider + disabled.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				ResourceName:      ledScheduleAddress,
				ImportState:       true,
				ImportStateId:     "led_schedule",
				ImportStateVerify: true,
			},
		},
	})
}

func TestLEDScheduleResourceUpdate(t *testing.T) {
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)

	schedules := []ledSchedule{
		{enabled: true, on: "07:00", off: "22:00", days: []int{1, 2}},
		{enabled: false, on: "07:00", off: "22:00", days: []int{1, 2}},
		{enabled: false, on: "07:30", off: "22:00", days: []int{1, 2}},
		{enabled: false, on: "07:30", off: "23:59", days: []int{1, 2}},
		{enabled: false, on: "07:30", off: "23:59", days: []int{0, 6}},
		{enabled: true, on: "07:30", off: "23:59", days: []int{0, 6}},
		{enabled: true, on: "00:00", off: "23:59", days: []int{0, 6}},
		{enabled: true, on: "00:00", off: "12:05", days: []int{0, 6}},
		{enabled: true, on: "00:00", off: "12:05", days: allWeekDays},
	}

	var steps []resource.TestStep
	for i, s := range schedules {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + s.config(),
			ConfigStateChecks: expectLEDScheduleState(s),
			Check:             expectRouterLEDSchedule(client, s.router()),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(ledScheduleAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step)
	}
	last := steps[len(steps)-1].Config
	steps = append(steps, resource.TestStep{Config: last, ConfigPlanChecks: expectEmptyPlan})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterLEDSchedule(client, schedules[len(schedules)-1].router()),
		Steps:                    steps,
	})
}

// TestLEDScheduleResourceDisabledCreate creates a disabled schedule with other values than the router has.
// The router then has the configured values, so the next plan is empty.
func TestLEDScheduleResourceDisabledCreate(t *testing.T) {
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	s := ledSchedule{enabled: false, on: "09:10", off: "20:40", days: []int{2, 4}}
	var (
		mu     sync.Mutex
		writes []string
	)
	set := srv.Handler("timer", "set_led")
	srv.Handle("timer", "set_led", func(params json.RawMessage) (any, error) {
		mu.Lock()
		writes = append(writes, string(params))
		mu.Unlock()
		return set(params)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + s.config(),
				ConfigStateChecks: expectLEDScheduleState(s),
				Check:             expectRouterLEDSchedule(client, s.router()),
			},
			{
				Config:           fakeRouterConfig(srv) + s.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	want := []string{
		`{"enable":true,"turnon_hour":"09","turnon_min":"10","turnoff_hour":"20","turnoff_min":"40","week":[2,4]}`,
		`{"enable":false}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(writes, want) {
		t.Errorf("set_led writes are %q, want %q", writes, want)
	}
}

func TestLEDScheduleResourceSortedDays(t *testing.T) {
	srv := glinettest.NewServer(t)
	s := ledSchedule{enabled: true, on: "07:00", off: "22:00", days: []int{6, 0, 3}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + s.config(),
				Check:  expectRouterLEDSchedule(testClient(t, srv.URL, srv.Username, srv.Password), ledSchedule{true, "07:00", "22:00", []int{0, 3, 6}}.router()),
			},
		},
	})
}

func TestLEDScheduleResourceImport(t *testing.T) {
	tests := []struct {
		name   string
		router glinet.LEDSchedule
		config ledSchedule
	}{
		{"enabled",
			glinet.LEDSchedule{Enabled: true, TurnOnHour: "06", TurnOnMin: "15", TurnOffHour: "23", TurnOffMin: "45", Week: []int{1, 2, 3, 4, 5}},
			ledSchedule{true, "06:15", "23:45", []int{1, 2, 3, 4, 5}}},
		{"disabled",
			glinet.LEDSchedule{Enabled: false, TurnOnHour: "07", TurnOnMin: "00", TurnOffHour: "22", TurnOffMin: "00", Week: allWeekDays},
			ledSchedule{false, "07:00", "22:00", allWeekDays}},
		{"one digit times",
			glinet.LEDSchedule{Enabled: true, TurnOnHour: "7", TurnOnMin: "0", TurnOffHour: "9", TurnOffMin: "5", Week: []int{1}},
			ledSchedule{true, "07:00", "09:05", []int{1}}},
		{"unsorted and duplicate days",
			glinet.LEDSchedule{Enabled: true, TurnOnHour: "07", TurnOnMin: "00", TurnOffHour: "22", TurnOffMin: "00", Week: []int{5, 1, 1, 3}},
			ledSchedule{true, "07:00", "22:00", []int{1, 3, 5}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeLEDSchedule(srv, tt.router)
			config := fakeRouterConfig(srv) + tt.config.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       ledScheduleAddress,
						ImportState:        true,
						ImportStateId:      "led_schedule",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectLEDScheduleState(tt.config),
					},
				},
			})

			if got := glinet.LEDSchedule(srv.Timer.LEDSchedule()); !reflect.DeepEqual(got, tt.router) {
				t.Errorf("router LED schedule is %+v, want %+v", got, tt.router)
			}
		})
	}
}

func TestLEDScheduleResourceImportInvalidID(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + ledSchedule{false, "07:00", "22:00", allWeekDays}.config(),
				ResourceName:  ledScheduleAddress,
				ImportState:   true,
				ImportStateId: "led",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestLEDScheduleResourceDrift(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled %t", enabled), func(t *testing.T) {
			srv := glinettest.NewServer(t)
			client := testClient(t, srv.URL, srv.Username, srv.Password)
			want := ledSchedule{enabled: enabled, on: "07:00", off: "22:00", days: []int{1, 3}}
			config := fakeRouterConfig(srv) + want.config()
			with := func(change func(*glinet.LEDSchedule)) glinet.LEDSchedule {
				s := want.router()
				s.Week = []int{1, 3}
				change(&s)
				return s
			}

			tests := []struct {
				name   string
				router glinet.LEDSchedule
			}{
				{"enabled", with(func(s *glinet.LEDSchedule) { s.Enabled = !enabled })},
				{"turn on hour", with(func(s *glinet.LEDSchedule) { s.TurnOnHour = "08" })},
				{"turn on minute", with(func(s *glinet.LEDSchedule) { s.TurnOnMin = "30" })},
				{"turn off hour", with(func(s *glinet.LEDSchedule) { s.TurnOffHour = "23" })},
				{"turn off minute", with(func(s *glinet.LEDSchedule) { s.TurnOffMin = "59" })},
				{"day added", with(func(s *glinet.LEDSchedule) { s.Week = []int{1, 3, 4} })},
				{"day removed", with(func(s *glinet.LEDSchedule) { s.Week = []int{1} })},
				{"invalid time", with(func(s *glinet.LEDSchedule) { s.TurnOnHour = "25" })},
				{"invalid day", with(func(s *glinet.LEDSchedule) { s.Week = []int{1, 3, 7} })},
			}

			steps := []resource.TestStep{{Config: config}}
			for _, tt := range tests {
				steps = append(steps, resource.TestStep{
					PreConfig:         func() { setFakeLEDSchedule(srv, tt.router) },
					Config:            config,
					ConfigPlanChecks:  expectResourceAction(ledScheduleAddress, plancheck.ResourceActionUpdate),
					ConfigStateChecks: expectLEDScheduleState(want),
					Check:             expectRouterLEDSchedule(client, want.router()),
				})
			}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps:                    steps,
			})
		})
	}
}

func TestLEDScheduleResourceInvalidConfig(t *testing.T) {
	srv := glinettest.NewServer(t)
	before := srv.Timer.LEDSchedule()

	tests := []struct {
		name   string
		config ledSchedule
		error  string
	}{
		{"one digit hour", ledSchedule{true, "7:00", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"hour 24", ledSchedule{true, "07:00", "24:00", []int{1}}, `turn_off_time must be a time from 00:00 to 23:59`},
		{"minute 60", ledSchedule{true, "07:60", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"seconds", ledSchedule{true, "07:00:00", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"no colon", ledSchedule{true, "0700", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"empty", ledSchedule{true, "07:00", "", []int{1}}, `turn_off_time must be a time from 00:00 to 23:59`},
		{"12-hour", ledSchedule{true, "07:00 PM", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"no days", ledSchedule{true, "07:00", "22:00", []int{}}, `days set must contain at least 1 elements`},
		{"day 7", ledSchedule{true, "07:00", "22:00", []int{1, 7}}, `value must be between 0 and 6, got: 7`},
		{"day -1", ledSchedule{false, "07:00", "22:00", []int{-1}}, `value must be between 0 and 6, got: -1`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + tt.config.config(),
						ExpectError: regexp.MustCompile(tt.error),
					},
				},
			})
		})
	}

	if got := srv.Timer.LEDSchedule(); !reflect.DeepEqual(got, before) {
		t.Errorf("router LED schedule is %+v, want %+v", got, before)
	}
}

func TestLEDScheduleResourceRouterErrors(t *testing.T) {
	s := ledSchedule{enabled: false, on: "08:00", off: "21:00", days: []int{1}}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "timer", "get_led")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LED Schedule.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks:  expectResourceAction(ledScheduleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectLEDScheduleState(s),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("timer", "get_led")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + s.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "timer", "get_led"),
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read LED Schedule.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("timer", "get_led", get) },
					Config:           fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.Timer.LEDSchedule()
		failRouterMethod(srv, "timer", "set_led")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set LED Schedule.*Method not found`),
				},
			},
		})

		if got := srv.Timer.LEDSchedule(); !reflect.DeepEqual(got, before) {
			t.Errorf("router LED schedule is %+v, want %+v", got, before)
		}
	})

	t.Run("set result error", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Handle("timer", "set_led", func(json.RawMessage) (any, error) {
			return map[string]any{"err_code": -1, "err_msg": "parameter missing"}, nil
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set LED Schedule.*parameter missing`),
				},
			},
		})
	})

	t.Run("disable", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		client := testClient(t, srv.URL, srv.Username, srv.Password)
		set := srv.Handler("timer", "set_led")
		var failDisable atomic.Bool
		failDisable.Store(true)
		srv.Handle("timer", "set_led", func(params json.RawMessage) (any, error) {
			if failDisable.Load() && string(params) == `{"enable":false}` {
				return nil, glinettest.ErrMethodNotFound
			}
			return set(params)
		})
		enabled := s
		enabled.enabled = true

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable LED Schedule.*The router has the schedule enabled.*Method not found`),
				},
				{
					PreConfig: func() {
						if err := expectRouterLEDSchedule(client, enabled.router())(nil); err != nil {
							t.Error(err)
						}
						failDisable.Store(false)
					},
					Config:            fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks:  expectResourceAction(ledScheduleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectLEDScheduleState(s),
					Check:             expectRouterLEDSchedule(client, s.router()),
				},
			},
		})
	})
}
