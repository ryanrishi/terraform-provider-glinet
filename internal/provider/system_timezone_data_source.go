package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &systemTimezoneDataSource{}
	_ datasource.DataSourceWithConfigure = &systemTimezoneDataSource{}
)

func NewSystemTimezoneDataSource() datasource.DataSource {
	return &systemTimezoneDataSource{}
}

type systemTimezoneDataSourceModel struct {
	Zonename            types.String `tfsdk:"zonename"`
	TZOffset            types.String `tfsdk:"tzoffset"`
	AutoTimezoneEnabled types.Bool   `tfsdk:"auto_timezone_enabled"`
	Localtime           types.Int64  `tfsdk:"localtime"`
	Timestamp           types.Int64  `tfsdk:"timestamp"`
	Timezone            types.String `tfsdk:"timezone"`
}

type systemTimezoneDataSource struct {
	client *glinet.Client
}

func (d *systemTimezoneDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_timezone"
}

func (d *systemTimezoneDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the time zone configuration of the router (`system.get_timezone_config`). To change it, use `glinet_system_timezone`.",
		Attributes: map[string]schema.Attribute{
			"zonename": schema.StringAttribute{
				Description: "Time zone name as the router shows it, for example `Europe/Berlin`. Empty when the router gives none.",
				Computed:    true,
			},
			"tzoffset": schema.StringAttribute{
				Description: "Current offset from UTC, for example `+0100`. It changes with daylight saving time.",
				Computed:    true,
			},
			"auto_timezone_enabled": schema.BoolAttribute{
				Description: "Whether the router sets the time zone automatically (`autotimezone_enabled`).",
				Computed:    true,
			},
			"localtime": schema.Int64Attribute{
				Description: "Current time of the router, as a Unix time in seconds.",
				Computed:    true,
			},
			"timestamp": schema.Int64Attribute{
				Description: "Clock of the router, as a Unix time in seconds.",
				Computed:    true,
			},
			"timezone": schema.StringAttribute{
				Description: "POSIX TZ string, for example `CET-1CEST,M3.5.0,M10.5.0/3`.",
				Computed:    true,
			},
		},
	}
}

func (d *systemTimezoneDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *systemTimezoneDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	config, err := d.client.System.GetTimezoneConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read System Timezone", err.Error())
		return
	}

	state := systemTimezoneDataSourceModel{
		Zonename:            types.StringValue(config.Zonename),
		TZOffset:            types.StringValue(config.TZOffset),
		AutoTimezoneEnabled: types.BoolValue(config.AutoTimezoneEnabled),
		Localtime:           types.Int64Value(config.Localtime),
		Timestamp:           types.Int64Value(config.Timestamp),
		Timezone:            types.StringValue(config.Timezone),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
