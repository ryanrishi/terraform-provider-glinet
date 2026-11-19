package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const localAccessID = "local_access"

// browserBlockedPorts are the ports that browsers do not connect to (the bad ports of the Fetch standard), as the
// web UI lists them. The web UI does not permit them for the web ports.
var browserBlockedPorts = []int64{
	1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111,
	113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548,
	554, 556, 563, 587, 601, 636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 5060, 5061, 6000, 6566, 6665,
	6666, 6667, 6668, 6669, 6697, 10080,
}

const endpointMoveNote = "A change of http_port, https_port or redirect_https moves the web UI and the API that the provider uses. " +
	"Change the host of the provider to the new address: http://<router address>:<http_port>, " +
	"or https://<router address>:<https_port> when redirect_https is true or the host uses HTTPS."

var (
	_ resource.Resource                     = &localAccessResource{}
	_ resource.ResourceWithConfigure        = &localAccessResource{}
	_ resource.ResourceWithImportState      = &localAccessResource{}
	_ resource.ResourceWithConfigValidators = &localAccessResource{}
)

func NewLocalAccessResource() resource.Resource {
	return &localAccessResource{}
}

type localAccessResource struct {
	client *glinet.Client
}

type localAccessResourceModel struct {
	ID                types.String `tfsdk:"id"`
	HTTPPort          types.Int64  `tfsdk:"http_port"`
	HTTPSPort         types.Int64  `tfsdk:"https_port"`
	RedirectHTTPS     types.Bool   `tfsdk:"redirect_https"`
	LuCIHTTPPort      types.Int64  `tfsdk:"luci_http_port"`
	LuCIHTTPSPort     types.Int64  `tfsdk:"luci_https_port"`
	LuCIRedirectHTTPS types.Bool   `tfsdk:"luci_redirect_https"`
	SessionTimeout    types.Int64  `tfsdk:"session_timeout"`
	SSHEnabled        types.Bool   `tfsdk:"ssh_enabled"`
	SSHPort           types.Int64  `tfsdk:"ssh_port"`
}

type namedPort struct {
	name  string
	value types.Int64
}

// ports returns the five ports, which must all be different.
func (m localAccessResourceModel) ports() []namedPort {
	return []namedPort{
		{"http_port", m.HTTPPort},
		{"https_port", m.HTTPSPort},
		{"luci_http_port", m.LuCIHTTPPort},
		{"luci_https_port", m.LuCIHTTPSPort},
		{"ssh_port", m.SSHPort},
	}
}

func (r *localAccessResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_local_access"
}

func webPortAttribute(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Description: description + " From 1 to 65535, and not a port that browsers block, for example 22, 25 or 6000 (the bad ports of the Fetch standard), as the web UI permits. " +
			"When it is not set, the router value does not change.",
		Optional: true,
		Computed: true,
		Validators: []validator.Int64{
			int64validator.Between(1, 65535),
			int64validator.NoneOf(browserBlockedPorts...),
		},
	}
}

func (r *localAccessResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the access to the router from the LAN: the ports of the web UI, LuCI and SSH, the redirect to HTTPS and the session timeout of the web UI " +
			"(`local-access.set_config` in the router API). The router has one local access configuration, so use one instance of this resource.\n\n" +
			"~> **Warning:** this resource can cut the access to the router, also the access of the provider. " +
			"The web UI and the API that the provider uses have the same address. A change of `http_port`, `https_port` or `redirect_https` moves this address: " +
			"after the apply, change `host` of the provider to the new address, for example `http://192.168.8.1:8080`, or the next plan cannot read the router. " +
			"After such a write, the provider saves the written values in the state before it reads the router again, and shows a warning; when that read fails, the apply does not fail. " +
			"It is not known whether the router answers the write before it moves the address; when it does not answer, the apply fails and the write was possibly done. " +
			"A change of `ssh_enabled` or `ssh_port` can end the SSH access to the router.\n\n" +
			"Each write sends all attributes, as the web UI does, also the LuCI attributes when LuCI is not installed. " +
			"When an attribute is not set, the provider sends back the router value that it reads just before the write. " +
			"The five ports must all be different; the provider checks this before the write, also against the router values of the ports that are not set. " +
			"The router refuses a port that is in use and SSH with a weak admin password. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + localAccessID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"http_port":  webPortAttribute("Port of the web UI and the API over HTTP (`http_port` in the router API)."),
			"https_port": webPortAttribute("Port of the web UI and the API over HTTPS (`https_port` in the router API)."),
			"redirect_https": schema.BoolAttribute{
				Description: "Whether the router redirects HTTP to HTTPS for the web UI (`redirect_https` in the router API). " +
					"What the redirect does to the API calls of the provider over HTTP is not known; use a `host` with `https://` when it is `true`. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
			"luci_http_port":  webPortAttribute("Port of LuCI over HTTP (`luci_http_port` in the router API). The web UI shows it only when LuCI is installed."),
			"luci_https_port": webPortAttribute("Port of LuCI over HTTPS (`luci_https_port` in the router API). The web UI shows it only when LuCI is installed."),
			"luci_redirect_https": schema.BoolAttribute{
				Description: "Whether the router redirects HTTP to HTTPS for LuCI (`luci_redirect_https` in the router API). The web UI shows it only when LuCI is installed. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
			"session_timeout": schema.Int64Attribute{
				Description: "Session timeout of the web UI in seconds, from 60 to 10800 in steps of 60 (`session_timeout` in the router API). " +
					"The web UI shows it in minutes. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.Int64{
					int64validator.Between(60, 10800),
				},
			},
			"ssh_enabled": schema.BoolAttribute{
				Description: "Whether the SSH server of the router is enabled (`ssh_enabled` in the router API). " +
					"The router refuses `true` when the admin password is too weak. " +
					"Firmware 4.7.0 does not return `ssh_enabled`, so the provider reads `false` there, and each write sends `ssh_enabled` `false` when it is not set; " +
					"it is not known what that firmware does with this value. " +
					"With such firmware, do not set it: a configured `true` makes a plan difference after each refresh. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
			"ssh_port": schema.Int64Attribute{
				Description: "Port of the SSH server (`ssh_port` in the router API), from 1 to 65535. When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
			},
		},
	}
}

