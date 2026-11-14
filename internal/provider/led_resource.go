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

const ledID = "led"

var (
	_ resource.Resource                = &ledResource{}
	_ resource.ResourceWithConfigure   = &ledResource{}
	_ resource.ResourceWithImportState = &ledResource{}
)

func NewLEDResource() resource.Resource {
	return &ledResource{}
}

type ledResource struct {
	client *glinet.Client
}

type ledResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func (r *ledResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_led"
}

func (r *ledResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Turns the LEDs of the router on or off. The router has one LED configuration, so use one instance of this resource. " +
			"The LED schedule (`glinet_led_schedule`) does not change this setting. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + ledID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the LEDs are on (`led_enable` in the router API).",
				Required:    true,
			},
		},
	}
}

func (r *ledResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *ledResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ledResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *ledResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ledResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *ledResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != ledID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_led must be %q, got %q.", ledID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ledID)...)
}

func (r *ledResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model ledResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if err := r.client.LED.SetConfig(ctx, glinet.LEDConfig{Enabled: model.Enabled.ValueBool()}); err != nil {
		diags.AddError("Unable to Set LED Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(ledID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *ledResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.LED.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read LED Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &ledResourceModel{
		ID:      types.StringValue(ledID),
		Enabled: types.BoolValue(config.Enabled),
	})...)
}
