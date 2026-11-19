package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const screenScheduleAddress = "glinet_screen_schedule.test"

// screenSchedule is the configuration of a glinet_screen_schedule resource.
type screenSchedule ledSchedule

func (s screenSchedule) config() string {
	return fmt.Sprintf(`
resource "glinet_screen_schedule" "test" {
  enabled       = %t
  turn_on_time  = %q
  turn_off_time = %q
  days          = %s
}
`, s.enabled, s.on, s.off, hclNumbers(s.days))
}

// router returns the screen schedule that the router has after an apply of s.
func (s screenSchedule) router() glinettest.ScreenSchedule {
	return glinettest.ScreenSchedule{
		Enabled:     s.enabled,
		TurnOnHour:  s.on[:2],
		TurnOnMin:   s.on[3:],
		TurnOffHour: s.off[:2],
		TurnOffMin:  s.off[3:],
		Week:        s.days,
	}
}

func expectScreenScheduleState(s screenSchedule) []statecheck.StateCheck {
	expect := func(attribute string, value knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(screenScheduleAddress, tfjsonpath.New(attribute), value)
	}
	return []statecheck.StateCheck{
		expect("id", knownvalue.StringExact("screen_schedule")),
		expect("enabled", knownvalue.Bool(s.enabled)),
		expect("turn_on_time", knownvalue.StringExact(s.on)),
		expect("turn_off_time", knownvalue.StringExact(s.off)),
		expectDays(screenScheduleAddress, s.days),
	}
}

func expectRouterScreenSchedule(srv *glinettest.Server, want glinettest.ScreenSchedule) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.Timer.ScreenSchedule(); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("router screen schedule is %+v, want %+v", got, want)
		}
		return nil
	}
}

// screenRouter returns a fake router with a screen.
func screenRouter(t *testing.T) *glinettest.Server {
	srv := glinettest.NewServer(t)
	setHardwareFeature(t, srv, "screen", true)
	return srv
}

func TestScreenScheduleResource(t *testing.T) {
	parallel(t)
	srv := screenRouter(t)
	enabled := screenSchedule{enabled: true, on: "08:30", off: "21:15", days: []int{1, 3, 5}}
	disabled := screenSchedule{enabled: false, on: "06:45", off: "23:00", days: []int{0, 6}}
	writes := recordCalls(t, srv, "timer", "set_screen")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterScreenSchedule(srv, disabled.router()),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + enabled.config(),
				ConfigStateChecks: expectScreenScheduleState(enabled),
				Check: resource.ComposeTestCheckFunc(
					expectRouterScreenSchedule(srv, enabled.router()),
					writes.expect(`{"enable":true,"turnon_hour":"08","turnon_min":"30","turnoff_hour":"21","turnoff_min":"15","week":[1,3,5]}`),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + disabled.config(),
				ConfigPlanChecks:  expectResourceAction(screenScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectScreenScheduleState(disabled),
				Check: resource.ComposeTestCheckFunc(
					expectRouterScreenSchedule(srv, disabled.router()),
					writes.expect(
						`{"enable":true,"turnon_hour":"06","turnon_min":"45","turnoff_hour":"23","turnoff_min":"00","week":[0,6]}`,
						`{"enable":false}`,
					),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + disabled.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				ResourceName:      screenScheduleAddress,
				ImportState:       true,
				ImportStateId:     "screen_schedule",
				ImportStateVerify: true,
			},
		},
	})
}

func TestScreenScheduleResourceDrift(t *testing.T) {
	parallel(t)
	srv := screenRouter(t)
	want := screenSchedule{enabled: true, on: "07:00", off: "22:00", days: []int{1, 3}}
	drifted := want.router()
	drifted.TurnOffMin = "30"
	drifted.Week = []int{1}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + want.config(),
			},
			{
				PreConfig:         func() { srv.Timer.SetScreenSchedule(drifted) },
				Config:            fakeRouterConfig(srv) + want.config(),
				ConfigPlanChecks:  expectResourceAction(screenScheduleAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectScreenScheduleState(want),
				Check:             expectRouterScreenSchedule(srv, want.router()),
			},
		},
	})
}

func TestScreenScheduleResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := screenRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + screenSchedule{false, "07:00", "22:00", allWeekDays}.config(),
				ResourceName:  screenScheduleAddress,
				ImportState:   true,
				ImportStateId: "led_schedule",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestScreenScheduleResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := screenRouter(t)
	writes := recordCalls(t, srv, "timer", "set_screen")

	tests := []struct {
		name   string
		config screenSchedule
		error  string
	}{
		{"same times", screenSchedule{true, "07:00", "07:00", []int{1}}, `turn_on_time and turn_off_time must not be the same`},
		{"same times disabled", screenSchedule{false, "22:00", "22:00", []int{1}}, `turn_on_time and turn_off_time must not be the same`},
		{"invalid time", screenSchedule{true, "7:00", "22:00", []int{1}}, `turn_on_time must be a time from 00:00 to 23:59`},
		{"no days", screenSchedule{true, "07:00", "22:00", []int{}}, `days set must contain at least 1 elements`},
		{"day 7", screenSchedule{true, "07:00", "22:00", []int{7}}, `value must be between 0 and 6, got: 7`},
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

	if err := writes.expect()(nil); err != nil {
		t.Error(err)
	}
}

func TestScreenScheduleResourceNoScreen(t *testing.T) {
	parallel(t)
	config := screenSchedule{true, "07:00", "22:00", []int{1}}.config()
	t.Run("no screen", func(t *testing.T) {
		expectNoHardware(t, glinettest.NewServer(t), "screen", false, config, "timer", "set_screen")
	})
	t.Run("system info error", func(t *testing.T) {
		expectSystemInfoError(t, screenRouter(t), config, "timer", "set_screen")
	})
}

func TestScreenScheduleResourceRouterErrors(t *testing.T) {
	parallel(t)
	s := screenSchedule{enabled: false, on: "08:00", off: "21:00", days: []int{1}}

	t.Run("set", func(t *testing.T) {
		srv := screenRouter(t)
		before := srv.Timer.ScreenSchedule()
		failRouterMethod(srv, "timer", "set_screen")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set Screen Schedule.*Method not found`),
				},
			},
		})

		if err := expectRouterScreenSchedule(srv, before)(nil); err != nil {
			t.Error(err)
		}
	})

	t.Run("disable", func(t *testing.T) {
		srv := screenRouter(t)
		set := srv.Handler("timer", "set_screen")
		var failDisable atomic.Bool
		failDisable.Store(true)
		srv.Handle("timer", "set_screen", func(params json.RawMessage) (any, error) {
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
					ExpectError: regexp.MustCompile(`(?s)Unable to Disable Screen Schedule.*The router has the schedule enabled.*Method not found`),
				},
				{
					PreConfig: func() {
						if err := expectRouterScreenSchedule(srv, enabled.router())(nil); err != nil {
							t.Error(err)
						}
					},
					RefreshState:       true,
					ExpectNonEmptyPlan: true,
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(screenScheduleAddress, "enabled", "true"),
						resource.TestCheckResourceAttr(screenScheduleAddress, "turn_on_time", s.on),
						resource.TestCheckResourceAttr(screenScheduleAddress, "turn_off_time", s.off),
					),
				},
				{
					PreConfig:         func() { failDisable.Store(false) },
					Config:            fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks:  expectResourceAction(screenScheduleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectScreenScheduleState(s),
					Check:             expectRouterScreenSchedule(srv, s.router()),
				},
			},
		})
	})

	t.Run("get after set", func(t *testing.T) {
		srv := screenRouter(t)
		failFirstCall(srv, "timer", "get_screen")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + s.config(),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Screen Schedule.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + s.config(),
					ConfigPlanChecks:  expectResourceAction(screenScheduleAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectScreenScheduleState(s),
				},
			},
		})
	})
}
