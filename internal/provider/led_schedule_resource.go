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

const ledScheduleID = "led_schedule"

var (
	_ resource.Resource                = &ledScheduleResource{}
	_ resource.ResourceWithConfigure   = &ledScheduleResource{}
	_ resource.ResourceWithImportState = &ledScheduleResource{}
)

func NewLEDScheduleResource() resource.Resource {
	return &ledScheduleResource{}
}

type ledScheduleResource struct {
	client *glinet.Client
}

type ledScheduleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	TurnOnTime  types.String `tfsdk:"turn_on_time"`
	TurnOffTime types.String `tfsdk:"turn_off_time"`
	Days        types.Set    `tfsdk:"days"`
}

func (r *ledScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_led_schedule"
}

func (r *ledScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the LED schedule of the router: on the scheduled days, the LEDs turn off at `turn_off_time` and on at `turn_on_time`. " +
			"The router has one LED schedule, so use one instance of this resource. The schedule does not change `glinet_led`. " +
			"The router keeps the times and days only from a write that enables the schedule. So when `enabled` is `false`, " +
			"the provider first writes the schedule enabled with the configured times and days, and then disables it. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + ledScheduleID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the schedule is enabled (`enable` in the router API).",
				Required:    true,
			},
			"turn_on_time":  clockAttribute("Time when the LEDs turn on (`turnon_hour` and `turnon_min` in the router API)."),
			"turn_off_time": clockAttribute("Time when the LEDs turn off (`turnoff_hour` and `turnoff_min` in the router API)."),
			"days":          daysAttribute("Days of the week when the schedule runs."),
		},
	}
}

func (r *ledScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *ledScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ledScheduleResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *ledScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ledScheduleResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *ledScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != ledScheduleID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_led_schedule must be %q, got %q.", ledScheduleID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ledScheduleID)...)
}

func (r *ledScheduleResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model ledScheduleResourceModel
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

	err := r.client.Timer.SetLEDSchedule(ctx, glinet.LEDSchedule{
		Enabled:     true,
		TurnOnHour:  turnOnHour,
		TurnOnMin:   turnOnMin,
		TurnOffHour: turnOffHour,
		TurnOffMin:  turnOffMin,
		Week:        week,
	})
	if err != nil {
		diags.AddError("Unable to Set LED Schedule", err.Error())
		return
	}

	model.ID = types.StringValue(ledScheduleID)
	enabled := model
	enabled.Enabled = types.BoolValue(true)
	diags.Append(state.Set(ctx, &enabled)...)
	if diags.HasError() {
		return
	}

	if !model.Enabled.ValueBool() {
		if err := r.client.Timer.SetLEDSchedule(ctx, glinet.LEDSchedule{Enabled: false}); err != nil {
			diags.AddError("Unable to Disable LED Schedule",
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

func (r *ledScheduleResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	schedule, err := r.client.Timer.GetLEDSchedule(ctx)
	if err != nil {
		diags.AddError("Unable to Read LED Schedule", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &ledScheduleResourceModel{
		ID:          types.StringValue(ledScheduleID),
		Enabled:     types.BoolValue(schedule.Enabled),
		TurnOnTime:  types.StringValue(joinClock(schedule.TurnOnHour, schedule.TurnOnMin)),
		TurnOffTime: types.StringValue(joinClock(schedule.TurnOffHour, schedule.TurnOffMin)),
		Days:        daysSet(schedule.Week),
	})...)
}
