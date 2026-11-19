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

const screenScheduleID = "screen_schedule"

var (
	_ resource.Resource                     = &screenScheduleResource{}
	_ resource.ResourceWithConfigure        = &screenScheduleResource{}
	_ resource.ResourceWithImportState      = &screenScheduleResource{}
	_ resource.ResourceWithConfigValidators = &screenScheduleResource{}
)

func NewScreenScheduleResource() resource.Resource {
	return &screenScheduleResource{}
}

type screenScheduleResource struct {
	client *glinet.Client
}

type screenScheduleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	TurnOnTime  types.String `tfsdk:"turn_on_time"`
	TurnOffTime types.String `tfsdk:"turn_off_time"`
	Days        types.Set    `tfsdk:"days"`
}

func (r *screenScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_screen_schedule"
}

func (r *screenScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the schedule of the LCD screen of the router (`timer.get_screen` and `timer.set_screen`): " +
			"on the scheduled days, the screen turns off at `turn_off_time` and on at `turn_on_time`. " +
			"The router has one screen schedule, so use one instance of this resource. " +
			"Only a router with a screen (`hardware_feature.screen` of `system.get_info`) has this setting: " +
			"on other routers, create and update fail and do not write to the router. " +
			"The resource works like `glinet_led_schedule`: when `enabled` is `false`, the provider first writes the schedule enabled " +
			"with the configured times and days, and then disables it. That the router keeps the times and days only from a write that enables the schedule, " +
			"as for the LED schedule, is not known. The writes are from the web UI code of firmware 4.8.3 and were not tested on a router. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + screenScheduleID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the schedule is enabled (`enable` in the router API).",
				Required:    true,
			},
			"turn_on_time": clockAttribute("Time when the screen turns on (`turnon_hour` and `turnon_min` in the router API). " +
				"It must not be the same as `turn_off_time`, as in the web UI."),
			"turn_off_time": clockAttribute("Time when the screen turns off (`turnoff_hour` and `turnoff_min` in the router API)."),
			"days":          daysAttribute("Days of the week when the schedule runs."),
		},
	}
}

func (r *screenScheduleResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "turn_on_time and turn_off_time must not be the same",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config screenScheduleResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.TurnOnTime.IsUnknown() || config.TurnOffTime.IsUnknown() {
				return
			}
			if config.TurnOnTime.Equal(config.TurnOffTime) {
				diags.AddAttributeError(path.Root("turn_on_time"), invalidCombination, "turn_on_time and turn_off_time must not be the same.")
			}
		},
	}}
}

func (r *screenScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *screenScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *screenScheduleResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *screenScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *screenScheduleResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *screenScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != screenScheduleID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_screen_schedule must be %q, got %q.", screenScheduleID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), screenScheduleID)...)
}

func (r *screenScheduleResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model screenScheduleResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	turnOnHour, turnOnMin := splitClock(model.TurnOnTime.ValueString())
	turnOffHour, turnOffMin := splitClock(model.TurnOffTime.ValueString())
	week := routerDays(ctx, model.Days, diags)
	if diags.HasError() {
		return
	}

	if !requireHardware(ctx, r.client, "screen", "screen", func(h glinet.HardwareFeature) bool { return h.Screen }, diags) {
		return
	}

	err := r.client.Timer.SetScreenSchedule(ctx, glinet.ScreenSchedule{
		Enabled:     true,
		TurnOnHour:  turnOnHour,
		TurnOnMin:   turnOnMin,
		TurnOffHour: turnOffHour,
		TurnOffMin:  turnOffMin,
		Week:        week,
	})
	if err != nil {
		diags.AddError("Unable to Set Screen Schedule", err.Error())
		return
	}

	model.ID = types.StringValue(screenScheduleID)
	enabled := model
	enabled.Enabled = types.BoolValue(true)
	diags.Append(state.Set(ctx, &enabled)...)
	if diags.HasError() {
		return
	}

	if !model.Enabled.ValueBool() {
		if err := r.client.Timer.SetScreenSchedule(ctx, glinet.ScreenSchedule{Enabled: false}); err != nil {
			diags.AddError("Unable to Disable Screen Schedule",
				"The router has the schedule enabled with the configured times and days: "+err.Error())
			return
		}

		diags.Append(state.Set(ctx, &model)...)
		if diags.HasError() {
			return
		}
	}

	r.read(ctx, state, diags)
}

func (r *screenScheduleResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	schedule, err := r.client.Timer.GetScreenSchedule(ctx)
	if err != nil {
		diags.AddError("Unable to Read Screen Schedule", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &screenScheduleResourceModel{
		ID:          types.StringValue(screenScheduleID),
		Enabled:     types.BoolValue(schedule.Enabled),
		TurnOnTime:  types.StringValue(joinClock(schedule.TurnOnHour, schedule.TurnOnMin)),
		TurnOffTime: types.StringValue(joinClock(schedule.TurnOffHour, schedule.TurnOffMin)),
		Days:        daysSet(schedule.Week),
	})...)
}
