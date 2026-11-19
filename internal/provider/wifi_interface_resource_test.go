package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	wifiInterfaceAddress = "glinet_wifi_interface.test"
	testWifiKey          = "test-wifi-key-1"
	otherWifiKey         = "test-wifi-key-2"
	hexKey               = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// wifiInterface is the configuration of a glinet_wifi_interface resource. hidden is in the configuration only when it is true.
type wifiInterface struct {
	resName, name, ssid, key, encryption string
	hidden, enabled                      bool
}

func (c wifiInterface) address() string {
	if c.resName == "" {
		return wifiInterfaceAddress
	}
	return "glinet_wifi_interface." + c.resName
}

func (c wifiInterface) config() string {
	resName := c.resName
	if resName == "" {
		resName = "test"
	}
	res := fmt.Sprintf(`
resource "glinet_wifi_interface" %q {
  name       = %q
  ssid       = %q
  encryption = %q
  enabled    = %t
`, resName, c.name, c.ssid, c.encryption, c.enabled)
	if c.key != "" {
		res += fmt.Sprintf("  key = %q\n", c.key)
	}
	if c.hidden {
		res += "  hidden = true\n"
	}
	return res + "}\n"
}

// radioOf gives the radio device, band and guest flag of each interface of the fake router, and whether the
// interface is enabled on a new fake router.
var radioOf = map[string]struct {
	device, band   string
	guest, enabled bool
	fields         string
}{
	"default_radio0": {"radio0", "5G", false, true, `"device":"radio0","hwmode":"11a/n/ac/ax","txpower":"Medium","random_bssid":true`},
	"guest5g":        {"radio0", "5G", true, false, ""},
	"default_radio1": {"radio1", "2G", false, false, `"device":"radio1","hwmode":"11g/n/ax","txpower":"Max","random_bssid":true`},
	"guest2g":        {"radio1", "2G", true, false, ""},
}

func (c wifiInterface) state() []statecheck.StateCheck {
	radio := radioOf[c.name]
	return expectAttributes(c.address(), map[string]knownvalue.Check{
		"name":       knownvalue.StringExact(c.name),
		"ssid":       knownvalue.StringExact(c.ssid),
		"key":        stringOrNullCheck(c.key),
		"encryption": knownvalue.StringExact(c.encryption),
		"hidden":     knownvalue.Bool(c.hidden),
		"enabled":    knownvalue.Bool(c.enabled),
		"band":       knownvalue.StringExact(radio.band),
		"guest":      knownvalue.Bool(radio.guest),
		"radio":      knownvalue.StringExact(radio.device),
	})
}

// settingsParams returns the params of the settings write of c, with radioFields for a main interface.
func (c wifiInterface) settingsParams(radioFields string) string {
	res := fmt.Sprintf(`{"iface_name":%q,"ssid":%q,"encryption":%q,"hidden":%t`, c.name, c.ssid, c.encryption, c.hidden)
	if c.key != "" {
		res += fmt.Sprintf(`,"key":%q`, c.key)
	}
	if radioFields != "" {
		res += "," + radioFields
	}
	return res + "}"
}

func (c wifiInterface) defaultSettingsParams() string {
	return c.settingsParams(radioOf[c.name].fields)
}

func (c wifiInterface) switchParams() string {
	return fmt.Sprintf(`{"iface_name":%q,"enabled":%t}`, c.name, c.enabled)
}

// createParams returns the params of the calls of a create of c on a new fake router, when the settings of c
// differ from those of the router.
func (c wifiInterface) createParams() []string {
	if c.enabled == radioOf[c.name].enabled {
		return []string{c.defaultSettingsParams()}
	}
	if c.enabled {
		return []string{c.defaultSettingsParams(), c.switchParams()}
	}
	return []string{c.switchParams(), c.defaultSettingsParams()}
}

// fakeWifiConfig is the get_config result of the fake router, with the radios and their interfaces as maps.
type fakeWifiConfig struct {
	DFSSupport bool             `json:"dfs_support"`
	Radios     []map[string]any `json:"res"`
}

