package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sync"
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

const rebootScheduleAddress = "glinet_reboot_schedule.test"

// rebootSchedule is the configuration of a glinet_reboot_schedule resource.
type rebootSchedule struct {
	enabled bool
	time    string
	days    []int
}

func (s rebootSchedule) config() string {
	return fmt.Sprintf(`
resource "glinet_reboot_schedule" "test" {
  enabled = %t
  time    = %q
  days    = %s
}
`, s.enabled, s.time, hclNumbers(s.days))
}

// router returns the reboot schedule that the router has after an enabled write of s.
func (s rebootSchedule) router() glinet.RebootSchedule {
	return glinet.RebootSchedule{Enabled: s.enabled, Hour: s.time[:2], Min: s.time[3:], Week: s.days}
}

func expectRebootScheduleState(s rebootSchedule) []statecheck.StateCheck {
	expect := func(attribute string, value knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(rebootScheduleAddress, tfjsonpath.New(attribute), value)
	}
	return []statecheck.StateCheck{
		expect("id", knownvalue.StringExact("reboot_schedule")),
		expect("enabled", knownvalue.Bool(s.enabled)),
		expect("time", knownvalue.StringExact(s.time)),
		expectDays(rebootScheduleAddress, s.days),
	}
}

// expectRouterRebootSchedule checks that the reboot schedule of the router is want.
func expectRouterRebootSchedule(client *glinet.Client, want glinet.RebootSchedule) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := client.Timer.GetRebootSchedule(context.Background())
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(*got, want) {
			return fmt.Errorf("router reboot schedule is %+v, want %+v", *got, want)
		}
		return nil
	}
}

func setFakeRebootSchedule(srv *glinettest.Server, s glinet.RebootSchedule) {
	srv.Timer.SetRebootSchedule(glinettest.RebootSchedule(s))
}

// allowRebootScheduleDisabled allows only the read of the reboot schedule and the write {"enable":false},
// so that no reboot can be scheduled on the real router.
func allowRebootScheduleDisabled(module, method string, params map[string]json.RawMessage) error {
	if module == "timer" && method == "get_reboot" {
		return nil
	}
	if module == "timer" && method == "set_reboot" && len(params) == 1 && string(params["enable"]) == "false" {
		return nil
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// rebootScheduleTestRouter returns the provider block and a client for the router of the test, and the initial reboot schedule.
// The real router is used only through guardedRealRouter with allowRebootScheduleDisabled, and only when its schedule is disabled.
// The schedule is disabled again when the test ends; disabled writes do not change the time and days of the router.
func rebootScheduleTestRouter(t *testing.T) (string, *glinet.Client, glinet.RebootSchedule) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password), glinet.RebootSchedule(srv.Timer.RebootSchedule())
	}

	provider, client := guardedRealRouter(t, allowRebootScheduleDisabled)
	initial, err := client.Timer.GetRebootSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Enabled {
		t.Fatalf("the reboot schedule of the router is enabled; the test runs only when it is disabled")
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.Timer.SetRebootSchedule(ctx, glinet.RebootSchedule{Enabled: false}); err != nil {
			t.Fatalf("disable reboot schedule: %v", err)
		}

		restored, err := client.Timer.GetRebootSchedule(ctx)
		if err != nil {
			t.Fatalf("read restored reboot schedule: %v", err)
		}
		if !reflect.DeepEqual(*restored, *initial) {
			t.Errorf("restored reboot schedule is %+v, want %+v", *restored, *initial)
		}
	})

	return provider, client, *initial
}

