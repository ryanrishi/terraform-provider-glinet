package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"glinet": providerserver.NewProtocol6WithError(New("test")()),
}

// realRouter reports whether tests run against the router at GLINET_HOST instead of a fake router.
// The provider then reads GLINET_HOST, GLINET_USERNAME and GLINET_PASSWORD from the environment.
func realRouter() bool {
	return os.Getenv(resource.EnvTfAcc) != "" && os.Getenv("GLINET_HOST") != ""
}

func providerConfig(host, username, password string) string {
	return fmt.Sprintf(`
provider "glinet" {
  host     = %q
  username = %q
  password = %q
}
`, host, username, password)
}

func fakeRouterConfig(srv *glinettest.Server) string {
	return providerConfig(srv.URL, srv.Username, srv.Password)
}

// guardedRealRouter returns the provider block for a proxy to the router at GLINET_HOST, and a client that uses the proxy.
// The proxy sends challenge and login to the router, and a call only when allow returns nil for its module, method and params.
// It answers each other request with HTTP status 403 and fails the test, so that the router does not get it.
func guardedRealRouter(t *testing.T, allow func(module, method string, params map[string]json.RawMessage) error) (string, *glinet.Client) {
	host := strings.TrimSuffix(os.Getenv("GLINET_HOST"), "/")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := checkRequest(body, allow); err != nil {
			t.Errorf("request blocked: %v", err)
			http.Error(w, "request blocked by the test", http.StatusForbidden)
			return
		}

		resp, err := http.Post(host+r.URL.RequestURI(), r.Header.Get("Content-Type"), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)

	provider := fmt.Sprintf(`
provider "glinet" {
  host = %q
}
`, proxy.URL)
	return provider, testClient(t, proxy.URL, os.Getenv("GLINET_USERNAME"), os.Getenv("GLINET_PASSWORD"))
}

func checkRequest(body []byte, allow func(module, method string, params map[string]json.RawMessage) error) error {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return errors.New("request is not a JSON-RPC request")
	}
	switch req.Method {
	case "challenge", "login":
		return nil
	case "call":
	default:
		return fmt.Errorf("JSON-RPC method %q", req.Method)
	}

	var call []json.RawMessage
	var module, method string
	if json.Unmarshal(req.Params, &call) != nil || len(call) < 3 ||
		json.Unmarshal(call[1], &module) != nil || json.Unmarshal(call[2], &method) != nil {
		return errors.New("call without module and method")
	}
	params := map[string]json.RawMessage{}
	if len(call) > 3 {
		if err := json.Unmarshal(call[3], &params); err != nil {
			return fmt.Errorf("%s.%s: params are not an object", module, method)
		}
	}
	return allow(module, method, params)
}

func expectResourceAction(address string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(address, action),
		},
	}
}

var expectEmptyPlan = resource.ConfigPlanChecks{
	PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
}

// failRouterMethod returns a function that makes each call of module.method on srv return "Method not found".
func failRouterMethod(srv *glinettest.Server, module, method string) func() {
	return func() {
		srv.Handle(module, method, func(json.RawMessage) (any, error) {
			return nil, glinettest.ErrMethodNotFound
		})
	}
}

// failFirstCall makes the first call of module.method on srv return "Method not found".
// The later calls go to the method that served it before.
func failFirstCall(srv *glinettest.Server, module, method string) {
	next := srv.Handler(module, method)
	var failed atomic.Bool
	srv.Handle(module, method, func(params json.RawMessage) (any, error) {
		if !failed.Swap(true) {
			return nil, glinettest.ErrMethodNotFound
		}
		return next(params)
	})
}

// Checks for the real-router tests, which cannot know the values.
var (
	anyString = knownvalue.StringFunc(func(string) error { return nil })
	anyBool   = knownvalue.BoolFunc(func(bool) error { return nil })
	anyInt64  = knownvalue.Int64Func(func(int64) error { return nil })

	nonEmptyString = knownvalue.StringFunc(func(v string) error {
		if v == "" {
			return errors.New("value is empty")
		}
		return nil
	})
	positiveInt64 = knownvalue.Int64Func(func(v int64) error {
		if v <= 0 {
			return errors.New("value is not positive")
		}
		return nil
	})
)

// expectAttributes checks the top-level attributes of the data source or resource at address.
func expectAttributes(address string, checks map[string]knownvalue.Check) []statecheck.StateCheck {
	var stateChecks []statecheck.StateCheck
	for name, check := range checks {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(address, tfjsonpath.New(name), check))
	}
	return stateChecks
}

// readDataSource applies config and runs checks on the state.
func readDataSource(t *testing.T, config string, checks []statecheck.StateCheck) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: checks,
			},
		},
	})
}

