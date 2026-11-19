package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &fanStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &fanStatusDataSource{}
)

func NewFanStatusDataSource() datasource.DataSource {
	return &fanStatusDataSource{}
}

type fanStatusDataSource struct {
	client *glinet.Client
}

type fanStatusModel struct {
	Running types.Bool  `tfsdk:"running"`
	Speed   types.Int64 `tfsdk:"speed"`
}

func (d *fanStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fan_status"
}

func (d *fanStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the status of the fan of the router (`fan.get_status`). The values change while the router runs. " +
			"Only a router with a fan (`hardware_feature.fan` of `system.get_info`) has a fan status; the result on other routers is not known. " +
			"To set the temperature at which the fan starts, use `glinet_fan`.",
		Attributes: map[string]schema.Attribute{
			"running": computedBool("Whether the fan turns (`status`)."),
			"speed":   computedInt64("Speed of the fan in RPM (`speed`)."),
		},
	}
}

func (d *fanStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *fanStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.Fan.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Fan Status", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &fanStatusModel{
		Running: types.BoolValue(status.Running),
		Speed:   types.Int64Value(int64(status.Speed)),
	})...)
}
