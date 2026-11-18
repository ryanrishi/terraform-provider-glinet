package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sync/atomic"
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

const (
	blockAddress       = "glinet_client_block.test"
	blockMAC           = "02:00:00:00:00:a1"
	blockMACUpper      = "02:00:00:00:00:A1"
	otherBlockMAC      = "02:00:00:00:00:a2"
	otherBlockMACUpper = "02:00:00:00:00:A2"
	unrelatedBlockMAC  = "02:00:00:00:00:B9"
)

func blockConfig(mac string) string {
	return fmt.Sprintf(`
resource "glinet_client_block" "test" {
  mac = %q
}
`, mac)
}

func expectBlockMAC(mac string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(blockAddress, tfjsonpath.New("mac"), knownvalue.StringExact(mac)),
	}
}

// allowTestMACBlock allows only the read of the access control configuration and the block-list writes of
// the test MAC address, so that the test cannot change the mode or block a real client of the real router.
func allowTestMACBlock(module, method string, params map[string]json.RawMessage) error {
	if module == "black_white_list" && method == "get_config" {
		return nil
	}
	if module == "black_white_list" && method == "set_single_mac" && len(params) == 3 && string(params["mode"]) == `"black"` {
		operate, mac := string(params["operate"]), string(params["mac"])
		if (operate == `"add"` && mac == `"`+blockMACUpper+`"`) ||
			(operate == `"del"` && (mac == `"`+blockMACUpper+`"` || mac == `"`+blockMAC+`"`)) {
			return nil
		}
	}
	return fmt.Errorf("%s.%s with params %v is not allowed", module, method, params)
}

// blockTestRouter returns the provider block and a client for the router of the test.
// The real router is used only through guardedRealRouter with allowTestMACBlock, and only when it is in block-list mode
// and its block list does not have the test MAC address. When the test ends, the test MAC address is removed in both
// letter cases, and the configuration must be as before.
func blockTestRouter(t *testing.T) (string, *glinet.Client) {
	if !realRouter() {
		srv := glinettest.NewServer(t)
		return fakeRouterConfig(srv), testClient(t, srv.URL, srv.Username, srv.Password)
	}

	provider, client := guardedRealRouter(t, allowTestMACBlock)
	initial, err := client.AccessControl.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != blockListMode {
		t.Fatalf("the access control mode of the router is %q; the test runs only in mode %q", initial.Mode, blockListMode)
	}
	if entries := matchingMACs(initial.BlackMAC, blockMAC); len(entries) > 0 {
		t.Fatalf("the block list of the router has the test MAC address %v", entries)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, mac := range []string{blockMACUpper, blockMAC} {
			if err := client.AccessControl.RemoveBlockedMAC(ctx, mac); err != nil {
				t.Errorf("remove %s: %v", mac, err)
			}
		}

		restored, err := client.AccessControl.GetConfig(ctx)
		if err != nil {
			t.Fatalf("read access control configuration: %v", err)
		}
		if !reflect.DeepEqual(*restored, *initial) {
			t.Errorf("access control configuration is %+v, want %+v", *restored, *initial)
		}
	})

	return provider, client
}

// expectRouterBlock checks that the block list entries for mac, in any letter case, are want.
func expectRouterBlock(client *glinet.Client, mac string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := client.AccessControl.GetConfig(context.Background())
		if err != nil {
			return err
		}
		if got := matchingMACs(config.BlackMAC, mac); !slices.Equal(got, want) {
			return fmt.Errorf("block list entries for %s are %v, want %v", mac, got, want)
		}
		return nil
	}
}

func expectBlockList(t *testing.T, srv *glinettest.Server, want ...string) {
	t.Helper()
	if got := srv.AccessControl.Config().BlackMAC; !slices.Equal(got, want) {
		t.Errorf("block list is %v, want %v", got, want)
	}
}

func setBlockList(srv *glinettest.Server, mode string, macs ...string) {
	srv.AccessControl.SetConfig(glinettest.AccessControlConfig{Mode: mode, BlackMAC: macs})
}

// failWritesOutsideBlockMode makes each block-list write fail the test when the fake router is not in mode "black",
// so that such a write does not reach the router.
func failWritesOutsideBlockMode(t *testing.T, srv *glinettest.Server) {
	for _, method := range []string{"set_single_mac", "set_config"} {
		next := srv.Handler("black_white_list", method)
		srv.Handle("black_white_list", method, func(params json.RawMessage) (any, error) {
			if mode := srv.AccessControl.Config().Mode; mode != blockListMode {
				t.Errorf("black_white_list.%s %s in mode %q", method, params, mode)
				return nil, glinettest.ErrMethodNotFound
			}
			return next(params)
		})
	}
}

