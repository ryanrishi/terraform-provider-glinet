package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &wanStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &wanStatusDataSource{}
)

func NewWANStatusDataSource() datasource.DataSource {
	return &wanStatusDataSource{}
}

type wanStatusDataSource struct {
	client *glinet.Client
}

type wanStatusModel struct {
	CableInserted   bool                `tfsdk:"cable_inserted"`
	CableEnabled    bool                `tfsdk:"cable_enabled"`
	MACCloneEnabled bool                `tfsdk:"mac_clone_enabled"`
	Mode            int                 `tfsdk:"mode"`
	Status          int                 `tfsdk:"status"`
	Interfaces      []wanInterfaceModel `tfsdk:"interfaces"`
}

type wanInterfaceModel struct {
	Interface string `tfsdk:"interface"`
	IP        string `tfsdk:"ip"`
	Netmask   string `tfsdk:"netmask"`
	Prefix    int    `tfsdk:"prefix"`
	Network   string `tfsdk:"network"`
	Broadcast string `tfsdk:"broadcast"`
}

func (d *wanStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wan_status"
}

func (d *wanStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the state of the wired WAN port (`cable.get_status` and `network.check_wan_cable`) " +
			"and the IPv4 addresses of the uplink interfaces (`lan.get_wan_info`). " +
			"The values change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"cable_inserted":    computedBool("Whether a cable is in the WAN port (`cable_inserted` of `network.check_wan_cable`)."),
			"cable_enabled":     computedBool("Whether the wired WAN is enabled (`cable_enabled` of `network.check_wan_cable`)."),
			"mac_clone_enabled": computedBool("Whether MAC address cloning is enabled for the wired WAN (`macclone_enabled` of `network.check_wan_cable`)."),
			"mode":              computedInt64("Mode of the wired WAN, as the router gives it (`mode` of `cable.get_status`). The meaning of the values is not documented."),
			"status": computedInt64("Status of the wired WAN, as the router gives it (`status` of `cable.get_status`). " +
				"The 2023 API reference gives `0` failed, `1` connected, `2` connecting and `3` not connected physically."),
			"interfaces": computedList("Uplink interfaces that have an IPv4 address (`wan_info` of `lan.get_wan_info`).", map[string]schema.Attribute{
				"interface": computedString("Name of the interface, for example `wan` or `wwan`."),
				"ip":        computedString("IPv4 address (`ipaddr`)."),
				"netmask":   computedString("Netmask."),
				"prefix":    computedInt64("Prefix length of the network."),
				"network":   computedString("Network address."),
				"broadcast": computedString("Broadcast address."),
			}),
		},
	}
}

func (d *wanStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *wanStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	port, err := d.client.Cable.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WAN Port Status", err.Error())
		return
	}

	cable, err := d.client.Network.CheckWANCable(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WAN Cable State", err.Error())
		return
	}

	uplinks, err := d.client.LAN.GetWANInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WAN Addresses", err.Error())
		return
	}

	state := wanStatusModel{
		CableInserted:   cable.CableInserted,
		CableEnabled:    cable.CableEnabled,
		MACCloneEnabled: cable.MACCloneEnabled,
		Mode:            port.Mode,
		Status:          port.Status,
		Interfaces:      []wanInterfaceModel{},
	}
	for _, u := range uplinks {
		state.Interfaces = append(state.Interfaces, wanInterfaceModel{
			Interface: u.Interface,
			IP:        u.Info.IPAddr,
			Netmask:   u.Info.Netmask,
			Prefix:    u.Info.Prefix,
			Network:   u.Info.Network,
			Broadcast: u.Info.Broadcast,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