// TestRebootScheduleResource uses only disabled schedules, because it also runs on the real router.
func TestRebootScheduleResource(t *testing.T) {
	parallel(t)
	provider, client, initial := rebootScheduleTestRouter(t)
	other := rebootSchedule{enabled: false, time: "04:30", days: []int{2, 5}}
	current := rebootSchedule{enabled: false, time: joinClock(initial.Hour, initial.Min), days: initial.Week}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterRebootSchedule(client, initial),
		Steps: []resource.TestStep{
			{
				Config:            provider + other.config(),
				ConfigStateChecks: expectRebootScheduleState(other),
				Check:             expectRouterRebootSchedule(client, initial),
			},
			{
				Config:           provider + other.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            provider + current.config(),
				ConfigPlanChecks:  expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRebootScheduleState(current),
				Check:             expectRouterRebootSchedule(client, initial),
			},
			{
				ResourceName:      rebootScheduleAddress,
				ImportState:       true,
				ImportStateId:     "reboot_schedule",
				ImportStateVerify: true,
			},
		},
	})
}

func TestRebootScheduleResourceUpdate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	enabled := rebootSchedule{enabled: true, time: "02:15", days: []int{0, 3}}
	laterTime := rebootSchedule{enabled: true, time: "05:45", days: []int{0, 3}}
	otherDays := rebootSchedule{enabled: true, time: "05:45", days: []int{6}}
	disabled := rebootSchedule{enabled: false, time: "05:45", days: []int{6}}
	disabledChanged := rebootSchedule{enabled: false, time: "01:00", days: allWeekDays}
	enabledChanged := rebootSchedule{enabled: true, time: "01:00", days: allWeekDays}
	disabledRouter := otherDays.router()
	disabledRouter.Enabled = false
	destroyedRouter := enabledChanged.router()
	destroyedRouter.Enabled = false

	tests := []struct {
		config rebootSchedule
		router glinet.RebootSchedule
	}{
		{enabled, enabled.router()},
		{laterTime, laterTime.router()},
		{otherDays, otherDays.router()},
		{disabled, disabledRouter},
		{disabledChanged, disabledRouter},
		{enabledChanged, enabledChanged.router()},
	}

	var steps []resource.TestStep
	for i, tt := range tests {
		step := resource.TestStep{
			Config:            fakeRouterConfig(srv) + tt.config.config(),
			ConfigStateChecks: expectRebootScheduleState(tt.config),
			Check:             expectRouterRebootSchedule(client, tt.router),
		}
		if i > 0 {
			step.ConfigPlanChecks = expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionUpdate)
		}
		steps = append(steps, step, resource.TestStep{Config: step.Config, ConfigPlanChecks: expectEmptyPlan})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterRebootSchedule(client, destroyedRouter),
		Steps:                    steps,
	})
}

