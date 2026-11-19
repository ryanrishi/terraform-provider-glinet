package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// The tests of glinet_local_access always use the fake router: the write can move the router API and end SSH access.

const localAccessAddress = "glinet_local_access.test"

func localAccessConfig(srv *glinettest.Server, attributes string) string {
	return fakeRouterConfig(srv) + `
resource "glinet_local_access" "test" {
` + attributes + `
}
`
}

func expectLocalAccess(srv *glinettest.Server, want glinettest.LocalAccessConfig) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := srv.LocalAccess.Config(); got != want {
			return fmt.Errorf("local access configuration is %+v, want %+v", got, want)
		}
		return nil
	}
}

func expectLocalAccessState(c glinettest.LocalAccessConfig) []statecheck.StateCheck {
	checks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(localAccessAddress, tfjsonpath.New("id"), knownvalue.StringExact("local_access")),
	}
	for name, value := range map[string]knownvalue.Check{
		"http_port":           knownvalue.Int64Exact(int64(c.HTTPPort)),
		"https_port":          knownvalue.Int64Exact(int64(c.HTTPSPort)),
		"redirect_https":      knownvalue.Bool(c.RedirectHTTPS),
		"luci_http_port":      knownvalue.Int64Exact(int64(c.LuCIHTTPPort)),
		"luci_https_port":     knownvalue.Int64Exact(int64(c.LuCIHTTPSPort)),
		"luci_redirect_https": knownvalue.Bool(c.LuCIRedirectHTTPS),
		"session_timeout":     knownvalue.Int64Exact(int64(c.SessionTimeout)),
		"ssh_enabled":         knownvalue.Bool(c.SSHEnabled),
		"ssh_port":            knownvalue.Int64Exact(int64(c.SSHPort)),
	} {
		checks = append(checks, statecheck.ExpectKnownValue(localAccessAddress, tfjsonpath.New(name), value))
	}
	return checks
}