func (r *localAccessResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "session_timeout is a multiple of 60, and the configured ports are all different",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config localAccessResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if t := config.SessionTimeout; !t.IsNull() && !t.IsUnknown() && t.ValueInt64()%60 != 0 {
				diags.AddAttributeError(path.Root("session_timeout"), "Invalid Attribute Value",
					fmt.Sprintf("session_timeout must be a multiple of 60 seconds, got: %d.", t.ValueInt64()))
			}
			checkDifferentPorts(config.ports(), "", diags)
		},
	}}
}

// checkDifferentPorts adds an error for each port that has the value of an earlier port. Null and unknown
// values are not compared. source tells where the earlier port has its value.
func checkDifferentPorts(ports []namedPort, source string, diags *diag.Diagnostics) {
	for i, p := range ports {
		if p.value.IsNull() || p.value.IsUnknown() {
			continue
		}
		for _, earlier := range ports[:i] {
			if p.value.Equal(earlier.value) {
				diags.AddAttributeError(path.Root(p.name), "Port Conflict",
					fmt.Sprintf("%s and %s%s are both %d. The five ports of glinet_local_access must all be different.",
						p.name, earlier.name, source, p.value.ValueInt64()))
				break
			}
		}
	}
}

func (r *localAccessResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *localAccessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *localAccessResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	config, err := r.client.LocalAccess.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Local Access Configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, localAccessModel(config))...)
}

func (r *localAccessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *localAccessResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *localAccessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != localAccessID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_local_access must be %q, got %q.", localAccessID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), localAccessID)...)
}

// set writes all fields. An unknown attribute, which is not in the configuration, gets the router value.
// The state gets the written values before the read after the write, because a change of the address of the API
// can make that read fail.
func (r *localAccessResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model localAccessResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	current, err := r.client.LocalAccess.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Local Access Configuration", err.Error())
		return
	}
	model.fillUnknown(localAccessModel(current))
	checkDifferentPorts(model.ports(), " (with the router values of the ports that are not set)", diags)
	if diags.HasError() {
		return
	}

	request := glinet.LocalAccessConfig{
		HTTPPort:          int(model.HTTPPort.ValueInt64()),
		HTTPSPort:         int(model.HTTPSPort.ValueInt64()),
		RedirectHTTPS:     model.RedirectHTTPS.ValueBool(),
		LuCIHTTPPort:      int(model.LuCIHTTPPort.ValueInt64()),
		LuCIHTTPSPort:     int(model.LuCIHTTPSPort.ValueInt64()),
		LuCIRedirectHTTPS: model.LuCIRedirectHTTPS.ValueBool(),
		SessionTimeout:    int(model.SessionTimeout.ValueInt64()),
		SSHEnabled:        model.SSHEnabled.ValueBool(),
		SSHPort:           int(model.SSHPort.ValueInt64()),
	}
	endpointMoves := request.HTTPPort != current.HTTPPort || request.HTTPSPort != current.HTTPSPort || request.RedirectHTTPS != current.RedirectHTTPS

	if err := r.client.LocalAccess.SetConfig(ctx, request); err != nil {
		addLocalAccessError(err, endpointMoves, diags)
		return
	}

	model.ID = types.StringValue(localAccessID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if endpointMoves {
		diags.AddWarning("Address of the Router API Changed", endpointMoveNote)
	}

	config, err := r.client.LocalAccess.GetConfig(ctx)
	if err != nil {
		if endpointMoves {
			diags.AddWarning("Unable to Read Local Access Configuration After the Write",
				"The write was done and the state has the written values. The read after the write failed, "+
					"probably because the router API has a new address. Change the host of the provider before the next plan. Error: "+err.Error())
			return
		}
		diags.AddError("Unable to Read Local Access Configuration", err.Error())
		return
	}
	diags.Append(state.Set(ctx, localAccessModel(config))...)
}

