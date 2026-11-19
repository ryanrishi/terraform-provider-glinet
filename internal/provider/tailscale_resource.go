package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const tailscaleID = "tailscale"

var (
	_ resource.Resource                     = &tailscaleResource{}
	_ resource.ResourceWithConfigure        = &tailscaleResource{}
	_ resource.ResourceWithImportState      = &tailscaleResource{}
	_ resource.ResourceWithConfigValidators = &tailscaleResource{}
)

func NewTailscaleResource() resource.Resource {
	return &tailscaleResource{}
}

type tailscaleResource struct {
	client *glinet.Client
}

type tailscaleResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	LANEnabled types.Bool   `tfsdk:"lan_enabled"`
	WANEnabled types.Bool   `tfsdk:"wan_enabled"`
	ExitNodeIP types.String `tfsdk:"exit_node_ip"`
	LANSubnet  types.String `tfsdk:"lan_subnet"`
}

func (r *tailscaleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tailscale"
}

func (r *tailscaleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages Tailscale on the router. The router has one Tailscale configuration, so use one instance of this resource. " +
			"The login of the router to a tailnet is done one time in the web UI of the router and is not managed by this resource. " +
			"When the router disables Tailscale, it also sets `lan_enabled` and `wan_enabled` to `false` and removes the exit node, " +
			"so these can be set only when `enabled` is `true`. " +
			"Destroy disables Tailscale on the router, which sets all values back to the initial state of the router. " +
			"Tested on a router that was not logged in to a tailnet: what `lan_enabled`, `wan_enabled` and `exit_node_ip` do on a router that is logged in was not verified.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + tailscaleID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether Tailscale runs on the router (`enabled` in the router API).",
				Required:    true,
			},
			"lan_enabled": schema.BoolAttribute{
				Description: "Whether the devices in the tailnet can get remote access to the LAN of the router (`lan_enabled` in the router API). " +
					"Can be `true` only when `enabled` is `true`. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"wan_enabled": schema.BoolAttribute{
				Description: "Whether the devices in the tailnet can get remote access to the WAN of the router (`wan_enabled` in the router API). " +
					"Can be `true` only when `enabled` is `true`. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"exit_node_ip": schema.StringAttribute{
				Description: "Tailscale IPv4 or IPv6 address of the exit node that the router uses (`exit_node_ip` in the router API), " +
					"for example `100.64.0.1` or `fd7a:115c:a1e0::1`. " +
					"The write of an IPv6 address comes from the web UI code of firmware 4.8.3 and was not tested on a router. " +
					"When it is not set, the router uses no exit node. Can be set only when `enabled` is `true`.",
				Optional: true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 or IPv6 address", valid: validIP},
				},
			},
			"lan_subnet": schema.StringAttribute{
				Description: "The LAN subnet that the router advertises to the tailnet, for example `192.168.8.0/24` (`lan_ip` in the router API).",
				Computed:    true,
			},
		},
	}
}

func (r *tailscaleResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{tailscaleDisabledValidator{}}
}

// tailscaleDisabledValidator rejects lan_enabled, wan_enabled and exit_node_ip with enabled = false,
// because the router resets them when it disables Tailscale, so the configuration could never converge.
type tailscaleDisabledValidator struct{}

func (v tailscaleDisabledValidator) Description(context.Context) string {
	return "lan_enabled, wan_enabled and exit_node_ip can be set only when enabled is true"
}

func (v tailscaleDisabledValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v tailscaleDisabledValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config tailscaleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Enabled.IsNull() || config.Enabled.IsUnknown() || config.Enabled.ValueBool() {
		return
	}

	const detail = "When the router disables Tailscale, it sets lan_enabled and wan_enabled to false and removes the exit node, " +
		"so lan_enabled = true, wan_enabled = true and exit_node_ip are allowed only with enabled = true."
	if config.LANEnabled.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("lan_enabled"), "Invalid Tailscale Configuration", detail)
	}
	if config.WANEnabled.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("wan_enabled"), "Invalid Tailscale Configuration", detail)
	}
	if !config.ExitNodeIP.IsNull() && !config.ExitNodeIP.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("exit_node_ip"), "Invalid Tailscale Configuration", detail)
	}
}

func (r *tailscaleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *tailscaleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *tailscaleResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *tailscaleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *tailscaleResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if err := r.client.Tailscale.SetConfig(ctx, glinet.SetTailscaleConfigRequest{Enabled: false}); err != nil {
		resp.Diagnostics.AddError("Unable to Disable Tailscale", err.Error())
	}
}

func (r *tailscaleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != tailscaleID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_tailscale must be %q, got %q.", tailscaleID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), tailscaleID)...)
}

func (r *tailscaleResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model tailscaleResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	err := r.client.Tailscale.SetConfig(ctx, glinet.SetTailscaleConfigRequest{
		Enabled:    model.Enabled.ValueBool(),
		LANEnabled: model.LANEnabled.ValueBool(),
		WANEnabled: model.WANEnabled.ValueBool(),
		ExitNodeIP: model.ExitNodeIP.ValueString(),
	})
	if err != nil {
		diags.AddError("Unable to Set Tailscale Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(tailscaleID)
	if model.LANSubnet.IsUnknown() {
		model.LANSubnet = types.StringNull()
	}
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *tailscaleResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.Tailscale.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Tailscale Configuration", err.Error())
		return
	}

	model := tailscaleResourceModel{
		ID:         types.StringValue(tailscaleID),
		Enabled:    types.BoolValue(config.Enabled),
		LANEnabled: types.BoolValue(config.LANEnabled),
		WANEnabled: types.BoolValue(config.WANEnabled),
		ExitNodeIP: types.StringNull(),
		LANSubnet:  types.StringValue(config.LANIP),
	}
	if config.ExitNodeIP != "" {
		model.ExitNodeIP = types.StringValue(config.ExitNodeIP)
	}

	diags.Append(state.Set(ctx, &model)...)
}