// TestLocalAccessResource sets some attributes and checks that each write sends all keys, with the router values of
// the attributes that are not set. Destroy does not change the router.
func TestLocalAccessResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "local-access", "set_config")
	first := srv.LocalAccess.Config()
	first.SessionTimeout = 1800
	first.SSHPort = 2222
	second := first
	second.LuCIRedirectHTTPS = true
	second.SSHEnabled = false

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(calls.expect(), expectLocalAccess(srv, second)),
		Steps: []resource.TestStep{
			{
				Config: localAccessConfig(srv, `
  session_timeout = 1800
  ssh_port        = 2222
`),
				ConfigStateChecks: expectLocalAccessState(first),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"http_port":80,"https_port":443,"redirect_https":false,"luci_http_port":8080,"luci_https_port":8443,`+
						`"luci_redirect_https":false,"session_timeout":1800,"ssh_enabled":true,"ssh_port":2222}`),
					expectLocalAccess(srv, first),
				),
			},
			{
				ResourceName:      localAccessAddress,
				ImportState:       true,
				ImportStateId:     "local_access",
				ImportStateVerify: true,
			},
			{
				Config: localAccessConfig(srv, `
  session_timeout     = 1800
  ssh_port            = 2222
  luci_redirect_https = true
  ssh_enabled         = false
`),
				ConfigStateChecks: expectLocalAccessState(second),
				Check: calls.expect(`{"http_port":80,"https_port":443,"redirect_https":false,"luci_http_port":8080,"luci_https_port":8443,` +
					`"luci_redirect_https":true,"session_timeout":1800,"ssh_enabled":false,"ssh_port":2222}`),
			},
		},
	})
}

// TestLocalAccessResourceEndpointMoves fails the first read after a write that changes http_port, or gives it no
// answer, as when the router API has moved. The apply keeps the written values in the state, does not fail, and ends
// soon after movedEndpointReadTimeout.
func TestLocalAccessResourceEndpointMoves(t *testing.T) {
	parallel(t)
	for name, noAnswer := range map[string]bool{"error": false, "no answer": true} {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			done := make(chan struct{})
			t.Cleanup(func() { close(done) })
			var written, failed atomic.Bool
			set := srv.Handler("local-access", "set_config")
			srv.Handle("local-access", "set_config", func(params json.RawMessage) (any, error) {
				written.Store(true)
				return set(params)
			})
			get := srv.Handler("local-access", "get_config")
			srv.Handle("local-access", "get_config", func(params json.RawMessage) (any, error) {
				if written.Load() && !failed.Swap(true) {
					if noAnswer {
						select {
						case <-done:
						case <-time.After(5 * time.Minute):
						}
					}
					return nil, &glinettest.Error{Code: -32603, Message: "Internal error"}
				}
				return get(params)
			})
			want := srv.LocalAccess.Config()
			want.HTTPPort = 8081
			start := time.Now()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:            localAccessConfig(srv, `  http_port = 8081`),
					ConfigStateChecks: expectLocalAccessState(want),
					Check: resource.ComposeTestCheckFunc(
						expectLocalAccess(srv, want),
						func(*terraform.State) error {
							if !failed.Load() {
								return fmt.Errorf("the read after the write did not fail")
							}
							if elapsed := time.Since(start); elapsed > movedEndpointReadTimeout+time.Minute {
								return fmt.Errorf("the apply took %s", elapsed)
							}
							return nil
						},
					),
				}},
			})
		})
	}
}

// TestLocalAccessResourceReadErrorAfterWrite fails the read after a write that does not move the router API.
func TestLocalAccessResourceReadErrorAfterWrite(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	var written atomic.Bool
	set := srv.Handler("local-access", "set_config")
	srv.Handle("local-access", "set_config", func(params json.RawMessage) (any, error) {
		written.Store(true)
		return set(params)
	})
	get := srv.Handler("local-access", "get_config")
	srv.Handle("local-access", "get_config", func(params json.RawMessage) (any, error) {
		if written.Load() {
			return nil, &glinettest.Error{Code: -32602, Message: "Invalid params"}
		}
		return get(params)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      localAccessConfig(srv, `  ssh_port = 2222`),
			ExpectError: regexp.MustCompile(`Unable to Read Local Access Configuration`),
		}},
	})
}

// TestLocalAccessResourceFirmware470 reads a configuration without ssh_enabled, as firmware 4.7.0 gives it.
// When ssh_enabled is not set, the plan has no difference.
func TestLocalAccessResourceFirmware470(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("local-access", "get_config", func(json.RawMessage) (any, error) {
		c := srv.LocalAccess.Config()
		return map[string]any{
			"http_port":           c.HTTPPort,
			"https_port":          c.HTTPSPort,
			"redirect_https":      c.RedirectHTTPS,
			"luci_http_port":      c.LuCIHTTPPort,
			"luci_https_port":     c.LuCIHTTPSPort,
			"luci_redirect_https": c.LuCIRedirectHTTPS,
			"session_timeout":     c.SessionTimeout,
			"ssh_port":            c.SSHPort,
		}, nil
	})
	calls := recordCalls(t, srv, "local-access", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: localAccessConfig(srv, `  session_timeout = 600`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(localAccessAddress, tfjsonpath.New("ssh_enabled"), knownvalue.Bool(false)),
				},
				Check: calls.expect(`{"http_port":80,"https_port":443,"redirect_https":false,"luci_http_port":8080,"luci_https_port":8443,` +
					`"luci_redirect_https":false,"session_timeout":600,"ssh_enabled":false,"ssh_port":22}`),
			},
			{
				Config:           localAccessConfig(srv, `  session_timeout = 600`),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestLocalAccessResourceRouterErrors(t *testing.T) {
	parallel(t)
	for name, tc := range map[string]struct {
		code    int
		message string
		err     string
	}{
		"http port":       {-1, "port is in use", `The HTTP port is in use`},
		"https port":      {-2, "port is in use", `The HTTPS port is in use`},
		"ssh port":        {-3, "ssh port is in use", `The SSH port is in use`},
		"luci https port": {-3, "luci https port is in use", `The LuCI HTTPS port is in use`},
		"luci http port":  {-3, "luci http port is in use", `The LuCI HTTP port is in use`},
		"weak password":   {-4, "password is too weak", `admin password is too weak`},
		"other":           {-5, "failed", `rpc error -5: failed`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Handle("local-access", "set_config", func(json.RawMessage) (any, error) {
				return nil, &glinettest.Error{Code: tc.code, Message: tc.message}
			})

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      localAccessConfig(srv, `  ssh_port = 2222`),
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

func TestLocalAccessResourceInvalidConfig(t *testing.T) {
	parallel(t)
	for name, tc := range map[string]struct {
		attributes string
		err        string
	}{
		"port out of range":    {`  ssh_port = 65536`, `must be between 1 and 65535`},
		"browser blocked port": {`  https_port = 6000`, `value must be none of`},
		"session timeout step": {`  session_timeout = 90`, `multiple of 60`},
		"session timeout max":  {`  session_timeout = 10860`, `must be between 60 and 10800`},
		"same ports": {`
  http_port = 8081
  ssh_port  = 8081
`, `Port Conflict`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      localAccessConfig(glinettest.NewServer(t), tc.attributes),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

// TestLocalAccessResourcePortConflictWithRouter checks a configured port against the router values of the ports that
// are not set, and sends no write.
func TestLocalAccessResourcePortConflictWithRouter(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "local-access", "set_config")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      localAccessConfig(srv, `  ssh_port = 8080`),
			ExpectError: regexp.MustCompile(`(?s)Port Conflict.*luci_http_port`),
		}},
	})
	if got := calls.take(); len(got) != 0 {
		t.Errorf("set_config calls: %q", got)
	}
}

func TestLocalAccessResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:        localAccessConfig(srv, ""),
			ResourceName:  localAccessAddress,
			ImportState:   true,
			ImportStateId: "security",
			ExpectError:   regexp.MustCompile(`Invalid Import ID`),
		}},
	})
}
