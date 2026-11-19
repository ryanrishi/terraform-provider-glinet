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

const fanID = "fan"

var (
	_ resource.Resource                = &fanResource{}
	_ resource.ResourceWithConfigure   = &fanResource{}
	_ resource.ResourceWithImportState = &fanResource{}
)

func NewFanResource() resource.Resource {
	return &fanResource{}
}

type fanResource struct {
	client *glinet.Client
}

type fanResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Temperature     types.Int64  `tfsdk:"temperature"`
	WarnTemperature types.Int64  `tfsdk:"warn_temperature"`
}

func (r *fanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fan"
}

func (r *fanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets the temperature at which the fan of the router starts (`fan.get_config` and `fan.set_config`). " +
			"The router has one fan configuration, so use one instance of this resource. " +
			"Only a router with a fan (`hardware_feature.fan` of `system.get_info`) has this setting: on other routers, create and update fail and do not write to the router. " +
			"For the speed of the fan, use the data source `glinet_fan_status`. " +
			"The write was tested on a GL-AXT1800 with firmware 4.8.3: the router accepted `temperature` 60, and the fan started. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + fanID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"temperature": schema.Int64Attribute{
				Description: "The fan starts when the CPU temperature is more than this value, in °C (`temperature` in the router API). " +
					"The web UI lets you select 70 to 90. The provider accepts 0 to 90: " +
					"a value more than 90 can let the router get too hot before the fan starts, and a lower value only makes the fan run more. " +
					"A GL-AXT1800 on firmware 4.8.3 accepted 60, and the fan started. Other values less than 70 were not tested on a router.",
				Required: true,
				Validators: []validator.Int64{
					int64validator.Between(0, 90),
				},
			},
			"warn_temperature": schema.Int64Attribute{
				Description: "Temperature in °C that the router gives and that you cannot set (`warn_temperature` in the router API). " +
					"The web UI shows a warning when `temperature` is this value or less. If a write changes it is not known.",
				Computed: true,
			},
		},
	}
}

func (r *fanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *fanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *fanResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *fanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *fanResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *fanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != fanID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_fan must be %q, got %q.", fanID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fanID)...)
}

func (r *fanResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model fanResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if !requireHardware(ctx, r.client, "fan", "fan", func(h glinet.HardwareFeature) bool { return h.Fan }, diags) {
		return
	}

	if err := r.client.Fan.SetTemperature(ctx, int(model.Temperature.ValueInt64())); err != nil {
		diags.AddError("Unable to Set Fan Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(fanID)
	model.WarnTemperature = types.Int64Null()
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *fanResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.Fan.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Fan Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &fanResourceModel{
		ID:              types.StringValue(fanID),
		Temperature:     types.Int64Value(int64(config.Temperature)),
		WarnTemperature: types.Int64Value(int64(config.WarnTemperature)),
	})...)
}
