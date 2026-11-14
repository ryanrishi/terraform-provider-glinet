package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &systemInfoDataSource{}
	_ datasource.DataSourceWithConfigure = &systemInfoDataSource{}
)

func NewSystemInfoDataSource() datasource.DataSource {
	return &systemInfoDataSource{}
}

type systemInfoDataSource struct {
	client *glinet.Client
}

// The nested models have the fields of the client types in the same order, so that a type conversion copies them.
type systemInfoModel struct {
	Vendor           string               `tfsdk:"vendor"`
	Model            string               `tfsdk:"model"`
	MAC              string               `tfsdk:"mac"`
	FirmwareVersion  string               `tfsdk:"firmware_version"`
	FirmwareType     string               `tfsdk:"firmware_type"`
	FirmwareDate     string               `tfsdk:"firmware_date"`
	HardwareVersion  string               `tfsdk:"hardware_version"`
	DeviceType       int                  `tfsdk:"device_type"`
	CountryCode      string               `tfsdk:"country_code"`
	CPUNum           int                  `tfsdk:"cpu_count"`
	Board            boardInfoModel       `tfsdk:"board"`
	HardwareFeatures hardwareFeatureModel `tfsdk:"hardware_features"`
	SoftwareFeatures softwareFeatureModel `tfsdk:"software_features"`
}

type boardInfoModel struct {
	Architecture   string `tfsdk:"architecture"`
	Hostname       string `tfsdk:"hostname"`
	KernelVersion  string `tfsdk:"kernel_version"`
	Model          string `tfsdk:"model"`
	OpenWrtVersion string `tfsdk:"openwrt_version"`
}

type hardwareFeatureModel struct {
	Bluetooth    bool   `tfsdk:"bluetooth"`
	BuildInModem string `tfsdk:"built_in_modem"`
	Fan          bool   `tfsdk:"fan"`
	GPS          bool   `tfsdk:"gps"`
	HWNAT        bool   `tfsdk:"hardware_nat"`
	LAN          string `tfsdk:"lan"`
	MCU          bool   `tfsdk:"mcu"`
	MicroSD      string `tfsdk:"microsd"`
	ModemReset   int    `tfsdk:"modem_reset"`
	NAND         bool   `tfsdk:"nand"`
	NoLED        bool   `tfsdk:"no_led"`
	NoWDS        bool   `tfsdk:"no_wds"`
	Radio        string `tfsdk:"radio"`
	ResetButton  string `tfsdk:"reset_button"`
	RS485        bool   `tfsdk:"rs485"`
	Slot         string `tfsdk:"slot"`
	Submodel     string `tfsdk:"submodel"`
	SwitchButton string `tfsdk:"switch_button"`
	USB          string `tfsdk:"usb"`
	USB3         string `tfsdk:"usb3"`
	USBReset     string `tfsdk:"usb_reset"`
	WAN          string `tfsdk:"wan"`
}

type softwareFeatureModel struct {
	AdGuard         bool `tfsdk:"adguard"`
	Bark            bool `tfsdk:"bark"`
	CellularUpgrade bool `tfsdk:"cellular_upgrade"`
	IDSIPS          bool `tfsdk:"ids_ips"`
	IPv6            bool `tfsdk:"ipv6"`
	KSMBD           bool `tfsdk:"ksmbd"`
	MLO             bool `tfsdk:"mlo"`
	NAS             bool `tfsdk:"nas"`
	Passthrough     bool `tfsdk:"passthrough"`
	RepeaterEAP     bool `tfsdk:"repeater_eap"`
	SecondWAN       bool `tfsdk:"second_wan"`
	SMSForward      bool `tfsdk:"sms_forward"`
	Tor             bool `tfsdk:"tor"`
	VPN             bool `tfsdk:"vpn"`
}

func (d *systemInfoDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_info"
}

