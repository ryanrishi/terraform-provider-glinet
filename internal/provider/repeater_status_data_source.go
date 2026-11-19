package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &repeaterStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &repeaterStatusDataSource{}
)

func NewRepeaterStatusDataSource() datasource.DataSource {
	return &repeaterStatusDataSource{}
}

type repeaterStatusDataSource struct {
	client *glinet.Client
}

type repeaterStatusModel struct {
	State     types.String     `tfsdk:"state"`
	StateCode int              `tfsdk:"state_code"`
	Running   bool             `tfsdk:"running"`
	FailType  types.String     `tfsdk:"fail_type"`
	SSID      types.String     `tfsdk:"ssid"`
	BSSID     types.String     `tfsdk:"bssid"`
	Band      types.String     `tfsdk:"band"`
	Channel   types.Int64      `tfsdk:"channel"`
	HTMode    types.String     `tfsdk:"htmode"`
	DFS       bool             `tfsdk:"dfs"`
	Signal    types.Int64      `tfsdk:"signal"`
	Device    types.String     `tfsdk:"device"`
	Network   types.String     `tfsdk:"network"`
	MAC       types.String     `tfsdk:"mac"`
	Connected types.String     `tfsdk:"connected"`
	EAP       bool             `tfsdk:"eap"`
	Portal    bool             `tfsdk:"portal"`
	PortalURL types.String     `tfsdk:"portal_url"`
	BareMode  bool             `tfsdk:"bare_mode"`
	Protocol  types.String     `tfsdk:"protocol"`
	IPv4      *uplinkIPv4Model `tfsdk:"ipv4"`
}

type uplinkIPv4Model struct {
	IP      types.String `tfsdk:"ip"`
	Gateway types.String `tfsdk:"gateway"`
	DNS     types.List   `tfsdk:"dns"`
}

func (d *repeaterStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repeater_status"
}

func (d *repeaterStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the state of the repeater, the Wi-Fi uplink of the router (`repeater.get_status`). " +
			"The data source does not put the Wi-Fi key, the EAP identity and the `wpa_status` values of the uplink network in the state. " +
			"The values change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"state": computedString("State of the repeater (`state_s`), for example `connected`, `connecting` or `failed`. " +
				"Null when the router gives no state name, for example when the repeater is not connected."),
			"state_code": computedInt64("State of the repeater as a number (`state`): `0` not connected and `2` connected. " +
				"The router can give other values, for which the meaning is not known."),
			"running":   computedBool("Whether the repeater runs (`running`)."),
			"fail_type": computedString("Reason of the last failed connection (`fail_type`), for example `key` (wrong key) or `not-found` (network not found). Null when the router gives no reason."),
			"ssid":      computedString("SSID of the uplink network. Null when the repeater has no network."),
			"bssid":     computedString("BSSID of the access point of the uplink network. Null when the router gives no BSSID."),
			"band":      computedString("Band of the uplink, for example `2g` or `5g`. Null when the router gives no band."),
			"channel":   computedInt64("Wi-Fi channel of the uplink. Null when the router gives no channel."),
			"htmode":    computedString("HT mode of the uplink (`htmode`), for example `HT40`. Null when the router gives no HT mode."),
			"dfs":       computedBool("Whether the channel of the uplink is a DFS channel (`dfs`)."),
			"signal":    computedInt64("Signal strength of the uplink in dBm (`signal`). Null when the router gives no signal."),
			"device":    computedString("Radio of the repeater (`device`), for example `radio0`. Null when the router gives no radio."),
			"network":   computedString("Network interface of the repeater (`network`), for example `wwan`. Null when the router gives no interface."),
			"mac": computedString("MAC address of the repeater interface (`macaddr`). " +
				"Null when the router gives no MAC address."),
			"connected": computedString("Time since the repeater connected, as the router gives it (`connected`), for example `40m,13s`. " +
				"Null when the router gives no time."),
			"eap": computedBool("Whether the uplink network uses enterprise (EAP) authentication (`eap`). " +
				"This value comes from the web UI code and was not seen on a router."),
			"portal": computedBool("Whether the uplink network has a captive portal (`portal`)."),
			"portal_url": computedString("URL of the captive portal (`portal_url`). " +
				"This value comes from the web UI code and was not seen on a router. Null when the router gives no URL."),
			"bare_mode": computedBool("Whether the repeater is in the captive portal login mode of the web UI (`bare_mode`)."),
			"protocol": computedString("Address protocol of the uplink network (`protocol` of `config`): `dhcp` or `static`. " +
				"Null when the router gives no protocol."),
			"ipv4": computedObject("IPv4 address of the uplink (`ipv4`). Null when the router gives no IPv4 address.", uplinkIPv4Attributes()),
		},
	}
}

func uplinkIPv4Attributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"ip":      computedString("IPv4 address with the prefix length (`ip`), for example `192.0.2.10/24`. Null when the router gives no address."),
		"gateway": computedString("IPv4 gateway (`gateway`). Null when the router gives no gateway."),
		"dns": schema.ListAttribute{
			Description: "DNS servers (`dns`).",
			ElementType: types.StringType,
			Computed:    true,
		},
	}
}

func (d *repeaterStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *repeaterStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.Repeater.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Repeater Status", err.Error())
		return
	}

	state := repeaterStatusModel{
		State:     stringOrNull(status.StateS),
		StateCode: status.State,
		Running:   status.Running,
		FailType:  stringOrNull(status.FailType),
		SSID:      stringOrNull(status.SSID),
		BSSID:     stringOrNull(status.BSSID),
		Band:      stringOrNull(status.Band),
		Channel:   channelOrNull(status.Channel),
		HTMode:    stringOrNull(status.HTMode),
		DFS:       status.DFS,
		Signal:    int64OrNull(status.Signal),
		Device:    stringOrNull(status.Device),
		Network:   stringOrNull(status.Network),
		MAC:       stringOrNull(status.MACAddr),
		Connected: stringOrNull(status.Connected),
		EAP:       status.EAP,
		Portal:    status.Portal,
		PortalURL: stringOrNull(status.PortalURL),
		BareMode:  status.BareMode,
		Protocol:  types.StringNull(),
		IPv4:      uplinkIPv4(ctx, status.IPv4, &resp.Diagnostics),
	}
	if status.Config != nil {
		state.Protocol = stringOrNull(status.Config.Protocol)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func uplinkIPv4(ctx context.Context, ipv4 *glinet.UplinkIPv4, diags *diag.Diagnostics) *uplinkIPv4Model {
	if ipv4 == nil {
		return nil
	}
	return &uplinkIPv4Model{
		IP:      stringOrNull(ipv4.IP),
		Gateway: stringOrNull(ipv4.Gateway),
		DNS:     stringList(ctx, nonNil(ipv4.DNS), diags),
	}
}

// nonNil returns an empty slice for nil, so that a list that the router leaves out is empty in the state and not null.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
