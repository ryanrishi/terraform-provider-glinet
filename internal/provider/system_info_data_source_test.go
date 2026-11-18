package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	systemInfoAddress          = "data.glinet_system_info.test"
	systemInfoDataSourceConfig = `
data "glinet_system_info" "test" {}
`
)

func TestSystemInfoDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+systemInfoDataSourceConfig, expectAttributes(systemInfoAddress, realRouterSystemInfoChecks()))
		return
	}

	srv := glinettest.NewServer(t)
	srv.System.SetInfo(json.RawMessage(`{
		"board_info": {
			"architecture": "ARMv8 Processor rev 4",
			"hostname": "router-1",
			"kernel_version": "5.4.164",
			"model": "GL Technologies, Inc. AXT1800",
			"openwrt_version": "OpenWrt 23.05"
		},
		"country_code": "DE",
		"cpu_num": 4,
		"ddns": "ddns-id",
		"device_type": 1,
		"firmware_date": "2025-03-19 10:28:59",
		"firmware_type": "release8",
		"firmware_version": "4.7.0",
		"hardware_feature": {
			"bluetooth": true,
			"build_in_modem": "",
			"fan": true,
			"gps": false,
			"hwnat": true,
			"lan": "eth1 eth2",
			"mcu": false,
			"microsd": "mmc",
			"modem_reset": 2,
			"nand": true,
			"noled": false,
			"nowds": true,
			"radio": "radio0 radio1",
			"reset_button": "gpio-50",
			"rs485": false,
			"screen": true,
			"simo": true,
			"slot": "single",
			"submodel": "",
			"switch_button": "gpio9",
			"usb": "1-1,2-1",
			"usb3": "2-1",
			"usb_power": "gpio-7",
			"usb_reset": "usb_power,1",
			"wan": "eth0"
		},
		"hardware_version": "",
		"hidden_features": ["guestip"],
		"mac": "02:00:00:00:00:01",
		"model": "axt1800",
		"sn": "serial",
		"sn_bak": "serial-bak",
		"software_feature": {
			"adguard": true,
			"bark": false,
			"cellular_upgrade": false,
			"ids_ips": true,
			"ipv6": true,
			"ksmbd": false,
			"mlo": false,
			"nas": true,
			"passthrough": false,
			"repeater_eap": true,
			"secondwan": true,
			"sms_forward": true,
			"tor": false,
			"vpn": true
		},
		"vendor": "GL.iNet"
	}`))

	readDataSource(t, fakeRouterConfig(srv)+systemInfoDataSourceConfig, expectAttributes(systemInfoAddress, map[string]knownvalue.Check{
		"vendor":           knownvalue.StringExact("GL.iNet"),
		"model":            knownvalue.StringExact("axt1800"),
		"mac":              knownvalue.StringExact("02:00:00:00:00:01"),
		"firmware_version": knownvalue.StringExact("4.7.0"),
		"firmware_type":    knownvalue.StringExact("release8"),
		"firmware_date":    knownvalue.StringExact("2025-03-19 10:28:59"),
		"hardware_version": knownvalue.StringExact(""),
		"device_type":      knownvalue.Int64Exact(1),
		"country_code":     knownvalue.StringExact("DE"),
		"cpu_count":        knownvalue.Int64Exact(4),
		"board": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"architecture":    knownvalue.StringExact("ARMv8 Processor rev 4"),
			"hostname":        knownvalue.StringExact("router-1"),
			"kernel_version":  knownvalue.StringExact("5.4.164"),
			"model":           knownvalue.StringExact("GL Technologies, Inc. AXT1800"),
			"openwrt_version": knownvalue.StringExact("OpenWrt 23.05"),
		}),
		"hardware_features": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"bluetooth":      knownvalue.Bool(true),
			"built_in_modem": knownvalue.StringExact(""),
			"fan":            knownvalue.Bool(true),
			"gps":            knownvalue.Bool(false),
			"hardware_nat":   knownvalue.Bool(true),
			"lan":            knownvalue.StringExact("eth1 eth2"),
			"mcu":            knownvalue.Bool(false),
			"microsd":        knownvalue.StringExact("mmc"),
			"modem_reset":    knownvalue.Int64Exact(2),
			"nand":           knownvalue.Bool(true),
			"no_led":         knownvalue.Bool(false),
			"no_wds":         knownvalue.Bool(true),
			"radio":          knownvalue.StringExact("radio0 radio1"),
			"reset_button":   knownvalue.StringExact("gpio-50"),
			"rs485":          knownvalue.Bool(false),
			"screen":         knownvalue.Bool(true),
			"simo":           knownvalue.Bool(true),
			"slot":           knownvalue.StringExact("single"),
			"submodel":       knownvalue.StringExact(""),
			"switch_button":  knownvalue.StringExact("gpio9"),
			"usb":            knownvalue.StringExact("1-1,2-1"),
			"usb3":           knownvalue.StringExact("2-1"),
			"usb_power":      knownvalue.StringExact("gpio-7"),
			"usb_reset":      knownvalue.StringExact("usb_power,1"),
			"wan":            knownvalue.StringExact("eth0"),
		}),
		"hidden_features": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("guestip")}),
		"software_features": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"adguard":          knownvalue.Bool(true),
			"bark":             knownvalue.Bool(false),
			"cellular_upgrade": knownvalue.Bool(false),
			"ids_ips":          knownvalue.Bool(true),
			"ipv6":             knownvalue.Bool(true),
			"ksmbd":            knownvalue.Bool(false),
			"mlo":              knownvalue.Bool(false),
			"nas":              knownvalue.Bool(true),
			"passthrough":      knownvalue.Bool(false),
			"repeater_eap":     knownvalue.Bool(true),
			"second_wan":       knownvalue.Bool(true),
			"sms_forward":      knownvalue.Bool(true),
			"tor":              knownvalue.Bool(false),
			"vpn":              knownvalue.Bool(true),
		}),
	}))
}

