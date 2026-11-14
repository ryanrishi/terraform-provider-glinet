package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &wifiStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &wifiStatusDataSource{}
)

func NewWifiStatusDataSource() datasource.DataSource {
	return &wifiStatusDataSource{}
}

type wifiStatusDataSource struct {
	client *glinet.Client
}

type wifiStatusModel struct {
	Radios []radioStatusModel `tfsdk:"radios"`
}

type radioStatusModel struct {
	Name    string      `tfsdk:"name"`
	State   string      `tfsdk:"state"`
	Channel types.Int64 `tfsdk:"channel"`
}

func (d *wifiStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_status"
}

func (d *wifiStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the state of the Wi-Fi radios (`wifi.get_status`). The values change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"radios": computedList("Wi-Fi radios (`res`).", map[string]schema.Attribute{
				"name":    computedString("Name of the radio, for example `radio0`."),
				"state":   computedString("State of the radio, for example `ready`."),
				"channel": computedInt64("Channel of the radio. Null when the router gives none."),
			}),
		},
	}
}

func (d *wifiStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *wifiStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	radios, err := d.client.Wifi.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Wi-Fi Status", err.Error())
		return
	}

	state := wifiStatusModel{Radios: []radioStatusModel{}}
	for _, r := range radios {
		state.Radios = append(state.Radios, radioStatusModel{
			Name:    r.Name,
			State:   r.State,
			Channel: channelOrNull(r.Channel),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
