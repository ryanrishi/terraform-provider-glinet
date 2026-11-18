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

const rebootScheduleID = "reboot_schedule"

var (
	_ resource.Resource                = &rebootScheduleResource{}
	_ resource.ResourceWithConfigure   = &rebootScheduleResource{}
	_ resource.ResourceWithImportState = &rebootScheduleResource{}
)

func NewRebootScheduleResource() resource.Resource {
	return &rebootScheduleResource{}
}

type rebootScheduleResource struct {
	client *glinet.Client
}

type rebootScheduleResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Time    types.String `tfsdk:"time"`
	Days    types.Set    `tfsdk:"days"`
}

func (r *rebootScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_reboot_schedule"
}

func (r *rebootScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the reboot schedule of the router: when it is enabled, the router reboots at `time` on the scheduled days. " +
			"The router has one reboot schedule, so use one instance of this resource. " +
			"When `enabled` is `false`, the provider only disables the schedule, and the router keeps its previous time and days: " +
			"the configured `time` and `days` are kept in the Terraform state and get to the router only when the schedule is enabled. " +
			"While the router reports the schedule as disabled, the provider does not compare `time` and `days` with the router. " +
			"The write that enables the schedule (`enable`, `hour`, `min` and `week`) is taken from the web UI of the router and was not tested on a router. " +
			"Destroy disables the schedule on the router, so that the router does not reboot on its own after the resource is removed. " +
			"Destroy sends the same write as `enabled = false`, so the router keeps its time and days.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + rebootScheduleID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the schedule is enabled (`enable` in the router API). When it is `true`, the router reboots at the scheduled times.",
				Required:    true,
			},
			"time": clockAttribute("Time of the reboot (`hour` and `min` in the router API)."),
			"days": daysAttribute("Days of the week when the router reboots."),
		},
	}
}

func (r *rebootScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *rebootScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *rebootScheduleResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *rebootScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *rebootScheduleResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if err := r.client.Timer.SetRebootSchedule(ctx, glinet.RebootSchedule{Enabled: false}); err != nil {
		resp.Diagnostics.AddError("Unable to Disable Reboot Schedule", err.Error())
	}
}

func (r *rebootScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != rebootScheduleID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_reboot_schedule must be %q, got %q.", rebootScheduleID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), rebootScheduleID)...)
}

func (r *rebootScheduleResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model rebootScheduleResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	schedule := glinet.RebootSchedule{Enabled: false}
	if model.Enabled.ValueBool() {
		hour, minute := splitClock(model.Time.ValueString())
		schedule = glinet.RebootSchedule{Enabled: true, Hour: hour, Min: minute, Week: routerDays(ctx, model.Days, diags)}
		if diags.HasError() {
			return
		}
	}

	if err := r.client.Timer.SetRebootSchedule(ctx, schedule); err != nil {
		diags.AddError("Unable to Set Reboot Schedule", err.Error())
		return
	}

	model.ID = types.StringValue(rebootScheduleID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

// read keeps time and days of the state while the router reports the schedule as disabled,
// because a disabled write does not change them on the router.
func (r *rebootScheduleResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	var prior rebootScheduleResourceModel
	diags.Append(state.Get(ctx, &prior)...)
	if diags.HasError() {
		return
	}

	schedule, err := r.client.Timer.GetRebootSchedule(ctx)
	if err != nil {
		diags.AddError("Unable to Read Reboot Schedule", err.Error())
		return
	}

	model := rebootScheduleResourceModel{
		ID:      types.StringValue(rebootScheduleID),
		Enabled: types.BoolValue(schedule.Enabled),
		Time:    types.StringValue(joinClock(schedule.Hour, schedule.Min)),
		Days:    daysSet(schedule.Week),
	}
	if !schedule.Enabled && !prior.Time.IsNull() && !prior.Days.IsNull() {
		model.Time = prior.Time
		model.Days = prior.Days
	}

	diags.Append(state.Set(ctx, &model)...)
}