// wifiRadios returns the get_config result of the fake router and the interfaces of each radio.
func wifiRadios(t *testing.T, srv *glinettest.Server) (fakeWifiConfig, [][]map[string]any) {
	t.Helper()
	var config fakeWifiConfig
	if err := json.Unmarshal(srv.Wifi.Config(), &config); err != nil {
		t.Fatal(err)
	}
	var ifaces [][]map[string]any
	for _, radio := range config.Radios {
		var items []map[string]any
		if err := json.Unmarshal([]byte(mustJSON(t, radio["ifaces"])), &items); err != nil {
			t.Fatal(err)
		}
		ifaces = append(ifaces, items)
	}
	return config, ifaces
}

// setWifi sets fields in the radio device and, when name is not empty, in its interface name.
func setWifi(t *testing.T, srv *glinettest.Server, device string, radioFields map[string]any, name string, ifaceFields map[string]any) {
	t.Helper()
	config, ifaces := wifiRadios(t, srv)
	for i, radio := range config.Radios {
		if radio["device"] != device {
			continue
		}
		maps.Copy(radio, radioFields)
		for _, iface := range ifaces[i] {
			if iface["name"] == name {
				maps.Copy(iface, ifaceFields)
			}
		}
		radio["ifaces"] = ifaces[i]
	}
	srv.Wifi.SetConfig(json.RawMessage(mustJSON(t, config)))
}

// expectRouterWifiInterface checks that the interface of c on the fake router has the values of c.
func expectRouterWifiInterface(t *testing.T, srv *glinettest.Server, c wifiInterface) resource.TestCheckFunc {
	return func(*terraform.State) error {
		_, ifaces := wifiRadios(t, srv)
		for _, radio := range ifaces {
			for _, iface := range radio {
				if iface["name"] != c.name {
					continue
				}
				got := fmt.Sprintf("%v %v %v %v", iface["ssid"], iface["encryption"], iface["hidden"], iface["enabled"])
				want := fmt.Sprintf("%v %v %v %v", c.ssid, c.encryption, c.hidden, c.enabled)
				if got != want {
					return fmt.Errorf("router interface %s has ssid, encryption, hidden, enabled %s, want %s", c.name, got, want)
				}
				if c.key != "" && iface["key"] != c.key {
					return fmt.Errorf("router interface %s has another key", c.name)
				}
				return nil
			}
		}
		return fmt.Errorf("router has no interface %s", c.name)
	}
}

var guest2g = wifiInterface{name: "guest2g", ssid: "guest-2g", key: testWifiKey, encryption: "psk2"}

// TestWifiInterfaceResource takes over guest2g, which is disabled on the router, and changes each attribute. Each step checks which of the two
// writes are sent, with which params, and in which order.
func TestWifiInterfaceResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")

	enabled := guest2g
	enabled.enabled = true
	renamed := enabled
	renamed.ssid = "guest-2g-new"
	newKey := renamed
	newKey.key = otherWifiKey
	sae := newKey
	sae.encryption = "sae-mixed"
	hidden := sae
	hidden.hidden = true
	disabledRenamed := hidden
	disabledRenamed.enabled, disabledRenamed.ssid = false, "guest-2g-off"
	open := disabledRenamed
	open.encryption, open.key = "none", ""
	enabledOpen := open
	enabledOpen.enabled = true
	hexKeyed := enabledOpen
	hexKeyed.encryption, hexKeyed.key, hexKeyed.hidden = "psk2", hexKey, false

	steps := []struct {
		c     wifiInterface
		calls []string
	}{
		{guest2g, []string{guest2g.settingsParams("")}},
		{enabled, []string{enabled.switchParams()}},
		{renamed, []string{renamed.settingsParams("")}},
		{newKey, []string{newKey.settingsParams("")}},
		{sae, []string{sae.settingsParams("")}},
		{hidden, []string{hidden.settingsParams("")}},
		{disabledRenamed, []string{disabledRenamed.switchParams(), disabledRenamed.settingsParams("")}},
		{open, []string{open.settingsParams("")}},
		{enabledOpen, []string{enabledOpen.switchParams()}},
		{hexKeyed, []string{hexKeyed.settingsParams("")}},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		ts := resource.TestStep{
			Config:            fakeRouterConfig(srv) + s.c.config(),
			ConfigStateChecks: s.c.state(),
			Check:             resource.ComposeTestCheckFunc(calls.expect(s.calls...), expectRouterWifiInterface(t, srv, s.c)),
		}
		if i > 0 {
			ts.ConfigPlanChecks = expectResourceAction(wifiInterfaceAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, ts)
	}
	testSteps = append(testSteps, resource.TestStep{
		ResourceName:                         wifiInterfaceAddress,
		ImportState:                          true,
		ImportStateId:                        "guest2g",
		ImportStateVerify:                    true,
		ImportStateVerifyIdentifierAttribute: "name",
	})
	applied := saveRouterJSON(srv.Wifi.Config)
	testSteps[len(steps)-1].Check = resource.ComposeTestCheckFunc(testSteps[len(steps)-1].Check, applied.save)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(calls.expect(), applied.expect),
		Steps:                    testSteps,
	})
}

