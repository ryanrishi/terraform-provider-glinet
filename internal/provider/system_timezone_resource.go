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

const systemTimezoneID = "timezone"

var (
	_ resource.Resource                = &systemTimezoneResource{}
	_ resource.ResourceWithConfigure   = &systemTimezoneResource{}
	_ resource.ResourceWithImportState = &systemTimezoneResource{}
)

func NewSystemTimezoneResource() resource.Resource {
	return &systemTimezoneResource{}
}

type systemTimezoneResource struct {
	client *glinet.Client
}

type systemTimezoneResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Zonename            types.String `tfsdk:"zonename"`
	Timezone            types.String `tfsdk:"timezone"`
	TZOffset            types.String `tfsdk:"tzoffset"`
	AutoTimezoneEnabled types.Bool   `tfsdk:"auto_timezone_enabled"`
}

func (r *systemTimezoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_timezone"
}

func (r *systemTimezoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the time zone of the router. The router has one time zone configuration, so use one instance of this resource. " +
			"Destroy removes the resource from the Terraform state and does not change the router. This resource does not change the router clock.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + systemTimezoneID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"zonename": schema.StringAttribute{
				Description: "Time zone name as the router shows it, for example `America/New York`.",
				Required:    true,
			},
			"timezone": schema.StringAttribute{
				Description: "POSIX TZ string, for example `EST5EDT,M3.2.0,M11.1.0`.",
				Required:    true,
			},
			"tzoffset": schema.StringAttribute{
				Description: "Current offset from UTC, for example `-0500`. It changes with daylight saving time.",
				Computed:    true,
			},
			"auto_timezone_enabled": schema.BoolAttribute{
				Description: "Whether the router sets the time zone automatically. The router does not let this resource change it.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *systemTimezoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *systemTimezoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *systemTimezoneResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *systemTimezoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *systemTimezoneResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *systemTimezoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != systemTimezoneID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_system_timezone must be %q, got %q.", systemTimezoneID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), systemTimezoneID)...)
}

func (r *systemTimezoneResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model systemTimezoneResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	err := r.client.System.SetTimezoneConfig(ctx, &glinet.SetSystemTimezoneRequest{
		Zonename: model.Zonename.ValueString(),
		Timezone: model.Timezone.ValueString(),
	})
	if err != nil {
		diags.AddError("Unable to Set System Timezone", err.Error())
		return
	}

	r.read(ctx, state, diags)
}

func (r *systemTimezoneResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.System.GetTimezoneConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read System Timezone", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &systemTimezoneResourceModel{
		ID:                  types.StringValue(systemTimezoneID),
		Zonename:            types.StringValue(config.Zonename),
		Timezone:            types.StringValue(config.Timezone),
		TZOffset:            types.StringValue(config.TZOffset),
		AutoTimezoneEnabled: types.BoolValue(config.AutoTimezoneEnabled),
	})...)
}
