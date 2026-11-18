package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &dnsSecureServersDataSource{}
	_ datasource.DataSourceWithConfigure = &dnsSecureServersDataSource{}
)

func NewDNSSecureServersDataSource() datasource.DataSource {
	return &dnsSecureServersDataSource{}
}

type dnsSecureServersDataSource struct {
	client *glinet.Client
}

type dnsSecureServersModel struct {
	Protocols []string               `tfsdk:"protocols"`
	Servers   []dnsSecureServerModel `tfsdk:"servers"`
}

// dnsSecureServerModel has the fields of glinet.EncryptedServer in the same order, so that a type conversion copies them.
type dnsSecureServerModel struct {
	Name     string `tfsdk:"name"`
	Proto    string `tfsdk:"protocol"`
	DNSSEC   bool   `tfsdk:"dnssec"`
	IPv6     bool   `tfsdk:"ipv6"`
	NoFilter bool   `tfsdk:"no_filter"`
	NoLog    bool   `tfsdk:"no_log"`
}

func (d *dnsSecureServersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_secure_servers"
}

func (d *dnsSecureServersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the encrypted DNS protocols and servers of the router (`dns.get_info`). " +
			"They are the values for `protocol` and `servers` of `glinet_dns` in mode `secure`.",
		Attributes: map[string]schema.Attribute{
			"protocols": schema.ListAttribute{
				Description: "Encrypted DNS protocols, for example `DoT` and `DoH` (`protos`).",
				ElementType: types.StringType,
				Computed:    true,
			},
			"servers": computedList("Encrypted DNS servers (`serverlist`).", map[string]schema.Attribute{
				"name":      computedString("Server name (`name`). It is a value for `servers` of `glinet_dns`."),
				"protocol":  computedString("Protocol of the server: `DoH`, `DNSCrypt` or `oDoH` (`proto`)."),
				"dnssec":    computedBool("Whether the server validates DNSSEC (`dnssec`)."),
				"ipv6":      computedBool("Whether the server has an IPv6 address (`ipv6`)."),
				"no_filter": computedBool("Whether the server does not filter the answers (`nofilter`)."),
				"no_log":    computedBool("Whether the server does not log the queries (`nolog`)."),
			}),
		},
	}
}

func (d *dnsSecureServersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *dnsSecureServersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.DNS.GetInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read DNS Secure Servers", err.Error())
		return
	}

	state := dnsSecureServersModel{
		Protocols: append([]string{}, info.Protos...),
		Servers:   []dnsSecureServerModel{},
	}
	for _, s := range info.ServerList {
		state.Servers = append(state.Servers, dnsSecureServerModel(s))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