func (m *localAccessResourceModel) fillUnknown(current *localAccessResourceModel) {
	for _, pair := range []struct{ value, router *types.Int64 }{
		{&m.HTTPPort, &current.HTTPPort},
		{&m.HTTPSPort, &current.HTTPSPort},
		{&m.LuCIHTTPPort, &current.LuCIHTTPPort},
		{&m.LuCIHTTPSPort, &current.LuCIHTTPSPort},
		{&m.SessionTimeout, &current.SessionTimeout},
		{&m.SSHPort, &current.SSHPort},
	} {
		if pair.value.IsUnknown() {
			*pair.value = *pair.router
		}
	}
	for _, pair := range []struct{ value, router *types.Bool }{
		{&m.RedirectHTTPS, &current.RedirectHTTPS},
		{&m.LuCIRedirectHTTPS, &current.LuCIRedirectHTTPS},
		{&m.SSHEnabled, &current.SSHEnabled},
	} {
		if pair.value.IsUnknown() {
			*pair.value = *pair.router
		}
	}
}

// addLocalAccessError adds the error of local-access set_config. The router gives err_code -1 to -3 for a port that
// is in use and -4 for SSH with a weak admin password.
func addLocalAccessError(err error, endpointMoves bool, diags *diag.Diagnostics) {
	const summary = "Unable to Set Local Access Configuration"
	var rpcErr *glinet.RPCError
	if !errors.As(err, &rpcErr) {
		detail := err.Error()
		if endpointMoves {
			detail = "The router did not answer the write. The write was possibly done. " + endpointMoveNote +
				" Then run terraform apply again. Error: " + detail
		}
		diags.AddError(summary, detail)
		return
	}

	message := strings.ToLower(rpcErr.Message + " " + rpcErr.Err)
	switch rpcErr.Code {
	case -1:
		diags.AddAttributeError(path.Root("http_port"), summary, "The HTTP port is in use on the router. Use another port. Error: "+err.Error())
	case -2:
		diags.AddAttributeError(path.Root("https_port"), summary, "The HTTPS port is in use on the router. Use another port. Error: "+err.Error())
	case -3:
		attribute, name := "luci_http_port", "LuCI HTTP port"
		switch {
		case strings.Contains(message, "ssh"):
			attribute, name = "ssh_port", "SSH port"
		case strings.Contains(message, "https"):
			attribute, name = "luci_https_port", "LuCI HTTPS port"
		}
		diags.AddAttributeError(path.Root(attribute), summary, "The "+name+" is in use on the router. Use another port. Error: "+err.Error())
	case -4:
		diags.AddAttributeError(path.Root("ssh_enabled"), summary,
			"The router does not enable SSH because the admin password is too weak. Set a stronger admin password in the web UI, then apply again. Error: "+err.Error())
	default:
		diags.AddError(summary, err.Error())
	}
}

func localAccessModel(c *glinet.LocalAccessConfig) *localAccessResourceModel {
	return &localAccessResourceModel{
		ID:                types.StringValue(localAccessID),
		HTTPPort:          types.Int64Value(int64(c.HTTPPort)),
		HTTPSPort:         types.Int64Value(int64(c.HTTPSPort)),
		RedirectHTTPS:     types.BoolValue(c.RedirectHTTPS),
		LuCIHTTPPort:      types.Int64Value(int64(c.LuCIHTTPPort)),
		LuCIHTTPSPort:     types.Int64Value(int64(c.LuCIHTTPSPort)),
		LuCIRedirectHTTPS: types.BoolValue(c.LuCIRedirectHTTPS),
		SessionTimeout:    types.Int64Value(int64(c.SessionTimeout)),
		SSHEnabled:        types.BoolValue(c.SSHEnabled),
		SSHPort:           types.Int64Value(int64(c.SSHPort)),
	}
}