func TestSystemInfoDataSourceAbsentFields(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.System.SetInfo(json.RawMessage(`{"model": "axt1800"}`))

	readDataSource(t, fakeRouterConfig(srv)+systemInfoDataSourceConfig, expectAttributes(systemInfoAddress, map[string]knownvalue.Check{
		"model":           knownvalue.StringExact("axt1800"),
		"vendor":          knownvalue.StringExact(""),
		"cpu_count":       knownvalue.Int64Exact(0),
		"hidden_features": knownvalue.ListExact([]knownvalue.Check{}),
		"board": knownvalue.ObjectPartial(map[string]knownvalue.Check{
			"hostname": knownvalue.StringExact(""),
		}),
		"hardware_features": knownvalue.ObjectPartial(map[string]knownvalue.Check{
			"fan": knownvalue.Bool(false),
		}),
	}))
}

func TestSystemInfoDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), systemInfoDataSourceConfig, "system", "get_info", "Unable to Read System Info")
}

// realRouterSystemInfoChecks checks only types and that the values that each router has are not empty.
func realRouterSystemInfoChecks() map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"vendor":           nonEmptyString,
		"model":            nonEmptyString,
		"mac":              nonEmptyString,
		"firmware_version": nonEmptyString,
		"firmware_type":    nonEmptyString,
		"firmware_date":    nonEmptyString,
		"hardware_version": anyString,
		"device_type":      anyInt64,
		"country_code":     anyString,
		"cpu_count":        positiveInt64,
		"board": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"architecture":    nonEmptyString,
			"hostname":        nonEmptyString,
			"kernel_version":  nonEmptyString,
			"model":           nonEmptyString,
			"openwrt_version": nonEmptyString,
		}),
		"hardware_features": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"bluetooth":      anyBool,
			"built_in_modem": anyString,
			"fan":            anyBool,
			"gps":            anyBool,
			"hardware_nat":   anyBool,
			"lan":            anyString,
			"mcu":            anyBool,
			"microsd":        anyString,
			"modem_reset":    anyInt64,
			"nand":           anyBool,
			"no_led":         anyBool,
			"no_wds":         anyBool,
			"radio":          anyString,
			"reset_button":   anyString,
			"rs485":          anyBool,
			"screen":         anyBool,
			"simo":           anyBool,
			"slot":           anyString,
			"submodel":       anyString,
			"switch_button":  anyString,
			"usb":            anyString,
			"usb3":           anyString,
			"usb_power":      anyString,
			"usb_reset":      anyString,
			"wan":            anyString,
		}),
		"hidden_features": knownvalue.ListPartial(map[int]knownvalue.Check{}),
		"software_features": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"adguard":          anyBool,
			"bark":             anyBool,
			"cellular_upgrade": anyBool,
			"ids_ips":          anyBool,
			"ipv6":             anyBool,
			"ksmbd":            anyBool,
			"mlo":              anyBool,
			"nas":              anyBool,
			"passthrough":      anyBool,
			"repeater_eap":     anyBool,
			"second_wan":       anyBool,
			"sms_forward":      anyBool,
			"tor":              anyBool,
			"vpn":              anyBool,
		}),
	}
}
