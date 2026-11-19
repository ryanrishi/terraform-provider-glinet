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

const (
	wifiRadioAddress = "glinet_wifi_radio.test"

	// radio0Iface is the start of the params of a radio0 settings write: the main interface default_radio0 of the
	// fake router, as the router has it.
	radio0Iface = `"iface_name":"default_radio0","ssid":"ssid-1","encryption":"psk2","hidden":false,"key":"redacted-passwd-1","device":"radio0"`
)

// radioValues is the radio settings of a glinet_wifi_radio resource and of the radio on the router.
type radioValues struct {
	channel                   int64
	channelWidth, mode, power string
	randomBSSID               bool
	psc                       *bool
}

var radio0Values = radioValues{channel: 48, channelWidth: "40", mode: "11a/n/ac/ax", power: "Medium", randomBSSID: true}

func (v radioValues) state(name, band string) []statecheck.StateCheck {
	var psc knownvalue.Check = knownvalue.Null()
	if v.psc != nil {
		psc = knownvalue.Bool(*v.psc)
	}
	return expectAttributes(wifiRadioAddress, map[string]knownvalue.Check{
		"name":              knownvalue.StringExact(name),
		"band":              knownvalue.StringExact(band),
		"channel":           knownvalue.Int64Exact(v.channel),
		"channel_width":     knownvalue.StringExact(v.channelWidth),
		"mode":              knownvalue.StringExact(v.mode),
		"tx_power":          knownvalue.StringExact(v.power),
		"random_bssid":      knownvalue.Bool(v.randomBSSID),
		"psc_channels_only": psc,
	})
}

// expectRouterRadio checks that the radio device on the fake router has the values v.
func expectRouterRadio(t *testing.T, srv *glinettest.Server, device string, v radioValues) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, _ := wifiRadios(t, srv)
		for _, radio := range config.Radios {
			if radio["device"] != device {
				continue
			}
			got := fmt.Sprintf("%v %v %v %v %v %v", radio["channel"], radio["htmode"], radio["hwmode"], radio["txpower"], radio["random_bssid"], radio["chan_6g_only_psc"])
			psc := any(nil)
			if v.psc != nil {
				psc = *v.psc
			}
			want := fmt.Sprintf("%v %v %v %v %v %v", v.channel, v.channelWidth, v.mode, v.power, v.randomBSSID, psc)
			if got != want {
				return fmt.Errorf("router radio %s has %s, want %s", device, got, want)
			}
			return nil
		}
		return fmt.Errorf("router has no radio %s", device)
	}
}

func wifiRadioConfig(name, attributes string) string {
	return fmt.Sprintf("resource \"glinet_wifi_radio\" \"test\" {\n  name = %q\n%s}\n", name, attributes)
}

// add6GRadio adds the radio radio2 with the band 6G and the main interface default_radio2 to the fake router.
func add6GRadio(t *testing.T, srv *glinettest.Server) {
	t.Helper()
	config, _ := wifiRadios(t, srv)
	config.Radios = append(config.Radios, map[string]any{
		"band":    "6G",
		"device":  "radio2",
		"channel": 0,
		"channels": []map[string]any{
			{"channel": 1, "dfs": false, "psc": false},
			{"channel": 5, "dfs": false, "psc": true},
		},
		"htmode":           "160",
		"htmodes":          map[string]any{"11ax": 160, "auto": false},
		"hwmode":           "11ax",
		"hwmodes":          []string{"11ax"},
		"txpower":          "Max",
		"random_bssid":     false,
		"chan_6g_only_psc": false,
		"encryptions":      []string{"sae"},
		"ifaces": []map[string]any{
			{"name": "default_radio2", "ssid": "ssid-6g", "key": testWifiKey, "encryption": "sae", "enabled": true, "hidden": false, "guest": false},
		},
	})
	srv.Wifi.SetConfig(json.RawMessage(mustJSON(t, config)))
}

func ptr[T any](v T) *T {
	return &v
}

