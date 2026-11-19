package provider

import (
	"context"
	"fmt"

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

const igmpSnoopingID = "igmp_snooping"

var (
	_ resource.Resource                = &igmpSnoopingResource{}
	_ resource.ResourceWithConfigure   = &igmpSnoopingResource{}
	_ resource.ResourceWithImportState = &igmpSnoopingResource{}
)

func NewIGMPSnoopingResource() resource.Resource {
	return &igmpSnoopingResource{}
}

type igmpSnoopingResource struct {
	client *glinet.Client
}

type igmpSnoopingResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Version types.Int64  `tfsdk:"version"`
}

func (r *igmpSnoopingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_igmp_snooping"
}

func (r *igmpSnoopingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages IGMP snooping of the router (`igmp.set_config` in the router API). " +
			"The router has one IGMP snooping configuration, so use one instance of this resource. " +
			"The web UI shows the setting only in the router network mode. " +
			"Each write sends `enabled` and `version`, as the web UI does. When `version` is not set, the provider sends back the value that it reads from the router just before the write. " +
			"The web UI waits 15 seconds after the write before it reads the configuration again; what the router restarts for the write is not known. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + igmpSnoopingID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether IGMP snooping is enabled (`enable` in the router API).",
				Required:    true,
			},
			"version": schema.Int64Attribute{
				Description: "IGMP version: `1`, `2` or `3` (`version` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.Int64{
					int64validator.OneOf(1, 2, 3),
				},
			},
		},
	}
}

func (r *igmpSnoopingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *igmpSnoopingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *igmpSnoopingResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *igmpSnoopingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *igmpSnoopingResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *igmpSnoopingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != igmpSnoopingID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_igmp_snooping must be %q, got %q.", igmpSnoopingID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), igmpSnoopingID)...)
}

func (r *igmpSnoopingResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model igmpSnoopingResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.IGMP.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read IGMP Snooping Configuration", err.Error())
		return
	}
	config.Enabled = model.Enabled.ValueBool()
	if !model.Version.IsUnknown() {
		config.Version = int(model.Version.ValueInt64())
	}

	if err := r.client.IGMP.SetConfig(ctx, *config); err != nil {
		diags.AddError("Unable to Set IGMP Snooping Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, igmpSnoopingModel(config))...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *igmpSnoopingResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.IGMP.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read IGMP Snooping Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, igmpSnoopingModel(config))...)
}

func igmpSnoopingModel(config *glinet.IGMPConfig) *igmpSnoopingResourceModel {
	return &igmpSnoopingResourceModel{
		ID:      types.StringValue(igmpSnoopingID),
		Enabled: types.BoolValue(config.Enabled),
		Version: types.Int64Value(int64(config.Version)),
	}
}