// expectReadError makes module.method fail on srv and expects config to fail with an error that matches summary and "Method not found".
func expectReadError(t *testing.T, srv *glinettest.Server, config, module, method, summary string) {
	failRouterMethod(srv, module, method)()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + config,
				ExpectError: regexp.MustCompile(`(?s)` + regexp.QuoteMeta(summary) + `.*Method not found`),
			},
		},
	})
}

const timezoneConfigDataSourceConfig = `
data "glinet_system_timezone_config" "test" {}
`

func TestProviderWrongPassword(t *testing.T) {
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(srv.URL, srv.Username, "wrong") + timezoneConfigDataSourceConfig,
				ExpectError: regexp.MustCompile(`(?s)Unable to Create GL\.iNet API Client.*Access denied`),
			},
		},
	})
}

func TestProviderMissingPassword(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_PASSWORD", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "glinet" {
  host = %q
}
`, srv.URL) + timezoneConfigDataSourceConfig,
				ExpectError: regexp.MustCompile(`Missing GL\.iNet API Password`),
			},
		},
	})
}

func readsTimezoneConfig(config string) resource.TestCase {
	return resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config + timezoneConfigDataSourceConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expectTimezoneConfig("id", knownvalue.NotNull()),
				},
			},
		},
	}
}

func TestProviderConfigFromEnvironment(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_HOST", srv.URL)
	t.Setenv("GLINET_USERNAME", srv.Username)
	t.Setenv("GLINET_PASSWORD", srv.Password)

	resource.UnitTest(t, readsTimezoneConfig(`provider "glinet" {}`))
}

func TestProviderConfigOverridesEnvironment(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_HOST", "http://127.0.0.1:1")
	t.Setenv("GLINET_USERNAME", "not-"+srv.Username)
	t.Setenv("GLINET_PASSWORD", "not-"+srv.Password)

	resource.UnitTest(t, readsTimezoneConfig(fakeRouterConfig(srv)))
}

func TestProviderDefaultUsername(t *testing.T) {
	srv := glinettest.NewServer(t)
	srv.Username = defaultUsername
	t.Setenv("GLINET_USERNAME", "")

	resource.UnitTest(t, readsTimezoneConfig(fmt.Sprintf(`
provider "glinet" {
  host     = %q
  password = %q
}
`, srv.URL, srv.Password)))
}

func TestValueOrEnv(t *testing.T) {
	const envVar = "GLINET_TEST_VALUE"

	tests := []struct {
		name  string
		value types.String
		env   string
		want  string
	}{
		{"config value", types.StringValue("config"), "env", "config"},
		{"empty config value", types.StringValue(""), "env", "env"},
		{"environment", types.StringNull(), "env", "env"},
		{"fallback", types.StringNull(), "", "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envVar, tt.env)
			if got := valueOrEnv(tt.value, envVar, "fallback"); got != tt.want {
				t.Errorf("valueOrEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGuardedRealRouter(t *testing.T) {
	srv := glinettest.NewServer(t)
	t.Setenv("GLINET_HOST", srv.URL)
	t.Setenv("GLINET_USERNAME", srv.Username)
	t.Setenv("GLINET_PASSWORD", srv.Password)

	_, client := guardedRealRouter(t, allowRebootScheduleDisabled)
	ctx := context.Background()
	if err := client.Timer.SetRebootSchedule(ctx, glinet.RebootSchedule{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Timer.GetRebootSchedule(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRequest(t *testing.T) {
	tests := []struct {
		body  string
		allow bool
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"challenge","params":{"username":"root"}}`, true},
		{`{"jsonrpc":"2.0","id":2,"method":"login","params":{"username":"root","hash":"x"}}`, true},
		{`{"jsonrpc":"2.0","id":3,"method":"call","params":["sid","timer","get_reboot",{}]}`, true},
		{`{"jsonrpc":"2.0","id":3,"method":"call","params":["sid","timer","get_reboot"]}`, true},
		{`{"jsonrpc":"2.0","id":4,"method":"call","params":["sid","timer","set_reboot",{"enable":false}]}`, true},
		{`{"jsonrpc":"2.0","id":5,"method":"call","params":["sid","timer","set_reboot",{"enable":true,"hour":"03","min":"00","week":[1]}]}`, false},
		{`{"jsonrpc":"2.0","id":6,"method":"call","params":["sid","timer","set_reboot","enable"]}`, false},
		{`{"jsonrpc":"2.0","id":7,"method":"call","params":["sid","timer"]}`, false},
		{`{"jsonrpc":"2.0","id":8,"method":"call","params":{"module":"timer"}}`, false},
		{`{"jsonrpc":"2.0","id":9,"method":"logout","params":{}}`, false},
		{`not json`, false},
	}

	for _, tt := range tests {
		if err := checkRequest([]byte(tt.body), allowRebootScheduleDisabled); (err == nil) != tt.allow {
			t.Errorf("%s: error %v, want allowed %t", tt.body, err, tt.allow)
		}
	}
}