func TestClientBlockResource(t *testing.T) {
	parallel(t)
	provider, client := blockTestRouter(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             expectRouterBlock(client, blockMAC),
		Steps: []resource.TestStep{
			{
				Config:            provider + blockConfig(blockMAC),
				ConfigStateChecks: expectBlockMAC(blockMAC),
				Check:             expectRouterBlock(client, blockMAC, blockMACUpper),
			},
			{
				ResourceName:                         blockAddress,
				ImportState:                          true,
				ImportStateId:                        blockMAC,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "mac",
			},
			{
				Config:           provider + blockConfig(blockMAC),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestClientBlockResourceReplace(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setBlockList(srv, blockListMode, unrelatedBlockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + blockConfig(blockMAC),
			},
			{
				Config:            fakeRouterConfig(srv) + blockConfig(otherBlockMAC),
				ConfigPlanChecks:  expectResourceAction(blockAddress, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectBlockMAC(otherBlockMAC),
				Check: func(*terraform.State) error {
					expectBlockList(t, srv, unrelatedBlockMAC, otherBlockMACUpper)
					return nil
				},
			},
		},
	})

	expectBlockList(t, srv, unrelatedBlockMAC)
}

func TestClientBlockResourceImport(t *testing.T) {
	parallel(t)
	for _, entry := range []string{blockMAC, blockMACUpper} {
		for _, id := range []string{blockMAC, blockMACUpper} {
			t.Run(entry+" as "+id, func(t *testing.T) {
				srv := glinettest.NewServer(t)
				setBlockList(srv, blockListMode, unrelatedBlockMAC, entry)
				config := fakeRouterConfig(srv) + blockConfig(blockMAC)

				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: testProtoV6ProviderFactories,
					Steps: []resource.TestStep{
						{
							Config:             config,
							ResourceName:       blockAddress,
							ImportState:        true,
							ImportStateId:      id,
							ImportStatePersist: true,
							ImportStateCheck: func(states []*terraform.InstanceState) error {
								if len(states) != 1 || states[0].Attributes["mac"] != blockMAC {
									return fmt.Errorf("imported states are %v, want one with mac %s", states, blockMAC)
								}
								return nil
							},
						},
						{
							Config:           config,
							ConfigPlanChecks: expectEmptyPlan,
						},
					},
				})

				expectBlockList(t, srv, unrelatedBlockMAC)
			})
		}
	}
}

func TestClientBlockResourceImportNotFound(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + blockConfig(blockMAC),
				ResourceName:  blockAddress,
				ImportState:   true,
				ImportStateId: blockMAC,
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

func TestClientBlockResourceInvalidImportID(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        fakeRouterConfig(srv) + blockConfig(blockMAC),
				ResourceName:  blockAddress,
				ImportState:   true,
				ImportStateId: "02-00-00-00-00-a1",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestClientBlockResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					setBlockList(srv, blockListMode, unrelatedBlockMAC)
				},
				Config:           config,
				ConfigPlanChecks: expectResourceAction(blockAddress, plancheck.ResourceActionCreate),
				Check: func(*terraform.State) error {
					expectBlockList(t, srv, unrelatedBlockMAC, blockMACUpper)
					return nil
				},
			},
		},
	})

	expectBlockList(t, srv, unrelatedBlockMAC)
}

// TestClientBlockResourceDeleteWhenGone removes the MAC address from the router after the refresh of the destroy.
func TestClientBlockResourceDeleteWhenGone(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)

	var removeAfterRead, removeAfterReadDone atomic.Bool
	getConfig := srv.Handler("black_white_list", "get_config")
	srv.Handle("black_white_list", "get_config", func(params json.RawMessage) (any, error) {
		res, err := getConfig(params)
		if removeAfterRead.Swap(false) {
			setBlockList(srv, blockListMode, unrelatedBlockMAC)
			removeAfterReadDone.Store(true)
		}
		return res, err
	})
	var writes atomic.Int32
	setSingleMAC := srv.Handler("black_white_list", "set_single_mac")
	srv.Handle("black_white_list", "set_single_mac", func(params json.RawMessage) (any, error) {
		writes.Add(1)
		return setSingleMAC(params)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(*terraform.State) error {
					removeAfterRead.Store(true)
					writes.Store(0)
					return nil
				},
			},
			{
				Config:  config,
				Destroy: true,
			},
		},
	})

	if !removeAfterReadDone.Load() {
		t.Error("the MAC address was not removed from the router before the delete")
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("%d writes after the MAC address was removed, want none", n)
	}
	expectBlockList(t, srv, unrelatedBlockMAC)
}

