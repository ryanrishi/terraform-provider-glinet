package provider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ryanrishi/glinet-client-go"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &RouterHelloDataSource{}

func NewRouterHelloDataSource() datasource.DataSource {
	return &RouterHelloDataSource{}
}

// RouterHellpDataSource defines the data source implementation.
type RouterHelloDataSource struct {
	client *glinet.APIClient
}

type RouterHelloDataSourceModel struct {
	Init       types.Bool   `tfsdk:"init"`
	Configured types.Bool   `tfsdk:"configured"`
	Connected  types.Bool   `tfsdk:"connected"`
	Version    types.Bool   `tfsdk:"version"`
	Model      types.String `tfsdk:"model"`
	Mac        types.String `tfsdk:"mac"`
	Type       types.String `tfsdk:"type"`
	Code       types.Int64  `tfsdk:"code"`
}

func (d *RouterHelloDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_example"
}

func (d *RouterHelloDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Check router is connected and configured. No login permission required.",

		Attributes: map[string]schema.Attribute{
			"init": schema.BoolAttribute {
				MarkdownDescription: "Identifies whether file system initialization complete.",
			},

			"configured": schema.BoolAttribute {
				MarkdownDescription: "Identifies whether the admin password is set.",
			},

			"connected": schema.BoolAttribute {
				MarkdownDescription: "Router connection status.",
			},

			"version": schema.StringAttribute{
				MarkdownDescription: "Current firmware version.",
			},

			"model": schema.StringAttribute {
				MarkdownDescription: "Device model.",
			},

			"mac": schema.StringAttribute {
				MarkdownDescription: "Device mac.",
			},

			"type": schema.StringAttribute {
				MarkdownDescription: "Whether the device is in mesh mode.",
			},

			"code": schema.Int64Attribute {
				MarkdownDescription: "return code.",
			},
		},
	}
}

func (d *RouterHelloDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*glinet.APIClient)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *http.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.client = client
}

func (d *RouterHelloDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data RouterHelloDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var api = d.client.RouterApi

	hello, _, err := api.GetRouterHelloExecute(api.GetRouterHello(ctx))

	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read hello, got error: %s", err))
		return
	}

	data = RouterHelloDataSourceModel{
		Init:       types.Bool{},
		Configured: types.Bool{},
		Connected:  types.Bool{},
		Version:    types.Bool{},
		Model:      types.String{},
		Mac:        types.String{},
		Type:       types.String{},
		Code:       types.Int64{},
	}
	data.Code = types.Int64Value(int64(hello.GetCode()))

	// Write logs using the tflog package
	// Documentation: https://terraform.io/plugin/log
	tflog.Trace(ctx, "read a data source")

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
