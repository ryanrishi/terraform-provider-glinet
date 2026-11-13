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

const (
	defaultHost     = "http://192.168.8.1"
	defaultUsername = "root"
)

var _ provider.Provider = &GLiNetProvider{}

type GLiNetProvider struct {
	version string
}

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
		Description: "Interact with GL.iNet routers that run v4 firmware.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Description: "URI for GL.iNet API. May also be provided via GLINET_HOST environment variable. Defaults to `" + defaultHost + "`.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "Username for GL.iNet API. May also be provided via GLINET_USERNAME environment variable. Defaults to `" + defaultUsername + "`.",
				Optional:    true,
			},
			"password": schema.StringAttribute{
				Description: "Password for GL.iNet API. May also be provided via GLINET_PASSWORD environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

func (p *GLiNetProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config GLiNetProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	checkKnown(resp, config.Host, "host", "GLINET_HOST")
	checkKnown(resp, config.Username, "username", "GLINET_USERNAME")
	checkKnown(resp, config.Password, "password", "GLINET_PASSWORD")
	if resp.Diagnostics.HasError() {
		return
	}

	host := valueOrEnv(config.Host, "GLINET_HOST", defaultHost)
	username := valueOrEnv(config.Username, "GLINET_USERNAME", defaultUsername)
	password := valueOrEnv(config.Password, "GLINET_PASSWORD", "")

	if password == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("password"),
			"Missing GL.iNet API Password",
			"The provider cannot create the GL.iNet API client because the password is missing or empty. "+
				"Set the password value in the configuration or use the GLINET_PASSWORD environment variable.",
		)
		return
	}

	ctx = tflog.SetField(ctx, "glinet_host", host)
	ctx = tflog.SetField(ctx, "glinet_username", username)
	tflog.Debug(ctx, "Creating GL.iNet client")

	client, err := glinet.NewClientWithHost(host, username, []byte(password))
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create GL.iNet API Client",
			"The provider cannot log in to the GL.iNet API at "+host+": "+err.Error(),
		)
		return
	}

	resp.DataSourceData = client
	resp.ResourceData = client

	tflog.Info(ctx, "Configured GL.iNet client")
}

func checkKnown(resp *provider.ConfigureResponse, value types.String, attribute, envVar string) {
	if !value.IsUnknown() {
		return
	}

	resp.Diagnostics.AddAttributeError(
		path.Root(attribute),
		"Unknown GL.iNet API "+attribute,
		"The provider cannot create the GL.iNet API client because the "+attribute+" value is unknown. "+
			"Either target apply the source of the value first, set the value statically in the configuration, or use the "+envVar+" environment variable.",
	)
}

func valueOrEnv(value types.String, envVar, fallback string) string {
	if !value.IsNull() {
		return value.ValueString()
	}
	if env := os.Getenv(envVar); env != "" {
		return env
	}
	return fallback
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
