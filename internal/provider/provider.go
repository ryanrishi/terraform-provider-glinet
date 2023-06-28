package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ryanrishi/glinet-client-go"
)

// Ensure GLiNetProvider satisfies various provider interfaces.
var _ provider.Provider = &GLiNetProvider{}

// GLiNetProvider defines the provider implementation.
type GLiNetProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// GLiNetProviderModel describes the provider data model.
type GLiNetProviderModel struct {
	Host     types.String `tfsdk:"host"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (p *GLiNetProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "glinet"
	resp.Version = p.version
}

func (p *GLiNetProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interact with GL.iNet.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Description: "URI for GL.iNet API. May also be provided via GLINET_HOST environment variable.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "Username for GL.iNet API. May also be provided via GLINET_USERNAME environment variable.",
				Required:    true,
			},
			"password": schema.StringAttribute{
				Description: "Password for GL.iNet API. May also be provided via GLINET_PASSWORD environment variable.",
				Required:    true,
				Sensitive:   true,
			},
		},
	}
}

func (p *GLiNetProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	tflog.Info(ctx, "Configuring GL.iNet client")

	// Retrieve provider data from configuration
	var config GLiNetProviderModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If practitioner provided a configuration value for any of the
	// attributes, it must be a known value.

	if config.Host.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("host"),
			"Unknown GL.iNet API Host",
			"The provider cannot create the GL.iNet API client as there is an unknown configuration value for the GL.iNet API host. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the GLINET_HOST environment variable.",
		)
	}

	if config.Username.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Unknown GL.iNet API Username",
			"The provider cannot create the GL.iNet API client as there is an unknown configuration value for the GL.iNet API username. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the GLINET_USERNAME environment variable.",
		)
	}

	if config.Password.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("password"),
			"Unknown GL.iNet API Password",
			"The provider cannot create the GL.iNet API client as there is an unknown configuration value for the GL.iNet API password. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the GLINET_PASSWORD environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Default values to environment variables, but override
	// with Terraform configuration value if set.

	host := os.Getenv("GLINET_HOST")
	username := os.Getenv("GLINET_USERNAME")
	password := os.Getenv("GLINET_PASSWORD")

	if !config.Host.IsNull() {
		host = config.Host.ValueString()
	}

	if !config.Username.IsNull() {
		username = config.Username.ValueString()
	}

	if !config.Password.IsNull() {
		password = config.Password.ValueString()
	}

	// If any of the expected configurations are missing, return
	// errors with provider-specific guidance.

	if host == "" {
		host = "http://192.168.8.1"
	}

	if username == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Missing GL.iNet API Username",
			"The provider cannot create the GL.iNet API client as there is a missing or empty value for the GL.iNet API username. "+
				"Set the username value in the configuration or use the GLINET_USERNAME environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if password == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("password"),
			"Missing GL.iNet API Password",
			"The provider cannot create the GL.iNet API client as there is a missing or empty value for the GL.iNet API password. "+
				"Set the password value in the configuration or use the GLINET_PASSWORD environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	ctx = tflog.SetField(ctx, "glinet_host", host)
	ctx = tflog.SetField(ctx, "glinet_username", username)
	ctx = tflog.SetField(ctx, "glinet_password", password)
	ctx = tflog.MaskFieldValuesWithFieldKeys(ctx, "glinet_password")

	tflog.Debug(ctx, "Creating GL.iNet client")

	// Create a new GL.iNet client using the configuration values
	client := glinet.NewClientWithHost(host, username, []byte(password))

	var data GLiNetProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.DataSourceData = client
	resp.ResourceData = client

	tflog.Info(ctx, "Configured GL.iNet client", map[string]any{"success": true})
}

func (p *GLiNetProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *GLiNetProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewSystemTimezoneConfigDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &GLiNetProvider{
			version: version,
		}
	}
}