// TestWifiInterfaceResourceCreate takes over an interface and sends only the calls whose values differ from the router values.
func TestWifiInterfaceResourceCreate(t *testing.T) {
	parallel(t)
	equal := wifiInterface{name: "guest2g", ssid: "ssid-3", key: "redacted-passwd-2", encryption: "psk2"}
	enabled := equal
	enabled.enabled = true
	open := wifiInterface{name: "guest2g", ssid: "ssid-3", encryption: "none"}

	tests := []struct {
		name  string
		c     wifiInterface
		calls []string
	}{
		{"equal", equal, nil},
		{"open network equal", open, nil},
		{"only settings differ", guest2g, []string{guest2g.settingsParams("")}},
		{"only enabled differs", enabled, []string{enabled.switchParams()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			if tt.c.encryption == "none" {
				setWifi(t, srv, "radio1", nil, "guest2g", map[string]any{"encryption": "none"})
			}
			calls := recordCalls(t, srv, "wifi", "set_config")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + tt.c.config(),
						ConfigStateChecks: tt.c.state(),
						Check:             resource.ComposeTestCheckFunc(calls.expect(tt.calls...), expectRouterWifiInterface(t, srv, tt.c)),
					},
				},
			})
		})
	}
}

// TestWifiInterfaceResourceEachInterface takes over each interface of the fake router. A write of a main interface
// sends the radio values of the router.
func TestWifiInterfaceResourceEachInterface(t *testing.T) {
	parallel(t)
	for _, name := range []string{"default_radio0", "guest5g", "default_radio1", "guest2g"} {
		t.Run(name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "wifi", "set_config")
			c := wifiInterface{name: name, ssid: "ssid-" + name, key: testWifiKey, encryption: "sae", enabled: true}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + c.config(),
						ConfigStateChecks: c.state(),
						Check:             resource.ComposeTestCheckFunc(calls.expect(c.createParams()...), expectRouterWifiInterface(t, srv, c)),
					},
				},
			})
		})
	}
}

// TestWifiInterfaceResourceRadioValues changes the radio values on the router before a write of a main interface:
// the write sends the values that the router has, and channel and htmode stay as they are.
func TestWifiInterfaceResourceRadioValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")
	c := wifiInterface{name: "default_radio0", ssid: "main", key: testWifiKey, encryption: "psk2", enabled: true}
	renamed := c
	renamed.ssid = "main-new"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + c.config(),
				Check:  calls.expect(c.defaultSettingsParams()),
			},
			{
				PreConfig: func() {
					setWifi(t, srv, "radio0", map[string]any{"hwmode": "11ac/ax", "txpower": "Low", "random_bssid": false, "channel": 36, "htmode": "80"}, "", nil)
				},
				Config: fakeRouterConfig(srv) + renamed.config(),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(renamed.settingsParams(`"device":"radio0","hwmode":"11ac/ax","txpower":"Low","random_bssid":false`)),
					func(*terraform.State) error {
						config, _ := wifiRadios(t, srv)
						radios := config.Radios
						got := fmt.Sprintf("%v %v %v %v %v", radios[0]["hwmode"], radios[0]["txpower"], radios[0]["random_bssid"], radios[0]["channel"], radios[0]["htmode"])
						if want := "11ac/ax Low false 36 80"; got != want {
							return fmt.Errorf("router radio0 has %s, want %s", got, want)
						}
						return nil
					},
				),
			},
		},
	})
}

