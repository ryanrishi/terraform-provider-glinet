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
	MAC        string       `tfsdk:"mac"`
	Name       string       `tfsdk:"name"`
	Alias      types.String `tfsdk:"alias"`
	Class      types.String `tfsdk:"class"`
	Blocked    bool         `tfsdk:"blocked"`
	LimitRx    int64        `tfsdk:"limit_rx"`
	LimitTx    int64        `tfsdk:"limit_tx"`
	IP         string       `tfsdk:"ip"`
	Connection string       `tfsdk:"connection"`
	Online     bool         `tfsdk:"online"`
}

func (d *clientsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_clients"
}

func (d *clientsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the client devices that the router knows, online and offline (`clients.get_list`). " +
			"`ip`, `connection` and `online` change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"clients": computedList("Client devices.", map[string]schema.Attribute{
				"mac":        computedString("MAC address of the client."),
				"name":       computedString("Name of the client, as the router detects it."),
				"alias":      computedString("Name that is set for the client on the router. Null when none is set."),
				"class":      computedString("Device class that is set for the client on the router, for example `laptop`. Null when none is set."),
				"blocked":    computedBool("Whether the router blocks the client."),
				"limit_rx":   computedInt64("Receive speed limit of the client (`limit_rx`), as the router gives it."),
				"limit_tx":   computedInt64("Transmit speed limit of the client (`limit_tx`), as the router gives it."),
				"ip":         computedString("IPv4 address of the client. The router also gives it for offline clients."),
				"connection": computedString("Connection of the client (`iface`), for example `cable` or `5G`."),
				"online":     computedBool("Whether the client is online."),
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
			MAC:        c.MAC,
			Name:       c.Name,
			Alias:      stringOrNull(c.Alias),
			Class:      stringOrNull(c.Class),
			Blocked:    c.Blocked,
			LimitRx:    c.LimitRx,
			LimitTx:    c.LimitTx,
			IP:         c.IP,
			Connection: c.Iface,
			Online:     c.Online,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