// TestWifiRadioResource takes over radio0 and changes each attribute. Each step checks which writes are sent and
// with which params.
func TestWifiRadioResource(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	settings := recordCalls(t, srv, "wifi", "set_config")
	txPower := recordCalls(t, srv, "wifi", "set_txpower")

	lowPower := radio0Values
	lowPower.power = "Low"
	channel36 := lowPower
	channel36.channel, channel36.channelWidth = 36, "80"
	mode := channel36
	mode.mode = "11ac/ax"
	random := mode
	random.randomBSSID, random.power = false, "High"
	auto := random
	auto.channel = 0

	steps := []struct {
		attributes      string
		v               radioValues
		settings, power []string
	}{
		{"", radio0Values, nil, nil},
		{`  tx_power = "Low"` + "\n", lowPower, nil, []string{`{"device":"radio0","txpower":"Low"}`}},
		{
			"  tx_power = \"Low\"\n  channel = 36\n  channel_width = \"80\"\n", channel36,
			[]string{`{` + radio0Iface + `,"hwmode":"11a/n/ac/ax","txpower":"Low","random_bssid":true,"channel":36,"htmode":"80"}`}, nil,
		},
		{
			"  tx_power = \"Low\"\n  channel = 36\n  channel_width = \"80\"\n  mode = \"11ac/ax\"\n", mode,
			[]string{`{` + radio0Iface + `,"hwmode":"11ac/ax","txpower":"Low","random_bssid":true}`}, nil,
		},
		{
			"  tx_power = \"High\"\n  channel = 36\n  channel_width = \"80\"\n  mode = \"11ac/ax\"\n  random_bssid = false\n", random,
			[]string{`{` + radio0Iface + `,"hwmode":"11ac/ax","txpower":"High","random_bssid":false}`}, nil,
		},
		{
			"  tx_power = \"High\"\n  channel = 0\n  channel_width = \"80\"\n  mode = \"11ac/ax\"\n  random_bssid = false\n", auto,
			[]string{`{` + radio0Iface + `,"hwmode":"11ac/ax","txpower":"High","random_bssid":false,"channel":0,"htmode":"80"}`}, nil,
		},
	}

	var testSteps []resource.TestStep
	for i, s := range steps {
		ts := resource.TestStep{
			Config:            fakeRouterConfig(srv) + wifiRadioConfig("radio0", s.attributes),
			ConfigStateChecks: s.v.state("radio0", "5G"),
			Check: resource.ComposeTestCheckFunc(
				settings.expect(s.settings...),
				txPower.expect(s.power...),
				expectRouterRadio(t, srv, "radio0", s.v),
			),
		}
		if i > 0 {
			ts.ConfigPlanChecks = expectResourceAction(wifiRadioAddress, plancheck.ResourceActionUpdate)
		}
		testSteps = append(testSteps, ts)
	}
	testSteps = append(testSteps, resource.TestStep{
		ResourceName:                         wifiRadioAddress,
		ImportState:                          true,
		ImportStateId:                        "radio0",
		ImportStateVerify:                    true,
		ImportStateVerifyIdentifierAttribute: "name",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps:                    testSteps,
	})
	if got := settings.take(); len(got) != 0 {
		t.Errorf("wifi.set_config was called with %q", got)
	}
}

// TestWifiRadioResourceRouterValues changes the radio values on the router before a write: the write sends the
// router values of the attributes that are not in the configuration.
func TestWifiRadioResourceRouterValues(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	settings := recordCalls(t, srv, "wifi", "set_config")
	want := radioValues{channel: 40, channelWidth: "40", mode: "11a/n/ac", power: "Low", randomBSSID: false}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wifiRadioConfig("radio0", "  channel = 36\n"),
				Check:  settings.expect(`{` + radio0Iface + `,"hwmode":"11a/n/ac/ax","txpower":"Medium","random_bssid":true,"channel":36,"htmode":"40"}`),
			},
			{
				PreConfig: func() {
					setWifi(t, srv, "radio0", map[string]any{"hwmode": "11a/n/ac", "txpower": "Low", "random_bssid": false}, "default_radio0", map[string]any{"ssid": "ssid-other"})
				},
				Config:            fakeRouterConfig(srv) + wifiRadioConfig("radio0", "  channel = 40\n"),
				ConfigStateChecks: want.state("radio0", "5G"),
				Check: resource.ComposeTestCheckFunc(
					settings.expect(`{"iface_name":"default_radio0","ssid":"ssid-other","encryption":"psk2","hidden":false,"key":"redacted-passwd-1","device":"radio0",`+
						`"hwmode":"11a/n/ac","txpower":"Low","random_bssid":false,"channel":40,"htmode":"40"}`),
					expectRouterRadio(t, srv, "radio0", want),
				),
			},
		},
	})
}

