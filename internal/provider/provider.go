package provider

import (
	"context"
	"fmt"
	"os"
	"sync"

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
	registryDocs    = "https://registry.terraform.io/providers/ryanrishi/glinet/latest/docs/"
)

var _ provider.Provider = &GLiNetProvider{}

// routerWrites serializes the sequences of resources that read the router, check or find, then write.
// Terraform runs resource operations in parallel, and a write by one operation can make the read of another one stale.
// One lock for all routers is enough: two provider configurations for the same router get separate clients,
// and the writes are few and short.
var routerWrites sync.Mutex

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
		Description: "Manages GL.iNet routers that run v4 firmware. " +
			"The provider is tested on a GL-AXT1800 with firmware 4.7.0 and 4.8.3. Other models and firmware versions can have different API methods.\n\n" +
			"~> **Warning:** some resources can cut the access to the router: " +
			"[`glinet_access_control`](" + registryDocs + "resources/access_control), " +
			"[`glinet_client_allow`](" + registryDocs + "resources/client_allow), " +
			"[`glinet_wan`](" + registryDocs + "resources/wan), " +
			"[`glinet_lan`](" + registryDocs + "resources/lan), " +
			"[`glinet_wifi_interface`](" + registryDocs + "resources/wifi_interface) and " +
			"[`glinet_wifi_radio`](" + registryDocs + "resources/wifi_radio). " +
			"Read the warnings on their pages before you apply them.\n\n" +
			"Destroy of a resource disables a feature that exposes the router or acts on its own " +
			"(`glinet_firewall_dmz`, `glinet_ddns`, `glinet_tailscale`, `glinet_zerotier`, `glinet_reboot_schedule`), " +
			"removes a list item (`glinet_lan_static_lease`, `glinet_client_block`, `glinet_client_allow`, `glinet_firewall_port_forward`, `glinet_firewall_rule`, " +
			"`glinet_wireguard_client_group`, `glinet_wireguard_client_config`, `glinet_wireguard_server_peer`), " +
			"and leaves a plain setting unchanged on the router (the other resources).",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Description: "URL of the router, with the scheme, for example `http://192.168.8.1`. When it is not set, the provider uses the `GLINET_HOST` environment variable. Defaults to `" + defaultHost + "`.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "User name for the login to the router. When it is not set, the provider uses the `GLINET_USERNAME` environment variable. Defaults to `" + defaultUsername + "`.",
				Optional:    true,
			},
			"password": schema.StringAttribute{
				Description: "Password for the login to the router, the same as for the web UI. When it is not set, the provider uses the `GLINET_PASSWORD` environment variable. One of the two is required.",
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
	if v := value.ValueString(); v != "" {
		return v
	}
	if env := os.Getenv(envVar); env != "" {
		return env
	}
	return fallback
}

func (p *GLiNetProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSystemTimezoneResource,
		NewLANStaticLeaseResource,
		NewFirewallPortForwardResource,
		NewFirewallPortForwardOrderResource,
		NewFirewallDMZResource,
		NewFirewallRuleResource,
		NewLEDResource,
		NewLEDScheduleResource,
		NewParentalControlResource,
		NewParentalControlGroupResource,
		NewParentalControlRulesetResource,
		NewRebootScheduleResource,
		NewDNSResource,
		NewAccessControlResource,
		NewAdGuardHomeResource,
		NewClientAllowResource,
		NewClientBlockResource,
		NewDDNSResource,
		NewTailscaleResource,
		NewZeroTierResource,
		NewWANResource,
		NewRepeaterResource,
		NewIPv6Resource,
		NewLANResource,
		NewWifiInterfaceResource,
		NewWifiRadioResource,
		NewWireGuardClientGroupResource,
		NewWireGuardClientConfigResource,
		NewWireGuardServerPeerResource,
	}
}

func (p *GLiNetProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewSystemTimezoneDataSource,
		NewSystemInfoDataSource,
		NewSystemStatusDataSource,
		NewClientsDataSource,
		NewFirewallZonesDataSource,
		NewParentalControlAppsDataSource,
		NewWANStatusDataSource,
		NewRepeaterStatusDataSource,
		NewRepeaterSavedNetworksDataSource,
		NewTetheringStatusDataSource,
		NewARPEntriesDataSource,
		NewWifiStatusDataSource,
		NewDNSSecureServersDataSource,
	}
}

func dataSourceClient(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *glinet.Client {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*glinet.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *glinet.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
	}

	return client
}

func resourceClient(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *glinet.Client {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*glinet.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *glinet.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
	}

	return client
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &GLiNetProvider{
			version: version,
		}
	}
}
