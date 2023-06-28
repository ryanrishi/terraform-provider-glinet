package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &systemTimezoneConfigDataSource{}
	_ datasource.DataSourceWithConfigure = &systemTimezoneConfigDataSource{}
)

// NewSystemTimezoneConfigDataSource is a helper function to simplify the provider implementation.
func NewSystemTimezoneConfigDataSource() datasource.DataSource {
	return &systemTimezoneConfigDataSource{}
}

type systemTimezoneConfigModel struct {
	Zonename            types.String `tfsdk:"zonename"`
	TZOffset            types.String `tfsdk:"tzoffset"`
	AutoTimezoneEnabled types.Bool   `tfsdk:"autotimezone_enabled"`
	Localtime           types.Int64  `tfsdk:"localtime"`
	Timezone            types.String `tfsdk:"timezone"`
}

type systemTimezoneConfigDataSource struct {
	client *glinet.Client
}

func (d *systemTimezoneConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_timezone_config"
}

// Schema defines the schema for the datasource.
func (d *systemTimezoneConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the time zone information of the device.",
		Attributes: map[string]schema.Attribute{
			"zonename": schema.StringAttribute{
				Description: "Time zone name of the device (It will return null when use UTC).",
				Computed:    true,
			},
			"tzoffset": schema.StringAttribute{
				Description: "System time-zone offset.",
				Computed:    true,
			},
			"auto_timezone_enabled": schema.BoolAttribute{
				Description: "Whether the device is enabled for automatic time zone.",
				Computed:    true,
			},
			"localtime": schema.Int64Attribute{
				Description: "The timestamp of the device (Unit: Seconds).",
				Computed:    true,
			},
			"timezone": schema.StringAttribute{
				Description: "Time zone of the device.",
				Computed:    true,
			},
		},
	}
}

func (d *systemTimezoneConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*glinet.Client)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *glinet.Client, got: %T. Please report this issue to the provider developers.",
				req.ProviderData),
		)

		return
	}

	d.client = client
}

// Read reads a Linux Bridge interface.
func (d *systemTimezoneConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	// Get current state
	var state systemTimezoneConfigModel

	if resp.Diagnostics.HasError() {
		return
	}

	config, err := d.client.System.GetTimezoneConfig()

	if err != nil {
		resp.Diagnostics.AddError("Unable to Read System Timezone Config", err.Error())
		return
	}

	state = systemTimezoneConfigModel{
		Zonename:            types.StringValue(config.Zonename),
		TZOffset:            types.StringValue(config.TZOffset),
		AutoTimezoneEnabled: types.BoolValue(config.AutoTimezoneEnabled),
		Localtime:           types.Int64Value(int64(config.Localtime)),
		Timezone:            types.StringValue(config.Timezone),
	}

	diags := resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

}