// TestWifiRadioResourceWithInterface manages radio0 and its main interface default_radio0 together: each write
// sends back the values of the other resource, and the plans stay empty.
func TestWifiRadioResourceWithInterface(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")
	iface := wifiInterface{name: "default_radio0", ssid: "main", key: testWifiKey, encryption: "psk2", enabled: true}
	renamed := iface
	renamed.ssid = "main-new"
	radio := wifiRadioConfig("radio0", "  channel = 36\n  channel_width = \"80\"\n  tx_power = \"Low\"\n")
	newMode := wifiRadioConfig("radio0", "  channel = 36\n  channel_width = \"80\"\n  tx_power = \"Low\"\n  mode = \"11ac/ax\"\n")
	values := radioValues{channel: 36, channelWidth: "80", mode: "11a/n/ac/ax", power: "Low", randomBSSID: true}
	newModeValues := values
	newModeValues.mode = "11ac/ax"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + radio + iface.config(),
				Check: resource.ComposeTestCheckFunc(
					func(*terraform.State) error { calls.take(); return nil },
					expectRouterRadio(t, srv, "radio0", values),
					expectRouterWifiInterface(t, srv, iface),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + radio + iface.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
			{
				Config: fakeRouterConfig(srv) + radio + renamed.config(),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(renamed.settingsParams(`"device":"radio0","hwmode":"11a/n/ac/ax","txpower":"Low","random_bssid":true`)),
					expectRouterRadio(t, srv, "radio0", values),
				),
			},
			{
				Config: fakeRouterConfig(srv) + newMode + renamed.config(),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{"iface_name":"default_radio0","ssid":"main-new","encryption":"psk2","hidden":false,"key":"test-wifi-key-1","device":"radio0",`+
						`"hwmode":"11ac/ax","txpower":"Low","random_bssid":true}`),
					expectRouterRadio(t, srv, "radio0", newModeValues),
					expectRouterWifiInterface(t, srv, renamed),
				),
			},
			{
				Config:           fakeRouterConfig(srv) + newMode + renamed.config(),
				ConfigPlanChecks: expectEmptyPlan,
			},
		},
	})
}

func TestWifiRadioResource6G(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	add6GRadio(t, srv)
	calls := recordCalls(t, srv, "wifi", "set_config")
	iface := `"iface_name":"default_radio2","ssid":"ssid-6g","encryption":"sae","hidden":false,"key":"test-wifi-key-1","device":"radio2","hwmode":"11ax","txpower":"Max","random_bssid":false`
	initial := radioValues{channel: 0, channelWidth: "160", mode: "11ax", power: "Max", psc: ptr(false)}
	psc := initial
	psc.channel, psc.psc = 5, ptr(true)
	ifaceRenamed := wifiInterface{resName: "main", name: "default_radio2", ssid: "ssid-6g-new", key: testWifiKey, encryption: "sae", enabled: true}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:            fakeRouterConfig(srv) + wifiRadioConfig("radio2", ""),
				ConfigStateChecks: initial.state("radio2", "6G"),
				Check:             calls.expect(),
			},
			{
				Config:      fakeRouterConfig(srv) + wifiRadioConfig("radio2", "  psc_channels_only = true\n  channel = 1\n"),
				ExpectError: regexp.MustCompile(`(?s)Channel Not Supported.*` + wrapped(`The channel 1 of the Wi-Fi radio radio2 is not a PSC channel.`)),
			},
			{
				Config:            fakeRouterConfig(srv) + wifiRadioConfig("radio2", "  psc_channels_only = true\n  channel = 5\n"),
				ConfigStateChecks: psc.state("radio2", "6G"),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(`{`+iface+`,"chan_6g_only_psc":true,"channel":5,"htmode":"160"}`),
					expectRouterRadio(t, srv, "radio2", psc),
				),
			},
			{
				Config: fakeRouterConfig(srv) + wifiRadioConfig("radio2", "  psc_channels_only = true\n  channel = 5\n") + ifaceRenamed.config(),
				Check: resource.ComposeTestCheckFunc(
					calls.expect(ifaceRenamed.settingsParams(`"device":"radio2","hwmode":"11ax","txpower":"Max","random_bssid":false,"chan_6g_only_psc":true`)),
					expectRouterRadio(t, srv, "radio2", psc),
				),
			},
		},
	})
}

// TestWifiRadioResourceInvalidValues checks the values against the lists of the radio on the router before a write.
func TestWifiRadioResourceInvalidValues(t *testing.T) {
	parallel(t)

	tests := []struct {
		name, device, attributes, err string
		setup                         func(*testing.T, *glinettest.Server)
	}{
		{"unknown radio", "radio9", "", `(?s)Wi-Fi Radio Not Found.*` + wrapped(`The router has no Wi-Fi radio "radio9". The radios of the router are: radio0, radio1.`), nil},
		{"mode", "radio0", `  mode = "11b/g/n"` + "\n", `(?s)Mode Not Supported.*` + wrapped(`The Wi-Fi radio radio0 does not support the mode "11b/g/n". It supports: 11ac/ax, 11n/ac/ax, 11a/n/ac/ax, 11a/n/ac.`), nil},
		{"width", "radio0", `  channel_width = "160"` + "\n", `(?s)Channel Width Not Supported.*` + wrapped(`does not support the channel width "160" with the mode "11a/n/ac/ax". It supports: 20, 40, 80.`), nil},
		{"auto width", "radio0", `  channel_width = "auto"` + "\n", `(?s)Channel Width Not Supported.*` + wrapped(`It supports: 20, 40, 80.`), nil},
		{"width of a new mode", "radio1", `  mode = "11n/ax"` + "\n", "", func(t *testing.T, srv *glinettest.Server) {
			setWifi(t, srv, "radio1", map[string]any{"htmode": "40", "htmodes": map[string]any{"11g/n/ax": 40, "11n/ax": 20, "auto": true}}, "", nil)
		}},
		{"channel", "radio0", "  channel = 1\n", `(?s)Channel Not Supported.*` + wrapped(`The Wi-Fi radio radio0 does not support the channel 1. It supports 0 (automatic) and: 36, 40,`), nil},
		{"DFS channel", "radio0", "  channel = 52\n", `(?s)Channel Not Supported.*` + wrapped(`The channel 52 of the Wi-Fi radio radio0 is a DFS channel, and the router does not support DFS.`), func(t *testing.T, srv *glinettest.Server) {
			config, _ := wifiRadios(t, srv)
			config.DFSSupport = false
			srv.Wifi.SetConfig(json.RawMessage(mustJSON(t, config)))
		}},
		{"PSC option of a 5G radio", "radio0", "  psc_channels_only = false\n", `(?s)PSC Option Not Supported.*` + wrapped(`The Wi-Fi radio radio0 has the band 5G; psc_channels_only is only for a radio with the band 6G.`), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			if tt.setup != nil {
				tt.setup(t, srv)
			}
			err := tt.err
			if err == "" {
				err = `(?s)Channel Width Not Supported.*` + wrapped(`does not support the channel width "40" with the mode "11n/ax". It supports: 20, auto.`)
			}
			settings := recordCalls(t, srv, "wifi", "set_config")
			txPower := recordCalls(t, srv, "wifi", "set_txpower")

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + wifiRadioConfig(tt.device, tt.attributes+"  tx_power = \"Low\"\n"),
						ExpectError: regexp.MustCompile(err),
					},
				},
			})
			if got := append(settings.take(), txPower.take()...); len(got) != 0 {
				t.Errorf("the router got the writes %q", got)
			}
		})
	}
}

func TestWifiRadioResourceInvalidConfig(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)

	for _, tt := range []struct{ attributes, err string }{
		{"  channel = -1\n", `Attribute channel value must be at least 0`},
		{`  channel_width = "60"` + "\n", `Attribute channel_width value must be one of`},
		{`  tx_power = "Ultra"` + "\n", `Attribute tx_power value must be one of`},
		{`  mode = ""` + "\n", `Attribute mode string length must be at least 1`},
		{"", `Attribute name string length must be at least 1`},
	} {
		name := "radio0"
		if tt.attributes == "" {
			name = ""
		}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      fakeRouterConfig(srv) + wifiRadioConfig(name, tt.attributes),
					ExpectError: regexp.MustCompile(tt.err),
				},
			},
		})
	}
}

func TestWifiRadioResourceDrift(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	calls := recordCalls(t, srv, "wifi", "set_config")
	config := fakeRouterConfig(srv) + wifiRadioConfig("radio0", "  channel = 36\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					calls.take()
					setWifi(t, srv, "radio0", map[string]any{"channel": 149, "txpower": "Low"}, "", nil)
				},
				Config:            config,
				ConfigPlanChecks:  expectResourceAction(wifiRadioAddress, plancheck.ResourceActionUpdate),
				ConfigStateChecks: radioValues{channel: 36, channelWidth: "40", mode: "11a/n/ac/ax", power: "Low", randomBSSID: true}.state("radio0", "5G"),
				Check:             calls.expect(`{` + radio0Iface + `,"hwmode":"11a/n/ac/ax","txpower":"Low","random_bssid":true,"channel":36,"htmode":"40"}`),
			},
			{
				PreConfig:         func() { setWifi(t, srv, "radio0", map[string]any{"txpower": "High"}, "", nil) },
				Config:            config,
				ConfigPlanChecks:  expectEmptyPlan,
				ConfigStateChecks: radioValues{channel: 36, channelWidth: "40", mode: "11a/n/ac/ax", power: "High", randomBSSID: true}.state("radio0", "5G"),
				Check:             calls.expect(),
			},
		},
	})
}

func TestWifiRadioResourceDestroy(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	settings := recordCalls(t, srv, "wifi", "set_config")
	txPower := recordCalls(t, srv, "wifi", "set_txpower")
	values := radio0Values
	values.power = "Low"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeRouterConfig(srv) + wifiRadioConfig("radio0", `  tx_power = "Low"`+"\n"),
				Check:  txPower.expect(`{"device":"radio0","txpower":"Low"}`),
			},
			{
				Config: fakeRouterConfig(srv),
				Check: resource.ComposeTestCheckFunc(
					settings.expect(),
					txPower.expect(),
					expectRouterRadio(t, srv, "radio0", values),
				),
			},
		},
	})
}

func TestWifiRadioResourceRouterErrors(t *testing.T) {
	parallel(t)

	for _, tt := range []struct{ method, attributes, err string }{
		{"get_config", "  channel = 36\n", `(?s)Unable to Read Wi-Fi Configuration.*Method not found`},
		{"set_config", "  channel = 36\n", `(?s)Unable to Set Wi-Fi Radio.*Method not found`},
		{"set_txpower", `  tx_power = "Low"` + "\n", `(?s)Unable to Set Wi-Fi Transmit Power.*Method not found`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			before := srv.Wifi.Config()
			failRouterMethod(srv, "wifi", tt.method)()

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fakeRouterConfig(srv) + wifiRadioConfig("radio0", tt.attributes),
						ExpectError: regexp.MustCompile(tt.err),
					},
				},
			})
			expectJSONEqual(t, srv.Wifi.Config(), string(before))
		})
	}
}