func TestClientBlockResourceAlreadyExists(t *testing.T) {
	parallel(t)
	for _, entry := range []string{blockMAC, blockMACUpper} {
		t.Run(entry, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setBlockList(srv, blockListMode, entry)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + blockConfig(blockMAC),
						ExpectError: regexp.MustCompile(`(?s)Client Block Already Exists.*` + regexp.QuoteMeta(entry) + `.*Import it with the ID "` + blockMAC + `"`),
					},
				},
			})

			expectBlockList(t, srv, entry)
		})
	}
}

// TestClientBlockResourceMixedCase has the MAC address in two letter cases on the router.
// The resource owns both entries: create fails, import finds them, and destroy removes both.
func TestClientBlockResourceMixedCase(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setBlockList(srv, blockListMode, blockMAC, unrelatedBlockMAC, blockMACUpper)
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Client Block Already Exists.*` + blockMAC + `, ` + blockMACUpper),
			},
			{
				Config:             config,
				ResourceName:       blockAddress,
				ImportState:        true,
				ImportStateId:      blockMAC,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectBlockList(t, srv, unrelatedBlockMAC)
}

func TestClientBlockResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, mac := range []string{"02-00-00-00-00-a1", "02:00:00:00:00", "02:00:00:00:00:a1:ff", "02:00:00:00:00:g1"} {
		t.Run(mac, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + blockConfig(mac),
						ExpectError: regexp.MustCompile(`must be a MAC address`),
					},
				},
			})
		})
	}

	expectBlockList(t, srv)
}

func TestClientBlockResourceAllowListModeCreate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failWritesOutsideBlockMode(t, srv)
	setBlockList(srv, "white", unrelatedBlockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + blockConfig(blockMAC),
				ExpectError: regexp.MustCompile(`(?s)Router Is Not in Block-List Mode.*"white"`),
			},
		},
	})

	if got := srv.AccessControl.Config(); !reflect.DeepEqual(got, glinettest.AccessControlConfig{Mode: "white", BlackMAC: []string{unrelatedBlockMAC}}) {
		t.Errorf("access control configuration is %+v", got)
	}
}

// TestClientBlockResourceAllowListModeReadAndDelete keeps the resource in the state while the router is in mode "white",
// and fails the destroy without a write until the router is in mode "black" again.
func TestClientBlockResourceAllowListModeReadAndDelete(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	failWritesOutsideBlockMode(t, srv)
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					setBlockList(srv, "white")
				},
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectBlockMAC(blockMAC),
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Router Is Not in Block-List Mode`),
			},
			{
				PreConfig: func() {
					setBlockList(srv, blockListMode, blockMACUpper)
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectBlockList(t, srv)
}

func TestClientBlockResourceReadErrorAfterCreate(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	getConfig := srv.Handler("black_white_list", "get_config")
	var failed atomic.Bool
	srv.Handle("black_white_list", "get_config", func(params json.RawMessage) (any, error) {
		if len(srv.AccessControl.Config().BlackMAC) > 0 && !failed.Swap(true) {
			return nil, glinettest.ErrMethodNotFound
		}
		return getConfig(params)
	})
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read Client Block.*Method not found`),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(blockAddress, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: expectBlockMAC(blockMAC),
				Check: func(*terraform.State) error {
					expectBlockList(t, srv, blockMACUpper)
					return nil
				},
			},
		},
	})

	expectBlockList(t, srv)
}

func TestClientBlockResourceRouterErrors(t *testing.T) {
	parallel(t)
	tests := []struct {
		name   string
		method string
		error  string
	}{
		{"read", "get_config", `Unable to Read Block List`},
		{"write", "set_single_mac", `Unable to Create Client Block`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			expectReadError(t, srv, blockConfig(blockMAC), "black_white_list", tt.method, tt.error)
			expectBlockList(t, srv)
		})
	}
}

func TestClientBlockResourceDeleteError(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	config := fakeRouterConfig(srv) + blockConfig(blockMAC)
	setSingleMAC := srv.Handler("black_white_list", "set_single_mac")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig:   failRouterMethod(srv, "black_white_list", "set_single_mac"),
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Unable to Delete Client Block.*Method not found`),
			},
			{
				PreConfig: func() {
					srv.Handle("black_white_list", "set_single_mac", setSingleMAC)
				},
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})

	expectBlockList(t, srv)
}

