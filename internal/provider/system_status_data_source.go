package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &systemStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &systemStatusDataSource{}
)

func NewSystemStatusDataSource() datasource.DataSource {
	return &systemStatusDataSource{}
}

type systemStatusDataSource struct {
	client *glinet.Client
}

type systemStatusModel struct {
	LANIP           string                     `tfsdk:"lan_ip"`
	LANNetmask      string                     `tfsdk:"lan_netmask"`
	GuestIP         string                     `tfsdk:"guest_ip"`
	GuestNetmask    string                     `tfsdk:"guest_netmask"`
	Uptime          float64                    `tfsdk:"uptime"`
	LoadAverage     []float64                  `tfsdk:"load_average"`
	MemoryTotal     int64                      `tfsdk:"memory_total"`
	MemoryFree      int64                      `tfsdk:"memory_free"`
	MemoryBuffCache int64                      `tfsdk:"memory_buff_cache"`
	FlashTotal      int64                      `tfsdk:"flash_total"`
	FlashFree       int64                      `tfsdk:"flash_free"`
	FlashApp        int64                      `tfsdk:"flash_app"`
	CPUTemperature  types.Int64                `tfsdk:"cpu_temperature"`
	DDNSEnabled     bool                       `tfsdk:"ddns_enabled"`
	IPv6Enabled     bool                       `tfsdk:"ipv6_enabled"`
	NATEnabled      bool                       `tfsdk:"nat_enabled"`
	Mode            int64                      `tfsdk:"mode"`
	Timestamp       int64                      `tfsdk:"timestamp"`
	TZOffset        string                     `tfsdk:"tzoffset"`
	ClientCounts    []clientCountModel         `tfsdk:"client_counts"`
	Networks        []networkStatusModel       `tfsdk:"networks"`
	Services        []serviceStatusModel       `tfsdk:"services"`
	WifiInterfaces  []wifiInterfaceStatusModel `tfsdk:"wifi_interfaces"`
}

type clientCountModel struct {
	CableTotal    int64 `tfsdk:"cable_total"`
	WirelessTotal int64 `tfsdk:"wireless_total"`
}

type networkStatusModel struct {
	Interface string `tfsdk:"interface"`
	Up        bool   `tfsdk:"up"`
	Online    bool   `tfsdk:"online"`
}

type serviceStatusModel struct {
	Name    string      `tfsdk:"name"`
	Status  int         `tfsdk:"status"`
	PeerID  types.Int64 `tfsdk:"peer_id"`
	GroupID types.Int64 `tfsdk:"group_id"`
}

type wifiInterfaceStatusModel struct {
	Name       string      `tfsdk:"name"`
	Band       string      `tfsdk:"band"`
	SSID       string      `tfsdk:"ssid"`
	Channel    types.Int64 `tfsdk:"channel"`
	Up         bool        `tfsdk:"up"`
	Guest      bool        `tfsdk:"guest"`
	Encryption string      `tfsdk:"encryption"`
	Hidden     bool        `tfsdk:"hidden"`
	MLD        bool        `tfsdk:"mld"`
}

func (d *systemStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_status"
}

