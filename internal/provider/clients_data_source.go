package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &clientsDataSource{}
	_ datasource.DataSourceWithConfigure = &clientsDataSource{}
)

func NewClientsDataSource() datasource.DataSource {
	return &clientsDataSource{}
}

type clientsDataSource struct {
	client *glinet.Client
}

type clientsModel struct {
	Clients []clientModel `tfsdk:"clients"`
}

type clientModel struct {
	MAC            string       `tfsdk:"mac"`
	Name           string       `tfsdk:"name"`
	Alias          types.String `tfsdk:"alias"`
	Icon           types.String `tfsdk:"icon"`
	Blocked        bool         `tfsdk:"blocked"`
	DownloadLimit  float64      `tfsdk:"download_limit"`
	UploadLimit    float64      `tfsdk:"upload_limit"`
	IP             string       `tfsdk:"ip"`
	IPv6           []string     `tfsdk:"ipv6"`
	Connection     string       `tfsdk:"connection"`
	Type           int64        `tfsdk:"type"`
	Online         bool         `tfsdk:"online"`
	OnlineTime     int64        `tfsdk:"online_time"`
	Remote         bool         `tfsdk:"remote"`
	Rx             int64        `tfsdk:"rx"`
	Tx             int64        `tfsdk:"tx"`
	TotalRx        int64        `tfsdk:"total_rx"`
	TotalTx        int64        `tfsdk:"total_tx"`
	TotalRxInit    int64        `tfsdk:"total_rx_init"`
	TotalTxInit    int64        `tfsdk:"total_tx_init"`
	LastRx         []int64      `tfsdk:"last_rx"`
	LastTx         []int64      `tfsdk:"last_tx"`
	LastUpdateRate int64        `tfsdk:"last_update_rate"`
}

func (d *clientsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_clients"
}

func (d *clientsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the client devices that the router knows, online and offline (`clients.get_list`). " +
			"`mac`, `name`, `alias`, `icon`, `blocked`, `download_limit` and `upload_limit` are names and settings of the client. " +
			"The other attributes change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"clients": computedList("Client devices.", map[string]schema.Attribute{
				"mac":            computedString("MAC address of the client."),
				"name":           computedString("Name of the client, as the router detects it."),
				"alias":          computedString("Name that is set for the client on the router. Null when none is set."),
				"icon":           computedString("Icon that is set for the client on the router (`class` in the router API), for example `laptop`. Null when none is set."),
				"blocked":        computedBool("Whether the router blocks the client."),
				"download_limit": computedFloat64("Download speed limit of the client in KiB/s (`limit_rx` in the router API). `0` is no limit. The value can have a fraction."),
				"upload_limit":   computedFloat64("Upload speed limit of the client in KiB/s (`limit_tx` in the router API). `0` is no limit. The value can have a fraction."),
				"ip":             computedString("IPv4 address of the client. The router also gives it for offline clients."),
				"ipv6": schema.ListAttribute{
					Description: "IPv6 addresses of the client. Empty when the router gives none.",
					Computed:    true,
					ElementType: types.StringType,
				},
				"connection": computedString("Connection of the client (`iface`), for example `cable` or `5G`."),
				"type": computedInt64("Connection type number of the client. On the test router, 0 is `2.4G`, 1 is `5G` and 2 is `cable`. " +
					"The web UI does not show clients of type 5 or 6. When it sorts the clients by network, it puts types 0, 1, 2, 8, 9 and 11 before the other types. " +
					"The meaning of the other numbers is not known."),
				"online": computedBool("Whether the client is online."),
				"online_time": computedInt64("Unix time in seconds that the router gives as `online_time`. " +
					"The web UI sorts the clients by it by default. The exact meaning is not known."),
				"remote": computedBool("Whether the router gives `remote` true for the client. " +
					"The web UI marks such a client with the label `clients.self`. The exact meaning is not known."),
				"rx":       computedInt64("Current receive rate of the client, in bytes per second. The web UI shows it as the download speed."),
				"tx":       computedInt64("Current transmit rate of the client, in bytes per second. The web UI shows it as the upload speed."),
				"total_rx": computedInt64("Data that the client received, in bytes. The web UI shows it as the download traffic."),
				"total_tx": computedInt64("Data that the client transmitted, in bytes. The web UI shows it as the upload traffic."),
				"total_rx_init": computedInt64("Number that the router gives as `total_rx_init`. " +
					"The web UI does not use it, and its meaning is not known."),
				"total_tx_init": computedInt64("Number that the router gives as `total_tx_init`. " +
					"The web UI does not use it, and its meaning is not known."),
				"last_rx": schema.ListAttribute{
					Description: "Numbers that the router gives as `last_rx`. On the test router, the list had 60 items, and the last item was equal to `total_rx`. " +
						"The web UI does not use it, and its meaning is not known.",
					Computed:    true,
					ElementType: types.Int64Type,
				},
				"last_tx": schema.ListAttribute{
					Description: "Numbers that the router gives as `last_tx`. On the test router, the list had 60 items, and the last item was equal to `total_tx`. " +
						"The web UI does not use it, and its meaning is not known.",
					Computed:    true,
					ElementType: types.Int64Type,
				},
				"last_update_rate": computedInt64("Unix time in seconds that the router gives as `last_update_rate`. " +
					"On the test router, all clients had the same value. The web UI does not use it, and its meaning is not known."),
			}),
		},
	}
}

func (d *clientsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *clientsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	clients, err := d.client.Clients.ListClients(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Clients", err.Error())
		return
	}

	state := clientsModel{Clients: []clientModel{}}
	for _, c := range clients {
		state.Clients = append(state.Clients, clientModel{
			MAC:            c.MAC,
			Name:           c.Name,
			Alias:          stringOrNull(c.Alias),
			Icon:           stringOrNull(c.Class),
			Blocked:        c.Blocked,
			DownloadLimit:  c.LimitRx,
			UploadLimit:    c.LimitTx,
			IP:             c.IP,
			IPv6:           append([]string{}, c.IPv6...),
			Connection:     c.Iface,
			Type:           int64(c.Type),
			Online:         c.Online,
			OnlineTime:     c.OnlineTime,
			Remote:         c.Remote,
			Rx:             c.Rx,
			Tx:             c.Tx,
			TotalRx:        c.TotalRx,
			TotalTx:        c.TotalTx,
			TotalRxInit:    c.TotalRxInit,
			TotalTxInit:    c.TotalTxInit,
			LastRx:         append([]int64{}, c.LastRx...),
			LastTx:         append([]int64{}, c.LastTx...),
			LastUpdateRate: c.LastUpdateRate,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
