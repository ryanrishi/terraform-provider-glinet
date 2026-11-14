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

const ddnsID = "ddns"

var (
	_ resource.Resource                = &ddnsResource{}
	_ resource.ResourceWithConfigure   = &ddnsResource{}
	_ resource.ResourceWithImportState = &ddnsResource{}
)

func NewDDNSResource() resource.Resource {
	return &ddnsResource{}
}

type ddnsResource struct {
	client *glinet.Client
}

type ddnsResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func (r *ddnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ddns"
}

func (r *ddnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the dynamic DNS (DDNS) service of GL.iNet on the router. The router has one DDNS configuration, so use one instance of this resource. " +
			"When DDNS is enabled, the router registers its name and its public address with the GL.iNet DDNS service. " +
			"DDNS does not open access to the router over the WAN. " +
			"Destroy disables DDNS on the router, so that the router does not keep a public name after the resource is removed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + ddnsID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether DDNS is enabled (`enable_ddns` in the router API).",
				Required:    true,
			},
		},
	}
}

func (r *ddnsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *ddnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ddnsResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *ddnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ddnsResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if err := r.client.DDNS.SetEnabled(ctx, false); err != nil {
		resp.Diagnostics.AddError("Unable to Disable DDNS", err.Error())
	}
}

func (r *ddnsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != ddnsID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_ddns must be %q, got %q.", ddnsID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ddnsID)...)
}

func (r *ddnsResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model ddnsResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if err := r.client.DDNS.SetEnabled(ctx, model.Enabled.ValueBool()); err != nil {
		diags.AddError("Unable to Set DDNS", err.Error())
		return
	}

	model.ID = types.StringValue(ddnsID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *ddnsResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.DDNS.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read DDNS Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &ddnsResourceModel{
		ID:      types.StringValue(ddnsID),
		Enabled: types.BoolValue(config.Enabled),
	})...)
}
