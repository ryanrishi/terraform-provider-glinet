package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

const parentalControlAddress = "glinet_parental_control.test"

func expectParentalControl(attribute string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(parentalControlAddress, tfjsonpath.New(attribute), value)
}

// editParentalControl changes the result of parental-control get_config of srv.
func editParentalControl(t *testing.T, srv *glinettest.Server, edit func(config *glinet.ParentalControlConfig)) {
	t.Helper()
	var config glinet.ParentalControlConfig
	if err := json.Unmarshal(srv.ParentalControl.Config(), &config); err != nil {
		t.Fatal(err)
	}
	edit(&config)
	b, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	srv.ParentalControl.SetConfig(b)
}

func expectRouterParentalControl(client *glinet.Client, want glinet.ParentalControlSettings) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.ParentalControl.GetConfig(context.Background())
		if err != nil {
			return err
		}
		got := glinet.ParentalControlSettings{Enable: config.Enable, DropAnonymous: config.DropAnonymous, AutoUpdate: config.AutoUpdate}
		if got != want {
			return fmt.Errorf("router has %+v, want %+v", got, want)
		}
		return nil
	}
}

func TestParentalControlResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)
	writes := recordCalls(t, srv, "parental-control", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterParentalControl(client, glinet.ParentalControlSettings{AutoUpdate: true}),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled        = true
  drop_anonymous = false
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expectParentalControl("id", knownvalue.StringExact("parental_control")),
					expectParentalControl("enabled", knownvalue.Bool(true)),
					expectParentalControl("drop_anonymous", knownvalue.Bool(false)),
					expectParentalControl("auto_update", knownvalue.Bool(false)),
					expectParentalControl("app_filter_enabled", knownvalue.Bool(false)),
				},
				Check: writes.expect(`{"enable":true,"drop_anonymous":false,"auto_update":false}`),
			},
			{
				PreConfig: func() {
					editParentalControl(t, srv, func(config *glinet.ParentalControlConfig) { config.AutoUpdate = true })
				},
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled = false
}
`,
				ConfigPlanChecks: expectResourceAction(parentalControlAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectParentalControl("enabled", knownvalue.Bool(false)),
					expectParentalControl("drop_anonymous", knownvalue.Bool(false)),
					expectParentalControl("auto_update", knownvalue.Bool(true)),
				},
				Check: writes.expect(`{"enable":false,"drop_anonymous":false,"auto_update":true}`),
			},
			{
				ResourceName:      parentalControlAddress,
				ImportState:       true,
				ImportStateId:     "parental_control",
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					editParentalControl(t, srv, func(config *glinet.ParentalControlConfig) { config.Enable = true })
				},
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled = false
}
`,
				ConfigPlanChecks: expectResourceAction(parentalControlAddress, plancheck.ResourceActionUpdate),
				Check:            writes.expect(`{"enable":false,"drop_anonymous":false,"auto_update":true}`),
			},
		},
	})
}

func TestParentalControlResourceDestroyKeepsSettings(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	client := testClient(t, srv.URL, srv.Username, srv.Password)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterParentalControl(client, glinet.ParentalControlSettings{DropAnonymous: true}),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled        = true
  drop_anonymous = true
}
`,
				Check: expectRouterParentalControl(client, glinet.ParentalControlSettings{Enable: true, DropAnonymous: true}),
			},
		},
	})
}

func TestParentalControlResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled = false
}
`,
				ResourceName:  parentalControlAddress,
				ImportState:   true,
				ImportStateId: "other",
				ExpectError:   regexp.MustCompile(`import ID of glinet_parental_control must be "parental_control"`),
			},
		},
	})
}

func TestParentalControlResourceAPIError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failRouterMethod(srv, "parental-control", "set_config")()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + `
resource "glinet_parental_control" "test" {
  enabled = true
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to Set Parental Control Configuration.*Method not found`),
			},
		},
	})
}
