package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &arpEntriesDataSource{}
	_ datasource.DataSourceWithConfigure = &arpEntriesDataSource{}
)

func NewARPEntriesDataSource() datasource.DataSource {
	return &arpEntriesDataSource{}
}

type arpEntriesDataSource struct {
	client *glinet.Client
}

type arpEntriesModel struct {
	Entries []arpEntryModel `tfsdk:"entries"`
}

// arpEntryModel has the fields of glinet.ARPEntry in the same order, so that a type conversion copies them.
type arpEntryModel struct {
	IP     string `tfsdk:"ip"`
	MAC    string `tfsdk:"mac"`
	Device string `tfsdk:"device"`
}

func (d *arpEntriesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_arp_entries"
}

func (d *arpEntriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the ARP table of the router (`network.get_arp_list`). The entries change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"entries": computedList("ARP entries.", map[string]schema.Attribute{
				"ip":     computedString("IPv4 address."),
				"mac":    computedString("MAC address."),
				"device": computedString("Network device of the entry, for example `br-lan`."),
			}),
		},
	}
}

func (d *arpEntriesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *arpEntriesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	entries, err := d.client.Network.ListARPEntries(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read ARP Entries", err.Error())
		return
	}

	state := arpEntriesModel{Entries: []arpEntryModel{}}
	for _, e := range entries {
		state.Entries = append(state.Entries, arpEntryModel(e))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
