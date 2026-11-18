package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const timezoneAddress = "glinet_system_timezone.test"

func timezoneConfig(zonename, timezone string) string {
	return fmt.Sprintf(`
resource "glinet_system_timezone" "test" {
  zonename = %q
  timezone = %q
}
`, zonename, timezone)
}

func expectTimezone(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(timezoneAddress, tfjsonpath.New(attribute), value)
}

// timezoneTestRouter returns the provider block and a client for the router of the test.
// On the real router, the time zone configuration goes back to its initial value when the test ends.
func timezoneTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	client := testClient(t, os.Getenv("GLINET_HOST"), os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
	initial, err := client.System.GetTimezoneConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()

	t.Cleanup(func() {
		ctx := context.Background()
		err := client.System.SetTimezoneConfig(ctx, &glinet.SetSystemTimezoneRequest{
			Zonename: initial.Zonename,
			Timezone: initial.Timezone,
		})
		if err != nil {
			t.Fatalf("restore time zone: %v", err)
		}

		restored, err := client.System.GetTimezoneConfig(ctx)
		if err != nil {
			t.Fatalf("read restored time zone: %v", err)
		}
		if restored.Zonename != initial.Zonename || restored.Timezone != initial.Timezone {
			t.Errorf("restored time zone is %q %q, want %q %q", restored.Zonename, restored.Timezone, initial.Zonename, initial.Timezone)
		}
		routerElapsed := time.Duration(restored.Timestamp-initial.Timestamp) * time.Second
		localElapsed := time.Since(start)
		if drift := (routerElapsed - localElapsed).Abs(); drift > 10*time.Second {
			t.Errorf("router clock moved %s while local clock moved %s", routerElapsed, localElapsed)
		}
	})

	return `provider "glinet" {}`, client
}

func testClient(t *testing.T, host, username, password string) *glinet.Client {
	client, err := glinet.NewClientWithHost(host, username, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func expectRouterZonename(client *glinet.Client, zonename string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.System.GetTimezoneConfig(context.Background())
		if err != nil {
			return err
		}
		if config.Zonename != zonename {
			return fmt.Errorf("router zonename is %q, want %q", config.Zonename, zonename)
		}
		return nil
	}
}

func TestSystemTimezoneResource(t *testing.T) {
	parallel(t)
	provider, client := timezoneTestRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterZonename(client, "Asia/Kolkata"),
		Steps: []resource.TestStep{
			{
				Config: provider + timezoneConfig("Asia/Tokyo", "JST-9"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezone("id", knownvalue.StringExact("system_timezone")),
					expectTimezone("zonename", knownvalue.StringExact("Asia/Tokyo")),
					expectTimezone("timezone", knownvalue.StringExact("JST-9")),
					expectTimezone("tzoffset", knownvalue.StringExact("+0900")),
					expectTimezone("auto_timezone_enabled", knownvalue.NotNull()),
				},
				Check: expectRouterZonename(client, "Asia/Tokyo"),
			},
			{
				Config: provider + timezoneConfig("Asia/Kolkata", "IST-5:30"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(timezoneAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectUnknownValue(timezoneAddress, tfjsonpath.New("tzoffset")),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezone("zonename", knownvalue.StringExact("Asia/Kolkata")),
					expectTimezone("timezone", knownvalue.StringExact("IST-5:30")),
					expectTimezone("tzoffset", knownvalue.StringExact("+0530")),
				},
				Check: expectRouterZonename(client, "Asia/Kolkata"),
			},
			{
				ResourceName:      timezoneAddress,
				ImportState:       true,
				ImportStateId:     "system_timezone",
				ImportStateVerify: true,
			},
		},
	})
}

func TestSystemTimezoneResourceZonenameWithSpace(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + timezoneConfig("America/New York", "EST5EDT,M3.2.0,M11.1.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezone("zonename", knownvalue.StringExact("America/New York")),
					expectTimezone("tzoffset", knownvalue.StringExact("-0500")),
				},
			},
		},
	})

	if got := srv.System.TimezoneConfig().Zonename; got != "America/New York" {
		t.Errorf("router zonename is %q, want %q", got, "America/New York")
	}
}

func TestSystemTimezoneResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + timezoneConfig("Asia/Tokyo", "JST-9")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					srv.System.SetTimezoneConfig(glinettest.TimezoneConfig{
						Zonename:            "UTC",
						Timezone:            "UTC0",
						TZOffset:            "+0000",
						AutoTimezoneEnabled: false,
					})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(timezoneAddress, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezone("zonename", knownvalue.StringExact("Asia/Tokyo")),
					expectTimezone("tzoffset", knownvalue.StringExact("+0900")),
					expectTimezone("auto_timezone_enabled", knownvalue.Bool(false)),
				},
			},
		},
	})
}

// TestSystemTimezoneResourceTimestampUnchanged proves that create and update do not send localtime,
// because the fake router, like the router, sets its clock from localtime.
func TestSystemTimezoneResourceTimestampUnchanged(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.System.TimezoneConfig().Timestamp
	expectTimestampUnchanged := func(*terraform.State) error {
		if after := srv.System.TimezoneConfig().Timestamp; after != before {
			return fmt.Errorf("router timestamp changed from %d to %d", before, after)
		}
		return nil
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + timezoneConfig("Asia/Tokyo", "JST-9"),
				Check:  expectTimestampUnchanged,
			},
			{
				Config: fakeRouterConfig(srv) + timezoneConfig("Asia/Kolkata", "IST-5:30"),
				Check:  expectTimestampUnchanged,
			},
		},
	})
}

func TestSystemTimezoneResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + timezoneConfig("UTC", "UTC0"),
				ResourceName:  timezoneAddress,
				ImportState:   true,
				ImportStateId: "something-else",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestSystemTimezoneResourceSetError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("system", "set_timezone_config", func(json.RawMessage) (any, error) {
		return nil, glinettest.ErrInvalidParams
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + timezoneConfig("Asia/Tokyo", "JST-9"),
				ExpectError: regexp.MustCompile(`(?s)Unable to Set System Timezone.*Invalid params`),
			},
		},
	})
}

func TestSystemTimezoneResourceReadError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("system", "get_timezone_config", func(json.RawMessage) (any, error) {
		return nil, glinettest.ErrMethodNotFound
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + timezoneConfig("Asia/Tokyo", "JST-9"),
				ExpectError: regexp.MustCompile(`(?s)Unable to Read System Timezone.*Method not found`),
			},
		},
	})
}