func (d *systemStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the runtime status of the router (`system.get_status`). " +
			"Most values change while the router runs, so each read can give different values. " +
			"The Wi-Fi keys are not available.",
		Attributes: map[string]schema.Attribute{
			"lan_ip":        computedString("IPv4 address of the router in the LAN."),
			"lan_netmask":   computedString("Netmask of the LAN."),
			"guest_ip":      computedString("IPv4 address of the router in the guest network."),
			"guest_netmask": computedString("Netmask of the guest network."),
			"uptime": schema.Float64Attribute{
				Description: "Time since the start of the router, in seconds.",
				Computed:    true,
			},
			"load_average": schema.ListAttribute{
				Description: "Load averages of the last 1, 5 and 15 minutes.",
				ElementType: types.Float64Type,
				Computed:    true,
			},
			"memory_total":      computedInt64("Total memory, in bytes."),
			"memory_free":       computedInt64("Free memory, in bytes."),
			"memory_buff_cache": computedInt64("Memory used for buffers and cache, in bytes."),
			"flash_total":       computedInt64("Size of the flash, in bytes."),
			"flash_free":        computedInt64("Free flash, in bytes."),
			"flash_app":         computedInt64("Flash used by applications, in bytes, as the router gives it."),
			"cpu_temperature":   computedInt64("CPU temperature in degrees Celsius (`cpu.temperature`). Null when the router gives none."),
			"ddns_enabled":      computedBool("Whether the GL.iNet DDNS service is enabled."),
			"ipv6_enabled":      computedBool("Whether IPv6 is enabled."),
			"nat_enabled":       computedBool("Whether NAT is enabled (`netnat_enabled`)."),
			"mode":              computedInt64("Operation mode of the router, as the router gives it. `0` is the router mode."),
			"timestamp":         computedInt64("Clock of the router, as a Unix time in seconds."),
			"tzoffset":          computedString("Current offset from UTC, for example `+0100`."),
			"client_counts": computedList("Numbers of connected clients (`client`).", map[string]schema.Attribute{
				"cable_total":    computedInt64("Number of clients on a cable."),
				"wireless_total": computedInt64("Number of clients on Wi-Fi."),
			}),
			"networks": computedList("Network interfaces (`network`).", map[string]schema.Attribute{
				"interface": computedString("Name of the interface, for example `wan` or `wwan`."),
				"up":        computedBool("Whether the interface is up."),
				"online":    computedBool("Whether the router reports the interface as online."),
			}),
			"services": computedList("Services (`service`).", map[string]schema.Attribute{
				"name":     computedString("Name of the service, for example `wgclient` or `adguard`."),
				"status":   computedInt64("Status of the service, as the router gives it. `1` means that the service runs."),
				"peer_id":  computedInt64("ID of the peer that the service uses, for example the WireGuard client configuration of `wgclient`. Null when the router gives none."),
				"group_id": computedInt64("ID of the group of `peer_id`. Null when the router gives none."),
			}),
			"wifi_interfaces": computedList("Wi-Fi interfaces (`wifi`).", map[string]schema.Attribute{
				"name":       computedString("Name of the interface, for example `default_radio0` or `guest5g`."),
				"band":       computedString("Band, for example `2G` or `5G`."),
				"ssid":       computedString("SSID."),
				"channel":    computedInt64("Channel. Null when the router gives none."),
				"up":         computedBool("Whether the interface is up."),
				"guest":      computedBool("Whether the interface is for the guest network."),
				"encryption": computedString("Encryption, for example `psk2`."),
				"hidden":     computedBool("Whether the interface hides its SSID."),
				"mld":        computedBool("Whether the interface is a Wi-Fi 7 multi-link device (MLD)."),
			}),
		},
	}
}

func (d *systemStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *systemStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.System.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read System Status", err.Error())
		return
	}

	sys := status.System
	state := systemStatusModel{
		LANIP:           sys.LanIP,
		LANNetmask:      sys.LanNetmask,
		GuestIP:         sys.GuestIP,
		GuestNetmask:    sys.GuestNetmask,
		Uptime:          sys.Uptime,
		LoadAverage:     append([]float64{}, sys.LoadAverage...),
		MemoryTotal:     sys.MemoryTotal,
		MemoryFree:      sys.MemoryFree,
		MemoryBuffCache: sys.MemoryBuffCache,
		FlashTotal:      sys.FlashTotal,
		FlashFree:       sys.FlashFree,
		FlashApp:        sys.FlashApp,
		CPUTemperature:  temperatureOrNull(sys.CPU.Temperature),
		DDNSEnabled:     sys.DDNSEnabled,
		IPv6Enabled:     sys.Ipv6Enabled,
		NATEnabled:      sys.NetNATEnabled,
		Mode:            int64(sys.Mode),
		Timestamp:       sys.Timestamp,
		TZOffset:        sys.TZOffset,
		ClientCounts:    []clientCountModel{},
		Networks:        []networkStatusModel{},
		Services:        []serviceStatusModel{},
		WifiInterfaces:  []wifiInterfaceStatusModel{},
	}
	for _, c := range status.Client {
		state.ClientCounts = append(state.ClientCounts, clientCountModel{CableTotal: int64(c.CableTotal), WirelessTotal: int64(c.WirelessTotal)})
	}
	for _, n := range status.Network {
		state.Networks = append(state.Networks, networkStatusModel{Interface: n.Interface, Up: n.Up, Online: n.Online})
	}
	for _, s := range status.Service {
		state.Services = append(state.Services, serviceStatusModel{
			Name:    s.Name,
			Status:  s.Status,
			PeerID:  idOrNull(s.PeerID),
			GroupID: idOrNull(s.GroupID),
		})
	}
	for _, w := range status.Wifi {
		state.WifiInterfaces = append(state.WifiInterfaces, wifiInterfaceStatusModel{
			Name:       w.Name,
			Band:       w.Band,
			SSID:       w.Ssid,
			Channel:    channelOrNull(w.Channel),
			Up:         w.Up,
			Guest:      w.Guest,
			Encryption: w.Encryption,
			Hidden:     w.Hidden,
			MLD:        w.MLD,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// temperatureOrNull returns null for 0, which the client gives when the router has no CPU temperature.
// A running CPU is not at 0 degrees Celsius.
func temperatureOrNull(celsius int) types.Int64 {
	if celsius == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(celsius))
}

// idOrNull returns null for 0, which the client gives when the router leaves out the ID.
func idOrNull(id int) types.Int64 {
	if id == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(id))
}
