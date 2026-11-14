package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

const zeroTierID = "zerotier"

var zeroTierNetworkID = regexp.MustCompile(`^[0-9a-f]{16}$`)

var (
	_ resource.Resource                = &zeroTierResource{}
	_ resource.ResourceWithConfigure   = &zeroTierResource{}
	_ resource.ResourceWithImportState = &zeroTierResource{}
)

func NewZeroTierResource() resource.Resource {
	return &zeroTierResource{}
}

type zeroTierResource struct {
	client *glinet.Client
}

type zeroTierResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	NetworkID  types.String `tfsdk:"network_id"`
	LANEnabled types.Bool   `tfsdk:"lan_enabled"`
	WANEnabled types.Bool   `tfsdk:"wan_enabled"`
}

func (r *zeroTierResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_zerotier"
}

func (r *zeroTierResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages ZeroTier on the router. The router has one ZeroTier configuration, so use one instance of this resource. " +
			"When ZeroTier is enabled, the router joins the ZeroTier network `network_id`. " +
			"A private network must still authorize the router in the ZeroTier network controller; this resource does not do that. " +
			"The router takes up to about 11 seconds to apply a write that enables ZeroTier. " +
			"When `enabled` is `false`, the provider only disables ZeroTier, and the router keeps its previous network ID, `lan_enabled` and `wan_enabled`: " +
			"the configured `network_id`, `lan_enabled` and `wan_enabled` are kept in the Terraform state and get to the router only when ZeroTier is enabled. " +
			"While the router reports ZeroTier as disabled, the provider does not compare them with the router. " +
			"Destroy disables ZeroTier on the router. The router keeps its network ID after a destroy, because the router API cannot remove it. " +
			"Tested only with a network ID that does not exist: what `lan_enabled` and `wan_enabled` do on a router that is connected to a ZeroTier network was not verified.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + zeroTierID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether ZeroTier runs on the router (`enabled` in the router API).",
				Required:    true,
			},
			"network_id": schema.StringAttribute{
				Description: "ID of the ZeroTier network that the router joins, 16 hexadecimal digits in lower case (`id` in the router API). " +
					"It is sensitive, because the ID of a public network is all that a device needs to join it.",
				Required:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(zeroTierNetworkID, "must be 16 hexadecimal digits in lower case"),
				},
			},
			"lan_enabled": schema.BoolAttribute{
				Description: "Whether the devices in the ZeroTier network can get remote access to the LAN of the router (`lan_enabled` in the router API). Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"wan_enabled": schema.BoolAttribute{
				Description: "Whether the devices in the ZeroTier network can get remote access to the WAN of the router (`wan_enabled` in the router API). Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
		},
	}
}

func (r *zeroTierResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *zeroTierResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *zeroTierResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *zeroTierResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *zeroTierResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model zeroTierResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model.Enabled = types.BoolValue(false)
	if err := r.client.ZeroTier.SetConfig(ctx, routerZeroTier(model)); err != nil {
		resp.Diagnostics.AddError("Unable to Disable ZeroTier", err.Error())
	}
}

func (r *zeroTierResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != zeroTierID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_zerotier must be %q, got %q.", zeroTierID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), zeroTierID)...)
}

// set sends all values also when ZeroTier is disabled: the router then only disables ZeroTier and ignores the other values.
func (r *zeroTierResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model zeroTierResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if err := r.client.ZeroTier.SetConfig(ctx, routerZeroTier(model)); err != nil {
		diags.AddError("Unable to Set ZeroTier Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(zeroTierID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

// read keeps network_id, lan_enabled and wan_enabled of the state while the router reports ZeroTier as disabled,
// because a disabled write does not change them on the router.
func (r *zeroTierResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	var prior zeroTierResourceModel
	diags.Append(state.Get(ctx, &prior)...)
	if diags.HasError() {
		return
	}

	config, err := r.client.ZeroTier.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read ZeroTier Configuration", err.Error())
		return
	}

	model := zeroTierResourceModel{
		ID:         types.StringValue(zeroTierID),
		Enabled:    types.BoolValue(config.Enabled),
		NetworkID:  types.StringValue(config.ID),
		LANEnabled: types.BoolValue(config.LANEnabled),
		WANEnabled: types.BoolValue(config.WANEnabled),
	}
	if !config.Enabled && !prior.NetworkID.IsNull() {
		model.NetworkID = prior.NetworkID
		model.LANEnabled = prior.LANEnabled
		model.WANEnabled = prior.WANEnabled
	}

	diags.Append(state.Set(ctx, &model)...)
}

func routerZeroTier(model zeroTierResourceModel) glinet.ZeroTierConfig {
	return glinet.ZeroTierConfig{
		Enabled:    model.Enabled.ValueBool(),
		ID:         model.NetworkID.ValueString(),
		LANEnabled: model.LANEnabled.ValueBool(),
		WANEnabled: model.WANEnabled.ValueBool(),
	}
}
