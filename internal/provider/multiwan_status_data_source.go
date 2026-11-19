package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

// multiWANStatuses has the status names at the index of their router value.
var multiWANStatuses = []string{"online", "offline", "error"}

var (
	_ datasource.DataSource              = &multiWANStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &multiWANStatusDataSource{}
)

func NewMultiWANStatusDataSource() datasource.DataSource {
	return &multiWANStatusDataSource{}
}

type multiWANStatusDataSource struct {
	client *glinet.Client
}

type multiWANStatusModel struct {
	Interfaces []multiWANInterfaceStatusModel `tfsdk:"interfaces"`
}

type multiWANInterfaceStatusModel struct {
	Interface  string       `tfsdk:"interface"`
	IPv4Status types.String `tfsdk:"ipv4_status"`
	IPv6Status types.String `tfsdk:"ipv6_status"`
}

func (d *multiWANStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multiwan_status"
}

func (d *multiWANStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	statusDescription := " status of the interface: `online`, `offline` or `error` (`%s` `0`, `1` or `2` in the router API). Null for a value that the web UI does not know."
	resp.Schema = schema.Schema{
		Description: "Get the state of the multi-WAN interfaces (`kmwan.get_status`). " +
			"The web UI shows the IPv4 status for track protocol `ipv4`, the IPv6 status for `ipv6`, and both for `both` or when tracking is disabled. " +
			"The values change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"interfaces": computedList("Multi-WAN interfaces of the router.", map[string]schema.Attribute{
				"interface":   computedString("Name of the interface, for example `wan` or `wwan`."),
				"ipv4_status": computedString("IPv4" + fmt.Sprintf(statusDescription, "status_v4")),
				"ipv6_status": computedString("IPv6" + fmt.Sprintf(statusDescription, "status_v6")),
			}),
		},
	}
}

func (d *multiWANStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *multiWANStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.MultiWAN.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Multi-WAN Status", err.Error())
		return
	}

	state := multiWANStatusModel{Interfaces: []multiWANInterfaceStatusModel{}}
	for _, s := range status.Interfaces {
		state.Interfaces = append(state.Interfaces, multiWANInterfaceStatusModel{
			Interface:  s.Interface,
			IPv4Status: multiWANStatusName(s.StatusV4),
			IPv6Status: multiWANStatusName(s.StatusV6),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func multiWANStatusName(status int) types.String {
	name, ok := enumName(multiWANStatuses, status)
	if !ok {
		return types.StringNull()
	}
	return types.StringValue(name)
}