func (d *systemInfoDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the device and firmware information of the router (`system.get_info`). " +
			"The values change only with a firmware upgrade or a reset. " +
			"The serial numbers and the DDNS device ID are not available.",
		Attributes: map[string]schema.Attribute{
			"vendor":           computedString("Vendor, for example `GL.iNet`."),
			"model":            computedString("Model, for example `axt1800`."),
			"mac":              computedString("MAC address of the device."),
			"firmware_version": computedString("Firmware version, for example `4.7.0`."),
			"firmware_type":    computedString("Firmware type, for example `release8`."),
			"firmware_date":    computedString("Build date of the firmware, for example `2025-03-19 10:28:59`."),
			"hardware_version": computedString("Hardware version. Empty when the router gives none."),
			"device_type":      computedInt64("Device type number, as the router gives it."),
			"country_code":     computedString("Country code, for example `US`."),
			"cpu_count":        computedInt64("Number of CPUs (`cpu_num`)."),
			"board": computedObject("OpenWrt board information (`board_info`).", map[string]schema.Attribute{
				"architecture":    computedString("CPU architecture."),
				"hostname":        computedString("Host name of the router."),
				"kernel_version":  computedString("Linux kernel version."),
				"model":           computedString("Board model."),
				"openwrt_version": computedString("OpenWrt version."),
			}),
			"hardware_features": computedObject("Hardware of the device (`hardware_feature`). "+
				"Most string values are names of devices or interfaces, for example `eth1 eth2` for `lan`.", map[string]schema.Attribute{
				"bluetooth":      computedBool("Whether the device has Bluetooth."),
				"built_in_modem": computedString("Built-in modem (`build_in_modem`)."),
				"fan":            computedBool("Whether the device has a fan."),
				"gps":            computedBool("Whether the device has GPS."),
				"hardware_nat":   computedBool("Whether the device has hardware NAT (`hwnat`)."),
				"lan":            computedString("LAN interfaces."),
				"mcu":            computedBool("Whether the device has an MCU."),
				"microsd":        computedString("microSD card slot."),
				"modem_reset":    computedInt64("Modem reset value, as the router gives it."),
				"nand":           computedBool("Whether the device has NAND flash."),
				"no_led":         computedBool("Whether the device has no LED (`noled`)."),
				"no_wds":         computedBool("Whether the device has no WDS (`nowds`)."),
				"radio":          computedString("Wi-Fi radios, for example `radio0 radio1`."),
				"reset_button":   computedString("Reset button, for example `gpio-50`."),
				"rs485":          computedBool("Whether the device has RS-485."),
				"slot":           computedString("Slot value, as the router gives it, for example `single`."),
				"submodel":       computedString("Submodel."),
				"switch_button":  computedString("Switch button."),
				"usb":            computedString("USB ports."),
				"usb3":           computedString("USB 3 ports."),
				"usb_reset":      computedString("USB power reset."),
				"wan":            computedString("WAN interface."),
			}),
			"software_features": computedObject("Features of the firmware (`software_feature`).", map[string]schema.Attribute{
				"adguard":          computedBool("AdGuard Home."),
				"bark":             computedBool("Bark notifications."),
				"cellular_upgrade": computedBool("Cellular modem upgrade."),
				"ids_ips":          computedBool("Intrusion detection and prevention."),
				"ipv6":             computedBool("IPv6."),
				"ksmbd":            computedBool("ksmbd file server."),
				"mlo":              computedBool("Wi-Fi 7 multi-link operation."),
				"nas":              computedBool("Network storage."),
				"passthrough":      computedBool("Passthrough."),
				"repeater_eap":     computedBool("Repeater to EAP networks."),
				"second_wan":       computedBool("Second WAN (`secondwan`)."),
				"sms_forward":      computedBool("SMS forwarding."),
				"tor":              computedBool("Tor."),
				"vpn":              computedBool("VPN."),
			}),
		},
	}
}

func (d *systemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *systemInfoDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.System.GetInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read System Info", err.Error())
		return
	}

	state := systemInfoModel{
		Vendor:           info.Vendor,
		Model:            info.Model,
		MAC:              info.MAC,
		FirmwareVersion:  info.FirmwareVersion,
		FirmwareType:     info.FirmwareType,
		FirmwareDate:     info.FirmwareDate,
		HardwareVersion:  info.HardwareVersion,
		DeviceType:       info.DeviceType,
		CountryCode:      info.CountryCode,
		CPUNum:           info.CPUNum,
		Board:            boardInfoModel(info.BoardInfo),
		HardwareFeatures: hardwareFeatureModel(info.HardwareFeature),
		SoftwareFeatures: softwareFeatureModel(info.SoftwareFeature),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