// TestWifiInterfaceResourceParallel applies the four interfaces in one apply.
func TestWifiInterfaceResourceParallel(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")

	var ifaces []wifiInterface
	var config string
	var want []string
	var checks []statecheck.StateCheck
	for i, name := range []string{"default_radio0", "guest5g", "default_radio1", "guest2g"} {
		c := wifiInterface{resName: fmt.Sprintf("i%d", i), name: name, ssid: "ssid-" + name, key: testWifiKey, encryption: "psk-mixed", enabled: i%2 == 0}
		ifaces = append(ifaces, c)
		config += c.config()
		want = append(want, c.createParams()...)
		checks = append(checks, c.state()...)
	}
	routerChecks := []resource.TestCheckFunc{calls.expectUnordered(want...)}
	for _, c := range ifaces {
		routerChecks = append(routerChecks, expectRouterWifiInterface(t, srv, c))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + config,
				ConfigStateChecks: checks,
				Check:             resource.ComposeTestCheckFunc(routerChecks...),
			},
		},
	})
}

func TestWifiInterfaceResourceUnknownInterface(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")
	c := guest2g
	c.name = "guest6g"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + c.config(),
				ExpectError: regexp.MustCompile(`(?s)Wi-Fi Interface Not Found.*` + wrapped(`The router has no Wi-Fi interface "guest6g". The interfaces of the router are: default_radio0, guest5g, default_radio1, guest2g.`)),
			},
			{
				Config:        fakeRouterConfig(srv) + c.config(),
				ResourceName:  wifiInterfaceAddress,
				ImportState:   true,
				ImportStateId: "guest6g",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
	if got := calls.take(); len(got) != 0 {
		t.Errorf("wifi.set_config was called with %q", got)
	}
}

func TestWifiInterfaceResourceUnsupportedEncryption(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setWifi(t, srv, "radio1", map[string]any{"encryptions": []string{"none", "psk2", "psk-mixed"}}, "", nil)
	calls := recordCalls(t, srv, "wifi", "set_config")
	c := guest2g
	c.encryption = "sae"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + c.config(),
				ExpectError: regexp.MustCompile(`(?s)Encryption Not Supported.*` + wrapped(`does not support the encryption "sae". It supports: none, psk2, psk-mixed.`)),
			},
		},
	})
	if got := calls.take(); len(got) != 0 {
		t.Errorf("wifi.set_config was called with %q", got)
	}
}

// TestWifiInterfaceResourceListedEncryption sets encryptions that the radio lists, but that the radio of the GL-AXT1800 does not have.
// Only "wep" is not in the list, so it fails without a write.
func TestWifiInterfaceResourceListedEncryption(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	setWifi(t, srv, "radio1", map[string]any{"encryptions": []string{"none", "psk2", "psk2+sae", "psk2+ccmp"}}, "", nil)
	calls := recordCalls(t, srv, "wifi", "set_config")
	hexKeyed := guest2g
	hexKeyed.encryption, hexKeyed.key = "psk2+ccmp", hexKey
	sae := guest2g
	sae.encryption = "psk2+sae"
	wep := guest2g
	wep.encryption = "wep"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + hexKeyed.config(),
				ConfigStateChecks: hexKeyed.state(),
				Check:             calls.expect(hexKeyed.defaultSettingsParams()),
			},
			{
				Config:            fakeRouterConfig(srv) + sae.config(),
				ConfigStateChecks: sae.state(),
				Check:             calls.expect(sae.defaultSettingsParams()),
			},
			{
				Config:      fakeRouterConfig(srv) + wep.config(),
				ExpectError: regexp.MustCompile(`(?s)Encryption Not Supported.*` + wrapped(`does not support the encryption "wep". It supports: none, psk2, psk2+sae, psk2+ccmp.`)),
			},
		},
	})
	if got := calls.take(); len(got) != 0 {
		t.Errorf("wifi.set_config was called with %q", got)
	}
}

