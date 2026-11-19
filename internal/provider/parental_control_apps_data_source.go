package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ datasource.DataSource              = &parentalControlAppsDataSource{}
	_ datasource.DataSourceWithConfigure = &parentalControlAppsDataSource{}
)

func NewParentalControlAppsDataSource() datasource.DataSource {
	return &parentalControlAppsDataSource{}
}

type parentalControlAppsDataSource struct {
	client *glinet.Client
}

type parentalControlAppsModel struct {
	Apps []parentalControlAppModel `tfsdk:"apps"`
}

type parentalControlAppModel struct {
	ID   int64  `tfsdk:"id"`
	Name string `tfsdk:"name"`
	Type int64  `tfsdk:"type"`
}

func (d *parentalControlAppsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_parental_control_apps"
}

func (d *parentalControlAppsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the apps of the app feature library of parental control (`parental-control.get_app_list`). " +
			"Use the IDs in `apps` of `glinet_parental_control_ruleset`. " +
			"The router can return the list also when it does not use the app feature library (`app_filter_enabled` of `glinet_parental_control`).",
		Attributes: map[string]schema.Attribute{
			"apps": computedList("Apps of the app feature library.", map[string]schema.Attribute{
				"id":   computedInt64("ID of the app."),
				"name": computedString("Name of the app."),
				"type": computedInt64("Type of the app: 1 social, 2 game, 3 video, 4 shopping, 5 music, 6 recruitment, 7 download, 8 portal."),
			}),
		},
	}
}

func (d *parentalControlAppsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *parentalControlAppsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	apps, err := d.client.ParentalControl.ListApps(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Apps", err.Error())
		return
	}

	state := parentalControlAppsModel{Apps: []parentalControlAppModel{}}
	for _, app := range apps {
		state.Apps = append(state.Apps, parentalControlAppModel{ID: int64(app.ID), Name: app.Name, Type: int64(app.Type)})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
