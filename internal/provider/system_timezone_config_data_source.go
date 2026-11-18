package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &systemTimezoneConfigDataSource{}
	_ datasource.DataSourceWithConfigure = &systemTimezoneConfigDataSource{}
)

func NewSystemTimezoneConfigDataSource() datasource.DataSource {
	return &systemTimezoneConfigDataSource{}
}

type systemTimezoneConfigModel struct {
	Id                  types.String `tfsdk:"id"`
	Zonename            types.String `tfsdk:"zonename"`
	TZOffset            types.String `tfsdk:"tzoffset"`
	AutoTimezoneEnabled types.Bool   `tfsdk:"auto_timezone_enabled"`
	Localtime           types.Int64  `tfsdk:"localtime"`
	Timezone            types.String `tfsdk:"timezone"`
}

type systemTimezoneConfigDataSource struct {
	client *glinet.Client
}

func (d *systemTimezoneConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_timezone_config"
}

func (d *systemTimezoneConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the time zone configuration of the router (`system.get_timezone_config`). To change it, use `glinet_system_timezone`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The POSIX TZ string, the same as `timezone`.",
				Computed:    true,
			},
			"zonename": schema.StringAttribute{
				Description: "Time zone name as the router shows it, for example `America/New York`. Empty when the router gives none.",
				Computed:    true,
			},
			"tzoffset": schema.StringAttribute{
				Description: "Current offset from UTC, for example `-0500`. It changes with daylight saving time.",
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
			"timezone": schema.StringAttribute{
				Description: "POSIX TZ string, for example `EST5EDT,M3.2.0,M11.1.0`.",
				Computed:    true,
			},
		},
	}
}

func (d *systemTimezoneConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *systemTimezoneConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	config, err := d.client.System.GetTimezoneConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read System Timezone Config", err.Error())
		return
	}

	state := systemTimezoneConfigModel{
		Id:                  types.StringValue(config.Timezone),
		Zonename:            types.StringValue(config.Zonename),
		TZOffset:            types.StringValue(config.TZOffset),
		AutoTimezoneEnabled: types.BoolValue(config.AutoTimezoneEnabled),
		Localtime:           types.Int64Value(config.Localtime),
		Timezone:            types.StringValue(config.Timezone),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