func TestWifiInterfaceResourceImport(t *testing.T) {
	parallel(t)
	tests := []struct {
		name string
		c    wifiInterface
	}{
		{"default_radio0", wifiInterface{name: "default_radio0", ssid: "ssid-1", key: "redacted-passwd-1", encryption: "psk2", enabled: true}},
		{"guest2g", wifiInterface{name: "guest2g", ssid: "ssid-3", key: "redacted-passwd-2", encryption: "psk2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "wifi", "set_config")
			config := fakeRouterConfig(srv) + tt.c.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             config,
						ResourceName:       wifiInterfaceAddress,
						ImportState:        true,
						ImportStateId:      tt.c.name,
						ImportStatePersist: true,
					},
					{
						Config:            config,
						ConfigPlanChecks:  expectEmptyPlan,
						ConfigStateChecks: tt.c.state(),
						Check:             calls.expect(),
					},
				},
			})
		})
	}

	t.Run("open network", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		setWifi(t, srv, "radio1", nil, "guest2g", map[string]any{"encryption": "none"})
		c := wifiInterface{name: "guest2g", ssid: "ssid-3", encryption: "none"}
		config := fakeRouterConfig(srv) + c.config()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             config,
					ResourceName:       wifiInterfaceAddress,
					ImportState:        true,
					ImportStateId:      "guest2g",
					ImportStatePersist: true,
				},
				{
					Config:            config,
					ConfigPlanChecks:  expectEmptyPlan,
					ConfigStateChecks: c.state(),
				},
			},
		})
	})
}

func TestWifiInterfaceResourceDrift(t *testing.T) {
	parallel(t)
	c := guest2g
	c.enabled = true

	tests := []struct {
		name   string
		fields map[string]any
		calls  []string
	}{
		{"ssid", map[string]any{"ssid": "other"}, []string{c.settingsParams("")}},
		{"key", map[string]any{"key": otherWifiKey}, []string{c.settingsParams("")}},
		{"encryption", map[string]any{"encryption": "sae"}, []string{c.settingsParams("")}},
		{"hidden", map[string]any{"hidden": true}, []string{c.settingsParams("")}},
		{"enabled", map[string]any{"enabled": false}, []string{c.switchParams()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			calls := recordCalls(t, srv, "wifi", "set_config")
			config := fakeRouterConfig(srv) + c.config()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
					},
					{
						PreConfig: func() {
							calls.take()
							setWifi(t, srv, "radio1", nil, "guest2g", tt.fields)
						},
						Config:            config,
						ConfigPlanChecks:  expectResourceAction(wifiInterfaceAddress, plancheck.ResourceActionUpdate),
						ConfigStateChecks: c.state(),
						Check:             resource.ComposeTestCheckFunc(calls.expect(tt.calls...), expectRouterWifiInterface(t, srv, c)),
					},
				},
			})
		})
	}

	t.Run("key of an open network", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		open := wifiInterface{name: "guest2g", ssid: "open", encryption: "none"}
		config := fakeRouterConfig(srv) + open.config()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					PreConfig:        func() { setWifi(t, srv, "radio1", nil, "guest2g", map[string]any{"key": otherWifiKey}) },
					Config:           config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})
}

// TestWifiInterfaceResourceDestroy destroys the resource, which leaves the router unchanged.
func TestWifiInterfaceResourceDestroy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")
	applied := saveRouterJSON(srv.Wifi.Config)
	c := wifiInterface{name: "default_radio1", ssid: "main-2g", key: testWifiKey, encryption: "psk2"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(calls.expect(), applied.expect),
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + c.config(),
				Check:  resource.ComposeTestCheckFunc(calls.expect(c.defaultSettingsParams()), applied.save),
			},
		},
	})
}

func TestWifiInterfaceResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	before := srv.Wifi.Config()
	calls := recordCalls(t, srv, "wifi", "set_config")

	with := func(change func(*wifiInterface)) string {
		c := guest2g
		change(&c)
		return c.config()
	}

	tests := []struct {
		config, error string
	}{
		{with(func(c *wifiInterface) { c.name = "" }), `name string length must be at least 1`},
		{with(func(c *wifiInterface) { c.ssid = "" }), `ssid string length must be between 1 and 32`},
		{with(func(c *wifiInterface) { c.ssid = strings.Repeat("a", 33) }), `ssid string length must be between 1 and 32`},
		{with(func(c *wifiInterface) { c.ssid = strings.Repeat("é", 17) }), `ssid string length must be between 1 and 32`},
		{with(func(c *wifiInterface) { c.key = "" }), `(?s)Invalid Attribute Combination.*key is required when encryption is psk2`},
		{with(func(c *wifiInterface) { c.key, c.encryption = "", "sae" }), `key is required when encryption is sae`},
		{with(func(c *wifiInterface) { c.encryption = "none" }), `key cannot be set when encryption is none`},
		{with(func(c *wifiInterface) { c.key = "1234567" }), `key must be 8 to 63 printable ASCII characters or 64 hex digits`},
		{with(func(c *wifiInterface) { c.key = strings.Repeat("a", 64) + "x" }), `key must be 8 to 63 printable ASCII characters`},
		{with(func(c *wifiInterface) { c.key = strings.Repeat("g", 64) }), `key must be 8 to 63 printable ASCII characters`},
		{with(func(c *wifiInterface) { c.key = "password\twith tab" }), `key must be 8 to 63 printable ASCII characters`},
		{with(func(c *wifiInterface) { c.key = "pässwörter" }), `key must be 8 to 63 printable ASCII characters`},
		{with(func(c *wifiInterface) { c.key, c.encryption = hexKey, "sae" }), `key must have at most 63 characters when encryption is sae`},
		{with(func(c *wifiInterface) { c.key, c.encryption = hexKey, "sae-mixed" }), `key must have at most 63 characters when encryption is\s+sae-mixed`},
		{with(func(c *wifiInterface) { c.key, c.encryption = hexKey, "psk2+sae" }), `key must have at most 63 characters when encryption is\s+psk2\+sae`},
		{with(func(c *wifiInterface) { c.key, c.encryption = "", "psk2+sae" }), `key is required when encryption is psk2\+sae`},
		{`resource "glinet_wifi_interface" "test" {
  name       = "guest2g"
  ssid       = "guest"
  encryption = "none"
}`, `The argument "enabled" is required`},
		{strings.Replace(guest2g.config(), "}\n", "band = \"5G\"\n}\n", 1), `Invalid Configuration for Read-Only Attribute`},
	}

	for _, tt := range tests {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + tt.config,
					ExpectError: regexp.MustCompile(tt.error),
				},
			},
		})
	}

	if got := calls.take(); len(got) != 0 {
		t.Errorf("wifi.set_config was called with %q", got)
	}
	expectJSONEqual(t, srv.Wifi.Config(), string(before))
}

// TestWifiInterfaceResourceValidKeys applies the limits of the key rules.
func TestWifiInterfaceResourceValidKeys(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, c := range []wifiInterface{
		{name: "guest2g", ssid: strings.Repeat("é", 16), key: "12345678", encryption: "psk2"},
		{name: "guest2g", ssid: strings.Repeat("a", 32), key: strings.Repeat("~", 63), encryption: "sae"},
		{name: "guest2g", ssid: "a", key: hexKey, encryption: "psk-mixed"},
		{name: "guest2g", ssid: "a b", key: " spaces and ~ ", encryption: "sae-mixed"},
	} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:            fakeRouterConfig(srv) + c.config(),
					ConfigStateChecks: c.state(),
				},
			},
		})
	}
}