// slowBlockListReads makes each read of the block list take 20 ms, so that parallel creates overlap.
func slowBlockListReads(srv *glinettest.Server) {
	getConfig := srv.Handler("black_white_list", "get_config")
	srv.Handle("black_white_list", "get_config", func(params json.RawMessage) (any, error) {
		res, err := getConfig(params)
		time.Sleep(20 * time.Millisecond)
		return res, err
	})
}

func TestClientBlockResourceParallel(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	slowBlockListReads(srv)
	setBlockList(srv, blockListMode, unrelatedBlockMAC)

	blocks := func(n int) string {
		config := fakeRouterConfig(srv)
		for i := 1; i <= n; i++ {
			config += fmt.Sprintf(`
resource "glinet_client_block" "b%d" {
  mac = "02:00:00:00:01:%02x"
}
`, i, i)
		}
		return config
	}
	upper := func(n int) []string {
		var macs []string
		for i := 1; i <= n; i++ {
			macs = append(macs, fmt.Sprintf("02:00:00:00:01:%02X", i))
		}
		return macs
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: blocks(6),
				Check: func(*terraform.State) error {
					got := srv.AccessControl.Config().BlackMAC
					want := append([]string{unrelatedBlockMAC}, upper(6)...)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						return fmt.Errorf("block list is %v, want %v", got, want)
					}
					return nil
				},
			},
			{
				Config: blocks(2),
				Check: func(*terraform.State) error {
					got := srv.AccessControl.Config().BlackMAC
					want := append([]string{unrelatedBlockMAC}, upper(2)...)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						return fmt.Errorf("block list is %v, want %v", got, want)
					}
					return nil
				},
			},
		},
	})

	expectBlockList(t, srv, unrelatedBlockMAC)
}

// TestClientBlockResourceParallelSameMAC creates two blocks of the same MAC address in one apply.
// Only one of them can own the entry, so the other one must fail.
func TestClientBlockResourceParallelSameMAC(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	slowBlockListReads(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + fmt.Sprintf(`
resource "glinet_client_block" "first" {
  mac = %q
}

resource "glinet_client_block" "second" {
  mac = %q
}
`, blockMAC, blockMAC),
				ExpectError: regexp.MustCompile(`Client Block Already Exists`),
			},
			{
				PreConfig: func() {
					expectBlockList(t, srv, blockMACUpper)
				},
				Config: fakeRouterConfig(srv),
			},
		},
	})

	expectBlockList(t, srv)
}

func TestAllowTestMACBlock(t *testing.T) {
	parallel(t)
	tests := []struct {
		method string
		params string
		allow  bool
	}{
		{"get_config", `{}`, true},
		{"set_single_mac", `{"mode":"black","operate":"add","mac":"02:00:00:00:00:A1"}`, true},
		{"set_single_mac", `{"mode":"black","operate":"del","mac":"02:00:00:00:00:A1"}`, true},
		{"set_single_mac", `{"mode":"black","operate":"del","mac":"02:00:00:00:00:a1"}`, true},
		{"set_single_mac", `{"mode":"black","operate":"add","mac":"02:00:00:00:00:a1"}`, false},
		{"set_single_mac", `{"mode":"black","operate":"add","mac":"02:00:00:00:00:A2"}`, false},
		{"set_single_mac", `{"mode":"black","operate":"del","mac":"02:00:00:00:00:a2"}`, false},
		{"set_single_mac", `{"mode":"white","operate":"add","mac":"02:00:00:00:00:A1"}`, false},
		{"set_single_mac", `{"operate":"add","mac":"02:00:00:00:00:A1"}`, false},
		{"set_single_mac", `{"mode":"black","operate":"toggle","mac":"02:00:00:00:00:A1"}`, false},
		{"set_single_mac", `{"mode":"black","operate":"add","mac":"02:00:00:00:00:A1","extra":1}`, false},
		{"set_config", `{"mode":"black","mac":[]}`, false},
	}

	for _, tt := range tests {
		var params map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.params), &params); err != nil {
			t.Fatal(err)
		}
		if err := allowTestMACBlock("black_white_list", tt.method, params); (err == nil) != tt.allow {
			t.Errorf("black_white_list.%s %s: error %v, want allowed %t", tt.method, tt.params, err, tt.allow)
		}
	}
	if err := allowTestMACBlock("clients", "block_client", nil); err == nil {
		t.Error("clients.block_client is allowed")
	}
}

