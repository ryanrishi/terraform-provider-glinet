package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &repeaterSavedNetworksDataSource{}
	_ datasource.DataSourceWithConfigure = &repeaterSavedNetworksDataSource{}
)

func NewRepeaterSavedNetworksDataSource() datasource.DataSource {
	return &repeaterSavedNetworksDataSource{}
}

type repeaterSavedNetworksDataSource struct {
	client *glinet.Client
}

type repeaterSavedNetworksModel struct {
	Networks []repeaterSavedNetworkModel `tfsdk:"networks"`
}

type repeaterSavedNetworkModel struct {
	SSID       string            `tfsdk:"ssid"`
	BSSID      types.String      `tfsdk:"bssid"`
	Protocol   types.String      `tfsdk:"protocol"`
	IP         types.String      `tfsdk:"ip"`
	Netmask    types.String      `tfsdk:"netmask"`
	Gateway    types.String      `tfsdk:"gateway"`
	DNS        types.List        `tfsdk:"dns"`
	Disguise   bool              `tfsdk:"disguise"`
	MACAddress *repeaterMACModel `tfsdk:"mac_address"`
	TTL        types.Int64       `tfsdk:"ttl"`
	TTLIPv6    types.Int64       `tfsdk:"ttl_ipv6"`
	MTU        types.Int64       `tfsdk:"mtu"`
	Manual     bool              `tfsdk:"manual"`
	AutoPortal bool              `tfsdk:"auto_portal"`
}

type repeaterMACModel struct {
	Mode   string       `tfsdk:"mode"`
	MAC    types.String `tfsdk:"mac"`
	Update types.String `tfsdk:"update"`
	Period types.Int64  `tfsdk:"period"`
}

func (d *repeaterSavedNetworksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repeater_saved_networks"
}

func (d *repeaterSavedNetworksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the saved networks of the repeater, the Wi-Fi uplink of the router (`repeater.get_saved_ap_list`). " +
			"The router also gives the Wi-Fi key and the EAP identity of each network. " +
			"The data source does not put them in the state, because the state is not a safe place for the credentials of the uplink networks.",
		Attributes: map[string]schema.Attribute{
			"networks": computedList("Saved networks (`res`).", map[string]schema.Attribute{
				"ssid":     computedString("SSID of the network."),
				"bssid":    computedString("BSSID of the one access point that the repeater uses for this network (`bssid`). Null when the network has no BSSID lock."),
				"protocol": computedString("Address protocol (`protocol`): `dhcp` or `static`. Null when the router gives no protocol, which some saved networks do."),
				"ip":       computedString("Static IPv4 address (`ip`). Null when the network has no static address."),
				"netmask":  computedString("Netmask of the static address (`netmask`). Null when the network has no static address."),
				"gateway":  computedString("Gateway of the static address (`gateway`). Null when the network has no static address."),
				"dns": schema.ListAttribute{
					Description: "DNS servers of the static address (`dns`). Empty when the network has no static address.",
					ElementType: types.StringType,
					Computed:    true,
				},
				"disguise": computedBool("Whether the disguise option of the web UI is enabled (`disguise`)."),
				"mac_address": computedObject("MAC address that the repeater uses on this network (`macaddr`). Null when the router gives no MAC address setting.", map[string]schema.Attribute{
					"mode": computedString("`default` (the MAC address of the router), `clone` (the MAC address of a client) or `random` (a random MAC address)."),
					"mac":  computedString("MAC address for the modes `clone` and `random` (`macaddr`). Null when the router gives no MAC address."),
					"update": computedString("When the router changes a random MAC address (`update`): `none`, `time` (every `period` hours) or `reboot`. " +
						"Null when the router gives no value."),
					"period": computedInt64("Period in hours for `update` `time` (`period`). Null when the router gives no period."),
				}),
				"ttl":         computedInt64("IPv4 TTL (`ttl`). Null when it is not set."),
				"ttl_ipv6":    computedInt64("IPv6 hop limit (`ttl_ipv6`). Null when it is not set."),
				"mtu":         computedInt64("MTU (`mtu`). Null when it is not set."),
				"manual":      computedBool("The `manual` value of the router. Its meaning is not known."),
				"auto_portal": computedBool("Whether the captive portal option of the web UI is enabled (`auto_portal`)."),
			}),
		},
	}
}

func (d *repeaterSavedNetworksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *repeaterSavedNetworksDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	networks, err := d.client.Repeater.ListSavedNetworks(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Repeater Saved Networks", err.Error())
		return
	}

	state := repeaterSavedNetworksModel{Networks: []repeaterSavedNetworkModel{}}
	for _, n := range networks {
		network := repeaterSavedNetworkModel{
			SSID:       n.SSID,
			BSSID:      stringOrNull(n.BSSID),
			Protocol:   stringOrNull(n.Protocol),
			IP:         stringOrNull(n.IP),
			Netmask:    stringOrNull(n.Netmask),
			Gateway:    stringOrNull(n.Gateway),
			DNS:        stringList(ctx, nonNil(n.DNS), &resp.Diagnostics),
			Disguise:   n.Disguise,
			TTL:        int64OrNull(n.TTL),
			TTLIPv6:    int64OrNull(n.TTLIPv6),
			MTU:        int64OrNull(n.MTU),
			Manual:     n.Manual,
			AutoPortal: n.AutoPortal,
		}
		if n.MACAddr != nil {
			network.MACAddress = &repeaterMACModel{
				Mode:   n.MACAddr.Mode,
				MAC:    stringOrNull(n.MACAddr.MAC),
				Update: stringOrNull(n.MACAddr.Update),
				Period: int64OrNull(n.MACAddr.Period),
			}
		}
		state.Networks = append(state.Networks, network)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