// TestWifiInterfaceResourceUnknownValues sets attributes from variables, which are unknown while Terraform validates the configuration.
func TestWifiInterfaceResourceUnknownValues(t *testing.T) {
	parallel(t)
	tests := []struct {
		name, variables, body string
		c                     wifiInterface
	}{
		{
			"encryption",
			`variable "encryption" { default = "none" }`,
			`encryption = var.encryption`,
			wifiInterface{name: "guest2g", ssid: "open", encryption: "none"},
		},
		{
			"null key with none",
			`variable "key" { default = null }`,
			"encryption = \"none\"\nkey = var.key",
			wifiInterface{name: "guest2g", ssid: "open", encryption: "none"},
		},
		{
			"key with sae",
			`variable "key" {
  default   = "` + testWifiKey + `"
  sensitive = true
}`,
			"encryption = \"sae\"\nkey = var.key",
			wifiInterface{name: "guest2g", ssid: "open", encryption: "sae", key: testWifiKey},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			config := fmt.Sprintf(`
resource "glinet_wifi_interface" "test" {
  name    = "guest2g"
  ssid    = "open"
  enabled = false
  %s
}
`, tt.body)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:            fakeRouterConfig(srv) + tt.variables + config,
						ConfigStateChecks: tt.c.state(),
						Check:             expectRouterWifiInterface(t, srv, tt.c),
					},
				},
			})
		})
	}
}

func TestWifiInterfaceResourceRouterErrors(t *testing.T) {
	parallel(t)
	c := guest2g
	config := c.config()

	t.Run("get after set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("wifi", "get_config")
		var n atomic.Int32
		srv.Handle("wifi", "get_config", func(params json.RawMessage) (any, error) {
			if n.Add(1) == 2 {
				return nil, glinettest.ErrMethodNotFound
			}
			return get(params)
		})

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Wi-Fi Configuration.*Method not found`),
				},
				{
					Config:            fakeRouterConfig(srv) + config,
					ConfigPlanChecks:  expectResourceAction(wifiInterfaceAddress, plancheck.ResourceActionDestroyBeforeCreate),
					ConfigStateChecks: c.state(),
				},
			},
		})
	})

	t.Run("get before set", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		calls := recordCalls(t, srv, "wifi", "set_config")
		failRouterMethod(srv, "wifi", "get_config")()

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Wi-Fi Configuration.*Method not found`),
				},
			},
		})
		if got := calls.take(); len(got) != 0 {
			t.Errorf("wifi.set_config was called with %q", got)
		}
	})

	t.Run("get on refresh", func(t *testing.T) {
		srv := glinettest.NewServer(t)
		get := srv.Handler("wifi", "get_config")

		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fakeRouterConfig(srv) + config,
				},
				{
					PreConfig:   failRouterMethod(srv, "wifi", "get_config"),
					Config:      fakeRouterConfig(srv) + config,
					ExpectError: regexp.MustCompile(`(?s)Unable to Read Wi-Fi Configuration.*Method not found`),
				},
				{
					PreConfig:        func() { srv.Handle("wifi", "get_config", get) },
					Config:           fakeRouterConfig(srv) + config,
					ConfigPlanChecks: expectEmptyPlan,
				},
			},
		})
	})

	for _, call := range []string{"switch", "settings"} {
		t.Run("set "+call, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			setWifi(t, srv, "radio1", nil, "guest2g", map[string]any{"enabled": true})
			before := srv.Wifi.Config()
			set := srv.Handler("wifi", "set_config")
			srv.Handle("wifi", "set_config", func(params json.RawMessage) (any, error) {
				if strings.Contains(string(params), `"enabled"`) == (call == "switch") {
					return nil, glinettest.ErrMethodNotFound
				}
				return set(params)
			})

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + config,
						ExpectError: regexp.MustCompile(`(?s)Unable to Set Wi-Fi Interface.*Method not found`),
					},
				},
			})
			if call == "switch" {
				expectJSONEqual(t, srv.Wifi.Config(), string(before))
			}
		})
	}
}