const (
	upperCaseBlockMAC = "02:00:00:00:AB:CD"
	lowerCaseBlockMAC = "02:00:00:00:ab:cd"
	mixedCaseBlockMAC = "02:00:00:00:aB:Cd"
)

// countBlockListWrites returns the number of block-list writes to the fake router.
func countBlockListWrites(srv *glinettest.Server) *atomic.Int32 {
	var writes atomic.Int32
	for _, method := range []string{"set_single_mac", "set_config"} {
		next := srv.Handler("black_white_list", method)
		srv.Handle("black_white_list", method, func(params json.RawMessage) (any, error) {
			writes.Add(1)
			return next(params)
		})
	}
	return &writes
}

// TestClientBlockResourceLetterCase configures the MAC address in upper case and in mixed case.
// The state keeps the configured letter case, also when the router has the MAC address in another letter case,
// and a change of only the letter case does not write to the router.
func TestClientBlockResourceLetterCase(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setBlockList(srv, blockListMode, unrelatedBlockMAC)
	writes := countBlockListWrites(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + blockConfig(upperCaseBlockMAC),
				ConfigStateChecks: expectBlockMAC(upperCaseBlockMAC),
				Check: func(*terraform.State) error {
					expectBlockList(t, srv, unrelatedBlockMAC, upperCaseBlockMAC)
					return nil
				},
			},
			{
				PreConfig: func() {
					setBlockList(srv, blockListMode, unrelatedBlockMAC, lowerCaseBlockMAC)
				},
				Config:            fakeRouterConfig(srv) + blockConfig(upperCaseBlockMAC),
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: expectBlockMAC(upperCaseBlockMAC),
			},
			{
				PreConfig: func() {
					writes.Store(0)
				},
				Config:            fakeRouterConfig(srv) + blockConfig(mixedCaseBlockMAC),
				ConfigPlanChecks:  expectResourceAction(blockAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectBlockMAC(mixedCaseBlockMAC),
				Check: func(*terraform.State) error {
					if n := writes.Load(); n != 0 {
						return fmt.Errorf("%d block-list writes for a change of the letter case, want none", n)
					}
					expectBlockList(t, srv, unrelatedBlockMAC, lowerCaseBlockMAC)
					return nil
				},
			},
			{
				Config:           fakeRouterConfig(srv) + blockConfig(mixedCaseBlockMAC),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config:            fakeRouterConfig(srv) + blockConfig(otherBlockMACUpper),
				ConfigPlanChecks:  expectResourceAction(blockAddress, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectBlockMAC(otherBlockMACUpper),
				Check: func(*terraform.State) error {
					expectBlockList(t, srv, unrelatedBlockMAC, otherBlockMACUpper)
					return nil
				},
			},
		},
	})

	expectBlockList(t, srv, unrelatedBlockMAC)
}

// TestClientBlockResourceImportLetterCase imports with the import ID in each letter case for a configuration
// in upper case. The import sets the MAC address in lower case, so the next apply changes only the state.
func TestClientBlockResourceImportLetterCase(t *testing.T) {
	parallel(t)
	for _, id := range []string{upperCaseBlockMAC, lowerCaseBlockMAC, mixedCaseBlockMAC} {
		t.Run(id, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setBlockList(srv, blockListMode, unrelatedBlockMAC, upperCaseBlockMAC)
			config := fakeRouterConfig(srv) + blockConfig(upperCaseBlockMAC)
			var writes *atomic.Int32

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       blockAddress,
						ImportState:        true,
						ImportStateId:      id,
						ImportStatePersist: true,
						ImportStateCheck: func(states []*terraform.InstanceState) error {
							if len(states) != 1 || states[0].Attributes["mac"] != lowerCaseBlockMAC {
								return fmt.Errorf("imported states are %v, want one with mac %s", states, lowerCaseBlockMAC)
							}
							return nil
						},
					},
					{
						PreConfig: func() {
							writes = countBlockListWrites(srv)
						},
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(blockAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: expectBlockMAC(upperCaseBlockMAC),
						Check: func(*terraform.State) error {
							if n := writes.Load(); n != 0 {
								return fmt.Errorf("%d block-list writes for a change of the letter case, want none", n)
							}
							return nil
						},
					},
					{
						Config:           config,
						ConfigPlanChecks: expectEmptyPlan,
					},
				},
			})

			expectBlockList(t, srv, unrelatedBlockMAC)
		})
	}
}
