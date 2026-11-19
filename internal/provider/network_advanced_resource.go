package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const networkAdvancedID = "network_advanced"

var (
	_ resource.Resource                = &networkAdvancedResource{}
	_ resource.ResourceWithConfigure   = &networkAdvancedResource{}
	_ resource.ResourceWithImportState = &networkAdvancedResource{}
)

func NewNetworkAdvancedResource() resource.Resource {
	return &networkAdvancedResource{}
}

type networkAdvancedResource struct {
	client *glinet.Client
}

type networkAdvancedResourceModel struct {
	ID          types.String `tfsdk:"id"`
	FullConeNAT types.Bool   `tfsdk:"full_cone_nat"`
	SIPALG      types.Bool   `tfsdk:"sip_alg"`
}

func (r *networkAdvancedResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_advanced"
}

func (r *networkAdvancedResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the advanced network settings of the router: Full Cone NAT and the SIP ALG (`network.set_advance_config` in the router API). " +
			"The router has one configuration of these settings, so use one instance of this resource. " +
			"Each write sends both values, as the web UI does. An attribute that is not set keeps the value of the router: the provider sends back the value that it reads from the router just before the write. " +
			"What the router restarts for the write is not known. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + networkAdvancedID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"full_cone_nat": schema.BoolAttribute{
				Description: "Whether the router uses Full Cone NAT (`nat_enable` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
			"sip_alg": schema.BoolAttribute{
				Description: "Whether the SIP ALG is enabled (`sip_enable` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

func (r *networkAdvancedResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *networkAdvancedResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *networkAdvancedResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *networkAdvancedResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *networkAdvancedResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *networkAdvancedResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != networkAdvancedID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_network_advanced must be %q, got %q.", networkAdvancedID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), networkAdvancedID)...)
}

func (r *networkAdvancedResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model networkAdvancedResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.Network.GetAdvancedConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Advanced Network Settings", err.Error())
		return
	}
	if !model.FullConeNAT.IsUnknown() {
		config.FullConeNAT = boolInt(model.FullConeNAT.ValueBool())
	}
	if !model.SIPALG.IsUnknown() {
		config.SIPALG = boolInt(model.SIPALG.ValueBool())
	}

	if err := r.client.Network.SetAdvancedConfig(ctx, *config); err != nil {
		diags.AddError("Unable to Set Advanced Network Settings", err.Error())
		return
	}

	diags.Append(state.Set(ctx, networkAdvancedModel(config))...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *networkAdvancedResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.Network.GetAdvancedConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Advanced Network Settings", err.Error())
		return
	}

	diags.Append(state.Set(ctx, networkAdvancedModel(config))...)
}

func networkAdvancedModel(config *glinet.NetworkAdvancedConfig) *networkAdvancedResourceModel {
	return &networkAdvancedResourceModel{
		ID:          types.StringValue(networkAdvancedID),
		FullConeNAT: types.BoolValue(config.FullConeNAT == 1),
		SIPALG:      types.BoolValue(config.SIPALG == 1),
	}
}
