package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const parentalControlID = "parental_control"

var (
	_ resource.Resource                = &parentalControlResource{}
	_ resource.ResourceWithConfigure   = &parentalControlResource{}
	_ resource.ResourceWithImportState = &parentalControlResource{}
)

func NewParentalControlResource() resource.Resource {
	return &parentalControlResource{}
}

type parentalControlResource struct {
	client *glinet.Client
}

type parentalControlResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	DropAnonymous    types.Bool   `tfsdk:"drop_anonymous_enabled"`
	AutoUpdate       types.Bool   `tfsdk:"auto_update_enabled"`
	AppFilterEnabled types.Bool   `tfsdk:"app_filter_enabled"`
}

func (r *parentalControlResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_parental_control"
}

func (r *parentalControlResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the global settings of parental control on the router (`parental-control.set_config`). " +
			"The router has one parental control configuration, so use one instance of this resource.\n\n" +
			parentalControlWarning + "\n\n" +
			"Parental control applies rule sets (`glinet_parental_control_ruleset`) to groups of clients (`glinet_parental_control_group`). " +
			parentalControlAccelerationNote + " " +
			"When `drop_anonymous_enabled` or `auto_update_enabled` is not set, the provider sends the router value. " +
			untestedWriteNote +
			"Destroy disables parental control on the router, because parental control acts on its own and can block clients. " +
			"Destroy keeps `drop_anonymous_enabled` and `auto_update_enabled`, the groups and the rule sets.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + parentalControlID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether parental control is enabled (`enable` in the router API).",
				Required:    true,
			},
			"drop_anonymous_enabled": schema.BoolAttribute{
				Description: "Whether the router blocks the clients that are in no group (`drop_anonymous` in the router API), " +
					"also the computer that runs Terraform. When it is not set, the provider keeps the router value.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"auto_update_enabled": schema.BoolAttribute{
				Description: "Whether the router updates its app feature library automatically (`auto_update` in the router API). " +
					"The web UI shows this setting only when `app_filter_enabled` is true. When it is not set, the provider keeps the router value.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"app_filter_enabled": schema.BoolAttribute{
				Description: "Whether the router has the app feature library (`enable_app` in the router API). " +
					"The router sets this value. The web UI shows the apps of a rule set and `auto_update_enabled` only when it is true.",
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *parentalControlResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *parentalControlResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, parentalControlModel(config))...)
}

func (r *parentalControlResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}

	settings := glinet.ParentalControlSettings{Enabled: false, DropAnonymous: config.DropAnonymous, AutoUpdate: config.AutoUpdate}
	if err := r.client.ParentalControl.SetConfig(ctx, settings); err != nil {
		resp.Diagnostics.AddError("Unable to Disable Parental Control", err.Error())
	}
}

func (r *parentalControlResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != parentalControlID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_parental_control must be %q, got %q.", parentalControlID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parentalControlID)...)
}

func (r *parentalControlResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model parentalControlResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}

	settings := glinet.ParentalControlSettings{
		Enabled:       model.Enabled.ValueBool(),
		DropAnonymous: boolOr(model.DropAnonymous, config.DropAnonymous),
		AutoUpdate:    boolOr(model.AutoUpdate, config.AutoUpdate),
	}
	if err := r.client.ParentalControl.SetConfig(ctx, settings); err != nil {
		diags.AddError("Unable to Set Parental Control Configuration", err.Error())
		return
	}

	config, err = r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, parentalControlModel(config))...)
}

// boolOr returns the value of v, or router when v is unknown or null.
func boolOr(v types.Bool, router bool) bool {
	if v.IsUnknown() || v.IsNull() {
		return router
	}
	return v.ValueBool()
}

func parentalControlModel(config *glinet.ParentalControlConfig) *parentalControlResourceModel {
	return &parentalControlResourceModel{
		ID:               types.StringValue(parentalControlID),
		Enabled:          types.BoolValue(config.Enabled),
		DropAnonymous:    types.BoolValue(config.DropAnonymous),
		AutoUpdate:       types.BoolValue(config.AutoUpdate),
		AppFilterEnabled: types.BoolValue(config.EnableApp),
	}
}
