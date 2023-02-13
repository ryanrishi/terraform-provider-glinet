package provider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.DataSourceType = routerHelloDataSourceType{}
var _ datasource.DataSource = routerHelloDataSource{}

type routerHelloDataSourceType struct{}

func (t routerHelloDataSourceType) NewDataSource(ctx context.Context, p provider.Provider) (datasource.DataSource, diag.Diagnostics) {
	provider, diags := convertProviderType(p)

	return routerHelloDataSource{
		provider: provider,
	}, diags
}

func (t routerHelloDataSourceType) GetSchema(ctx context.Context) (tfsdk.Schema, diag.Diagnostics) {
	return tfsdk.Schema{
		MarkdownDescription: "Check router is connected and configured. No login permission required.",

		Attributes: map[string]tfsdk.Attribute{
			"init": {
				MarkdownDescription: "Identifies whether file system initialization complete.",
				Type:                types.BoolType,
			},

			"configured": {
				MarkdownDescription: "Identifies whether the admin password is set.",
				Type:                types.BoolType,
			},

			"connected": {
				MarkdownDescription: "Router connection status.",
				Type:                types.BoolType,
			},

			"version": {
				MarkdownDescription: "Current firmware version.",
				Type:                types.BoolType,
			},

			"model": {
				MarkdownDescription: "Device model.",
				Type:                types.StringType,
			},

			"mac": {
				MarkdownDescription: "Device mac.",
				Type:                types.StringType,
			},

			"type": {
				MarkdownDescription: "Whether the device is in mesh mode.",
				Type:                types.StringType,
			},

			"code": {
				MarkdownDescription: "return code.",
				Type:                types.Int64Type,
			},
		},
	}, nil
}

type routerHelloDataSourceData struct {
	Init       types.Bool   `tfsdk:"init"`
	Configured types.Bool   `tfsdk:"configured"`
	Connected  types.Bool   `tfsdk:"connected"`
	Version    types.Bool   `tfsdk:"version"`
	Model      types.String `tfsdk:"model"`
	Mac        types.String `tfsdk:"mac"`
	Type       types.String `tfsdk:"type"`
	Code       types.Int64  `tfsdk:"code"`
}

type routerHelloDataSource struct {
	provider scaffoldingProvider
}

func (d routerHelloDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data routerHelloDataSourceData

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	var api = d.provider.client.RouterApi

	hello, _, err := api.GetRouterHelloExecute(api.GetRouterHello(ctx))

	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read hello, got error: %s", err))
		return
	}

	data := routerHelloDataSourceData{
		Init:       types.Bool{hello.},
		Configured: types.Bool{},
		Connected:  types.Bool{},
		Version:    types.Bool{},
		Model:      types.String{},
		Mac:        types.String{},
		Type:       types.String{},
		Code:       types.Int64{},
	}
	data.Code = types.Int64{Value: int64(hello.GetCode())}

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}
