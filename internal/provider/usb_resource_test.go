package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const usbAddress = "glinet_usb.test"

func usbConfig(usb3Enabled bool) string {
	return fmt.Sprintf(`
resource "glinet_usb" "test" {
  usb3_enabled = %t
}
`, usb3Enabled)
}

func expectUSBState(usb3Enabled bool) []statecheck.StateCheck {
	return expectAttributes(usbAddress, map[string]knownvalue.Check{
		"id":           knownvalue.StringExact("usb"),
		"usb3_enabled": knownvalue.Bool(usb3Enabled),
	})
}

func expectRouterUSB3Disabled(srv *glinettest.Server, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.System.USB3Disabled(); got != want {
			return fmt.Errorf("router usb3_disable is %t, want %t", got, want)
		}
		return nil
	}
}

func TestUSBResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	writes := recordCalls(t, srv, "system", "set_usb3_disable")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterUSB3Disabled(srv, false),
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + usbConfig(false),
				ConfigStateChecks: expectUSBState(false),
				Check: resource.ComposeTestCheckFunc(
					expectRouterUSB3Disabled(srv, true),
					writes.expect(`{"usb3_disable":true}`),
				),
			},
			{
				Config:            fakeRouterConfig(srv) + usbConfig(true),
				ConfigPlanChecks:  expectResourceAction(usbAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectUSBState(true),
				Check: resource.ComposeTestCheckFunc(
					expectRouterUSB3Disabled(srv, false),
					writes.expect(`{"usb3_disable":false}`),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + usbConfig(true),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				ResourceName:      usbAddress,
				ImportState:       true,
				ImportStateId:     "usb",
				ImportStateVerify: true,
			},
		},
	})
}

func TestUSBResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + usbConfig(true),
			},
			{
				PreConfig:         func() { srv.System.SetUSB3Disabled(true) },
				Config:            fakeRouterConfig(srv) + usbConfig(true),
				ConfigPlanChecks:  expectResourceAction(usbAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectUSBState(true),
				Check:             expectRouterUSB3Disabled(srv, false),
			},
		},
	})
}

// TestUSBResourceNoValue reads a result without usb3_disable as USB 2.0, as the web UI does.
func TestUSBResourceNoValue(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("system", "get_usb3_disable", func(json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             fakeRouterConfig(srv) + usbConfig(false),
				ResourceName:       usbAddress,
				ImportState:        true,
				ImportStateId:      "usb",
				ImportStatePersist: true,
			},
			{
				Config:            fakeRouterConfig(srv) + usbConfig(false),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectUSBState(false),
			},
		},
	})
}

func TestUSBResourceImportInvalidID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + usbConfig(true),
				ResourceName:  usbAddress,
				ImportState:   true,
				ImportStateId: "usb3",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestUSBResourceNoUSB3(t *testing.T) {
	parallel(t)
	t.Run("no USB 3.0 port", func(t *testing.T) {
		expectNoHardware(t, glinettest.NewServer(t), "usb3", "", usbConfig(false), "system", "set_usb3_disable")
	})
	t.Run("system info error", func(t *testing.T) {
		expectSystemInfoError(t, glinettest.NewServer(t), usbConfig(false), "system", "set_usb3_disable")
	})
}

func TestUSBResourceRouterErrors(t *testing.T) {
	parallel(t)

	t.Run("set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failRouterMethod(srv, "system", "set_usb3_disable")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + usbConfig(false),
					ExpectError: regexp.MustCompile(`(?s)Unable to Set USB Configuration.*Method not found`),
				},
			},
		})

		if srv.System.USB3Disabled() {
			t.Error("router usb3_disable changed")
		}
	})

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		failFirstCall(srv, "system", "get_usb3_disable")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + usbConfig(false),
					ExpectError: regexp.MustCompile(`(?s)Unable to Read USB Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + usbConfig(false),
					ConfigPlanChecks:  expectResourceAction(usbAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: expectUSBState(false),
				},
			},
		})
	})
}
