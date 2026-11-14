package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &firewallZonesDataSource{}
	_ datasource.DataSourceWithConfigure = &firewallZonesDataSource{}
)

func NewFirewallZonesDataSource() datasource.DataSource {
	return &firewallZonesDataSource{}
}

type firewallZonesDataSource struct {
	client *glinet.Client
}

type firewallZonesModel struct {
	InternalZones []string `tfsdk:"internal_zones"`
	ExternalZones []string `tfsdk:"external_zones"`
}

func (d *firewallZonesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_zones"
}

func (d *firewallZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the names of the firewall zones (`firewall.get_zone_list`). " +
			"They are the values for `external_zone` and `internal_zone` of `glinet_firewall_port_forward` " +
			"and for `source_zone` and `destination_zone` of `glinet_firewall_rule`.",
		Attributes: map[string]schema.Attribute{
			"internal_zones": schema.ListAttribute{
				Description: "Internal zones (`internals`).",
				ElementType: types.StringType,
				Computed:    true,
			},
			"external_zones": schema.ListAttribute{
				Description: "External zones (`externals`).",
				ElementType: types.StringType,
				Computed:    true,
			},
		},
	}
}

func (d *firewallZonesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *firewallZonesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	zones, err := d.client.Firewall.GetZones(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Zones", err.Error())
		return
	}

	state := firewallZonesModel{
		InternalZones: append([]string{}, zones.Internals...),
		ExternalZones: append([]string{}, zones.Externals...),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