func TestRebootScheduleResourceWrites(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	var (
		mu     sync.Mutex
		writes []string
	)
	set := srv.Handler("timer", "set_reboot")
	srv.Handle("timer", "set_reboot", func(params json.RawMessage) (any, error) {
		mu.Lock()
		writes = append(writes, string(params))
		mu.Unlock()
		return set(params)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: fakeRouterConfig(srv) + rebootSchedule{false, "04:00", []int{1}}.config()},
			{Config: fakeRouterConfig(srv) + rebootSchedule{true, "04:00", []int{6, 1, 3}}.config()},
		},
	})

	want := []string{
		`{"enable":false}`,
		`{"enable":true,"hour":"04","min":"00","week":[1,3,6]}`,
		`{"enable":false}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(writes, want) {
		t.Errorf("set_reboot writes are %q, want %q", writes, want)
	}
}

func TestRebootScheduleResourceImport(t *testing.T) {
	parallel(t)
	tests := []struct {
		name   string
		router glinet.RebootSchedule
		config rebootSchedule
	}{
		{"enabled",
			glinet.RebootSchedule{Enabled: true, Hour: "04", Min: "30", Week: []int{0, 6}},
			rebootSchedule{true, "04:30", []int{0, 6}}},
		{"disabled",
			glinet.RebootSchedule{Enabled: false, Hour: "03", Min: "00", Week: []int{1, 4}},
			rebootSchedule{false, "03:00", []int{1, 4}}},
		{"one digit times",
			glinet.RebootSchedule{Enabled: true, Hour: "3", Min: "5", Week: []int{1}},
			rebootSchedule{true, "03:05", []int{1}}},
		{"unsorted and duplicate days",
			glinet.RebootSchedule{Enabled: true, Hour: "03", Min: "00", Week: []int{4, 1, 4}},
			rebootSchedule{true, "03:00", []int{1, 4}}},
		{"all days",
			glinet.RebootSchedule{Enabled: true, Hour: "03", Min: "00", Week: allWeekDays},
			rebootSchedule{true, "03:00", allWeekDays}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setFakeRebootSchedule(srv, tt.router)
			config := fakeRouterConfig(srv) + tt.config.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       rebootScheduleAddress,
						ImportState:        true,
						ImportStateId:      "reboot_schedule",
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: expectRebootScheduleState(tt.config),
						Check:             expectRouterRebootSchedule(testClient(t, srv.URL, srv.Username, srv.Password), tt.router),
					},
				},
			})
		})
	}
}

// TestRebootScheduleResourceImportDisabledOtherValues imports a disabled schedule with other times and days than the
// configuration. The update keeps the schedule disabled and stores the configured values in the state.
func TestRebootScheduleResourceImportDisabledOtherValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	router := glinet.RebootSchedule(srv.Timer.RebootSchedule())
	want := rebootSchedule{enabled: false, time: "23:30", days: []int{5}}
	config := fakeRouterConfig(srv) + want.config()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       rebootScheduleAddress,
				ImportState:        true,
				ImportStateId:      "reboot_schedule",
				ImportStatePersist: true,
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRebootScheduleState(want),
				Check:             expectRouterRebootSchedule(testClient(t, srv.URL, srv.Username, srv.Password), router),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestRebootScheduleResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + rebootSchedule{false, "03:00", []int{1, 4}}.config(),
				ResourceName:  rebootScheduleAddress,
				ImportState:   true,
				ImportStateId: "reboot",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestRebootScheduleResourceDrift(t *testing.T) {
	parallel(t)
	t.Run("enabled", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		client := testClient(t, srv.URL, srv.Username, srv.Password)
		want := rebootSchedule{enabled: true, time: "04:00", days: []int{2, 4}}
		config := fakeRouterConfig(srv) + want.config()
		with := func(change func(*glinet.RebootSchedule)) glinet.RebootSchedule {
			s := want.router()
			s.Week = []int{2, 4}
			change(&s)
			return s
		}

		tests := []struct {
			name   string
			router glinet.RebootSchedule
		}{
			{"disabled", with(func(s *glinet.RebootSchedule) { s.Enabled = false })},
			{"disabled with other values", glinet.RebootSchedule{Enabled: false, Hour: "05", Min: "00", Week: []int{1}}},
			{"hour", with(func(s *glinet.RebootSchedule) { s.Hour = "05" })},
			{"minute", with(func(s *glinet.RebootSchedule) { s.Min = "59" })},
			{"day added", with(func(s *glinet.RebootSchedule) { s.Week = []int{2, 3, 4} })},
			{"day removed", with(func(s *glinet.RebootSchedule) { s.Week = []int{4} })},
		}

		steps := []resource.TestStep{{Config: config}}
		for _, tt := range tests {
			steps = append(steps, resource.TestStep{
				PreConfig:         func() { setFakeRebootSchedule(srv, tt.router) },
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectRebootScheduleState(want),
				Check:             expectRouterRebootSchedule(client, want.router()),
			})
		}

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps:                    steps,
		})
	})

	t.Run("disabled", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		client := testClient(t, srv.URL, srv.Username, srv.Password)
		want := rebootSchedule{enabled: false, time: "04:00", days: []int{2, 4}}
		config := fakeRouterConfig(srv) + want.config()
		changed := glinet.RebootSchedule{Enabled: false, Hour: "06", Min: "10", Week: []int{0}}
		enabled := glinet.RebootSchedule{Enabled: true, Hour: "06", Min: "10", Week: []int{0}}

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					PreConfig:         func() { setFakeRebootSchedule(srv, changed) },
					Config:            config,
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: expectRebootScheduleState(want),
				},
				{
					PreConfig:         func() { setFakeRebootSchedule(srv, enabled) },
					Config:            config,
					ConfigPlanChecks:  expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionUpdate),
					ConfigStateChecks: expectRebootScheduleState(want),
					Check:             expectRouterRebootSchedule(client, changed),
				},
			},
		})
	})
}

func TestRebootScheduleResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.Timer.RebootSchedule()

	tests := []struct {
		name   string
		config rebootSchedule
		error  string
	}{
		{"one digit hour", rebootSchedule{false, "3:00", []int{1}}, `time must be a time from 00:00 to 23:59`},
		{"hour 24", rebootSchedule{true, "24:00", []int{1}}, `time must be a time from 00:00 to 23:59`},
		{"minute 60", rebootSchedule{true, "03:60", []int{1}}, `time must be a time from 00:00 to 23:59`},
		{"empty", rebootSchedule{false, "", []int{1}}, `time must be a time from 00:00 to 23:59`},
		{"no days", rebootSchedule{false, "03:00", []int{}}, `days set must contain at least 1 elements`},
		{"day 7", rebootSchedule{true, "03:00", []int{7}}, `value must be between 0 and 6, got: 7`},
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

	if got := srv.Timer.RebootSchedule(); !reflect.DeepEqual(got, before) {
		t.Errorf("router reboot schedule is %+v, want %+v", got, before)
	}
}

func TestRebootScheduleResourceRouterErrors(t *testing.T) {
	parallel(t)
	s := rebootSchedule{enabled: true, time: "04:00", days: []int{1}}

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "timer", "get_reboot")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Reboot Schedule.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks:  expectResourceAction(rebootScheduleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectRebootScheduleState(s),
				},
			},
		})
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("timer", "get_reboot")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + s.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "timer", "get_reboot"),
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Reboot Schedule.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("timer", "get_reboot", get) },
					Config:           fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		before := srv.Timer.RebootSchedule()
		failRouterMethod(srv, "timer", "set_reboot")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Reboot Schedule.*Method not found`),
				},
			},
		})

		if got := srv.Timer.RebootSchedule(); !reflect.DeepEqual(got, before) {
			t.Errorf("router reboot schedule is %+v, want %+v", got, before)
		}
	})

	t.Run("set result error", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		srv.Handle("timer", "set_reboot", func(json.RawMessage) (any, error) {
			return map[string]any{"err_code": -1, "err_msg": "parameter missing"}, nil
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Reboot Schedule.*parameter missing`),
				},
			},
		})
	})

	t.Run("set on delete", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		client := testClient(t, srv.URL, srv.Username, srv.Password)
		set := srv.Handler("timer", "set_reboot")
		disabled := s.router()
		disabled.Enabled = false

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + s.config(),
				},
				{
					PreConfig:   failRouterMethod(srv, "timer", "set_reboot"),
					Config:      fakeRouterConfig(srv),
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable Reboot Schedule.*Method not found`),
				},
				{
					PreConfig: func() { srv.Handle("timer", "set_reboot", set) },
					Config:    fakeRouterConfig(srv),
					Check:     expectRouterRebootSchedule(client, disabled),
				},
			},
		})
	})
}

func TestAllowRebootScheduleDisabled(t *testing.T) {
	parallel(t)
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_reboot", `{}`, true},
		{"set_reboot", `{"enable":false}`, true},
		{"set_reboot", `{"enable":true,"hour":"03","min":"00","week":[1]}`, false},
		{"set_reboot", `{"enable":false,"hour":"03"}`, false},
		{"set_reboot", `{"enable":0}`, false},
		{"set_reboot", `{}`, false},
		{"set_led", `{"enable":false}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowRebootScheduleDisabled("timer", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("timer.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
}
