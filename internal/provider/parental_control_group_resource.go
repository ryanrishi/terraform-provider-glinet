package provider

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// minScheduleMinutes is the shortest schedule that the web UI accepts.
const minScheduleMinutes = 15

var scheduleEndValidator = stringCheck{
	description: "must be a time from 00:00 to 24:00 in the format HH:MM, and 24:00 instead of 23:59",
	valid:       func(s string) bool { return s == "24:00" || (validClock(s) && s != "23:59") },
}

var scheduleAttrTypes = map[string]attr.Type{
	"days":       types.SetType{ElemType: types.Int64Type},
	"start_time": types.StringType,
	"end_time":   types.StringType,
	"ruleset":    types.StringType,
}

var (
	_ resource.Resource                   = &parentalControlGroupResource{}
	_ resource.ResourceWithConfigure      = &parentalControlGroupResource{}
	_ resource.ResourceWithImportState    = &parentalControlGroupResource{}
	_ resource.ResourceWithValidateConfig = &parentalControlGroupResource{}
)

func NewParentalControlGroupResource() resource.Resource {
	return &parentalControlGroupResource{}
}

type parentalControlGroupResource struct {
	client *glinet.Client
}

type parentalControlGroupModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	DefaultRuleset types.String `tfsdk:"default_ruleset"`
	MACs           types.Set    `tfsdk:"macs"`
	Schedules      types.Set    `tfsdk:"schedules"`
}

type parentalControlScheduleModel struct {
	Days      types.Set    `tfsdk:"days"`
	StartTime types.String `tfsdk:"start_time"`
	EndTime   types.String `tfsdk:"end_time"`
	Ruleset   types.String `tfsdk:"ruleset"`
}

// daySchedule is a schedule of the router API for one day. begin and end are "HH:MM", and end can be "24:00".
type daySchedule struct {
	week             int
	begin, end, rule string
}

func (r *parentalControlGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_parental_control_group"
}

func (r *parentalControlGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a group of clients of parental control (`parental-control.add_group` in the router API). " +
			"The group applies its default rule set to its clients, and in the time of a schedule the rule set of the schedule.\n\n" +
			parentalControlWarning + "\n\n" +
			parentalControlAccelerationNote + " " +
			parentalControlIDNote + " " +
			"The API reference does not say that group names are unique. The provider uses a name only when one group has it: " +
			"create and a rename fail when another group has the name, and the search by name and the import fail when more than one group has it. " +
			"The router gives each schedule an ID; the provider does not show these IDs, and sends a schedule with its ID when the router has the same schedule. " +
			"The provider writes the schedules only when they are different from the router schedules. " +
			"The resource does not manage the temporary rule set of the group. " +
			"Import the group with its name, for example `terraform import glinet_parental_control_group.example kids`. " +
			untestedWriteNote +
			"Destroy removes the group from the router. Its clients are then in no group, and `drop_anonymous_enabled` of `glinet_parental_control` blocks them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "ID of the group, which the router gives.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the group: 1 to 64 characters.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
				},
			},
			"default_ruleset": schema.StringAttribute{
				Description: "ID of the rule set that applies when no schedule applies (`default_rule` in the router API): " +
					"the `id` of a `glinet_parental_control_ruleset`, or a preset rule set: `drop` blocks all traffic, `accept` blocks nothing.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"macs": schema.SetAttribute{
				Description: "MAC addresses of the clients of the group, in lower case or upper case, for example `02:00:00:00:00:01`. " +
					"The provider sends the MAC addresses as configured, and compares them without letter case, as the web UI does. " +
					"It is not known what the router does with a MAC address that is also in another group.",
				ElementType: caseInsensitiveStringType{},
				Optional:    true,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(macAddress, "must be a MAC address, for example 02:00:00:00:00:01")),
				},
			},
			"schedules": schema.SetNestedAttribute{
				Description: "Weekly schedule of the group: from `start_time` to `end_time` on each day of `days`, the group uses the rule set `ruleset`. " +
					"As in the web UI, a schedule must be at least 15 minutes long, and the schedules of a day must not overlap. " +
					"Two schedules with the same `start_time`, `end_time` and `ruleset` must be one schedule with all their days, because the router keeps one schedule for each day.",
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"days": daysAttribute("Days of the schedule."),
						"start_time": schema.StringAttribute{
							Description: "Start time, format `HH:MM`, 24 hours, in the time zone of the router.",
							Required:    true,
							Validators:  []validator.String{clockValidator},
						},
						"end_time": schema.StringAttribute{
							Description: "End time, format `HH:MM`, 24 hours, in the time zone of the router. `24:00` is the end of the day. " +
								"The web UI also treats `23:59` as the end of the day, so refresh shows a router end `23:59` as `24:00`, and the configuration must use `24:00`. " +
								"It is not known whether the router keeps `24:00`.",
							Required:   true,
							Validators: []validator.String{scheduleEndValidator},
						},
						"ruleset": schema.StringAttribute{
							Description: "ID of the rule set of the schedule, as in `default_ruleset`.",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
					},
				},
			},
		},
	}
}

func (r *parentalControlGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *parentalControlGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config parentalControlGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.MACs.IsUnknown() {
		seen := map[string]bool{}
		for _, mac := range groupMACs(ctx, config.MACs, &resp.Diagnostics) {
			if mac == "" {
				continue
			}
			key := strings.ToLower(mac)
			if seen[key] {
				resp.Diagnostics.AddAttributeError(path.Root("macs"), "Duplicate MAC Address",
					fmt.Sprintf("macs has the MAC address %s more than one time, in different letter case.", mac))
			}
			seen[key] = true
		}
	}

	validateSchedules(ctx, config.Schedules, &resp.Diagnostics)
}

// validateSchedules checks the rules of the web UI for the known values of schedules: the duration of each schedule,
// no overlap on a day, and one item for the same times and rule set. Terraform validates the configuration also
// before it knows all values, so Create and Update check the schedules again.
func validateSchedules(ctx context.Context, schedules types.Set, diags *diag.Diagnostics) {
	if schedules.IsUnknown() || schedules.IsNull() {
		return
	}
	var items []parentalControlScheduleModel
	diags.Append(schedules.ElementsAs(ctx, &items, false)...)

	var days []daySchedule
	seen := map[[3]string]bool{}
	for _, item := range items {
		if item.StartTime.IsUnknown() || item.EndTime.IsUnknown() {
			continue
		}
		begin, end := item.StartTime.ValueString(), item.EndTime.ValueString()
		if !validClock(begin) || !scheduleEndValidator.valid(end) {
			continue
		}
		if clockMinutes(end)-clockMinutes(begin) < minScheduleMinutes {
			diags.AddAttributeError(path.Root("schedules"), "Invalid Schedule",
				fmt.Sprintf("The schedule from %s to %s is shorter than %d minutes. end_time must be at least %d minutes after start_time.",
					begin, end, minScheduleMinutes, minScheduleMinutes))
			continue
		}

		if !item.Ruleset.IsUnknown() {
			k := [3]string{begin, end, item.Ruleset.ValueString()}
			if seen[k] {
				diags.AddAttributeError(path.Root("schedules"), "Invalid Schedule",
					fmt.Sprintf("Two schedules have the start_time %s, the end_time %s and the rule set %q. Make them one schedule with all their days.", begin, end, k[2]))
			}
			seen[k] = true
		}

		if item.Days.IsUnknown() || slices.ContainsFunc(item.Days.Elements(), attr.Value.IsUnknown) {
			continue
		}
		for _, week := range routerDays(ctx, item.Days, diags) {
			days = append(days, daySchedule{week: week, begin: begin, end: end})
		}
	}

	for i, a := range days {
		for _, b := range days[i+1:] {
			if a.week == b.week && a.begin < b.end && b.begin < a.end {
				diags.AddAttributeError(path.Root("schedules"), "Overlapping Schedules",
					fmt.Sprintf("On day %d, the schedule from %s to %s overlaps the schedule from %s to %s.", a.week, a.begin, a.end, b.begin, b.end))
			}
		}
	}
}

func (r *parentalControlGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan parentalControlGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	name := plan.Name.ValueString()
	if i := slices.IndexFunc(config.Groups, func(g glinet.ParentalControlGroup) bool { return g.Name == name }); i >= 0 {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Parental Control Group Exists",
			fmt.Sprintf("The router already has the parental control group %q (ID %q). Import it with the ID %q, or use another name.", name, config.Groups[i].ID, name))
		return
	}
	validateSchedules(ctx, plan.Schedules, &resp.Diagnostics)
	schedules := planSchedules(ctx, plan, &resp.Diagnostics)
	checkRulesets(config, groupRulesets(plan, schedules), &resp.Diagnostics)
	settings := groupSettings(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := r.client.ParentalControl.AddGroup(ctx, settings)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Create Parental Control Group", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)

	if len(schedules) > 0 {
		if err := r.client.ParentalControl.SetSchedules(ctx, id, routerSchedules(schedules, nil)); err != nil {
			resp.Diagnostics.AddError("Unable to Set Parental Control Group Schedules", err.Error())
			return
		}
	}

	r.readBack(ctx, id, plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state parentalControlGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	group, err := findGroup(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Group", err.Error())
		return
	}
	if group == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, groupModel(ctx, group, state, &resp.Diagnostics))...)
}

func (r *parentalControlGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state parentalControlGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	group, err := findGroup(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Group", err.Error())
		return
	}
	if group == nil {
		resp.Diagnostics.AddError("Parental Control Group Not Found",
			fmt.Sprintf("The router has no parental control group with the ID %q or the name %q.", state.ID.ValueString(), state.Name.ValueString()))
		return
	}
	name := plan.Name.ValueString()
	if i := slices.IndexFunc(config.Groups, func(g glinet.ParentalControlGroup) bool { return g.Name == name && g.ID != group.ID }); i >= 0 {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Parental Control Group Exists",
			fmt.Sprintf("The router already has another parental control group with the name %q (ID %q).", name, config.Groups[i].ID))
		return
	}
	validateSchedules(ctx, plan.Schedules, &resp.Diagnostics)
	schedules := planSchedules(ctx, plan, &resp.Diagnostics)
	checkRulesets(config, groupRulesets(plan, schedules), &resp.Diagnostics)
	settings := groupSettings(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if settings.Name != group.Name || settings.DefaultRule != group.DefaultRule || !sameMACs(settings.MACs, group.MACs) {
		if err := r.client.ParentalControl.SetGroup(ctx, group.ID, settings); err != nil {
			resp.Diagnostics.AddError("Unable to Update Parental Control Group", err.Error())
			return
		}
	}

	written := state
	written.ID = types.StringValue(group.ID)
	written.Name = plan.Name
	written.DefaultRuleset = plan.DefaultRuleset
	written.MACs = plan.MACs
	resp.Diagnostics.Append(resp.State.Set(ctx, written)...)

	if !slices.Equal(schedules, daySchedules(group.Schedules)) {
		if err := r.client.ParentalControl.SetSchedules(ctx, group.ID, routerSchedules(schedules, group.Schedules)); err != nil {
			resp.Diagnostics.AddError("Unable to Set Parental Control Group Schedules", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(group.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, group.ID, plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state parentalControlGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	group, err := findGroup(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Group", err.Error())
		return
	}
	if group == nil {
		return
	}

	if err := r.client.ParentalControl.RemoveGroup(ctx, group.ID); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Parental Control Group", err.Error())
	}
}

// ImportState sets the name. The next read finds the group by its name.
func (r *parentalControlGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

func (r *parentalControlGroupResource) readBack(ctx context.Context, id string, prior parentalControlGroupModel, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	i := slices.IndexFunc(config.Groups, func(g glinet.ParentalControlGroup) bool { return g.ID == id })
	if i < 0 {
		diags.AddError("Parental Control Group Not Found", fmt.Sprintf("The router has no parental control group with the ID %q after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, groupModel(ctx, &config.Groups[i], prior, diags))...)
}

// findGroup returns the group of the state, or nil when the router does not have it.
func findGroup(config *glinet.ParentalControlConfig, state parentalControlGroupModel) (*glinet.ParentalControlGroup, error) {
	i, err := findParentalControlItem(config.Groups, groupKey, state.ID.ValueString(), state.Name.ValueString(), "group")
	if err != nil || i < 0 {
		return nil, err
	}
	return &config.Groups[i], nil
}

func groupMACs(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	var macs []caseInsensitiveString
	if !set.IsNull() && !set.IsUnknown() {
		diags.Append(set.ElementsAs(ctx, &macs, false)...)
	}
	values := make([]string, len(macs))
	for i, mac := range macs {
		values[i] = mac.ValueString()
	}
	return values
}

func groupSettings(ctx context.Context, plan parentalControlGroupModel, diags *diag.Diagnostics) glinet.ParentalControlGroupSettings {
	macs := groupMACs(ctx, plan.MACs, diags)
	slices.Sort(macs)
	return glinet.ParentalControlGroupSettings{
		Name:        plan.Name.ValueString(),
		DefaultRule: plan.DefaultRuleset.ValueString(),
		MACs:        macs,
	}
}

func sameMACs(a, b []string) bool {
	lower := func(macs []string) []string {
		values := make([]string, len(macs))
		for i, mac := range macs {
			values[i] = strings.ToLower(mac)
		}
		slices.Sort(values)
		return values
	}
	return slices.Equal(lower(a), lower(b))
}

// groupRulesets returns the rule set IDs that the group uses.
func groupRulesets(plan parentalControlGroupModel, schedules []daySchedule) []string {
	ids := []string{plan.DefaultRuleset.ValueString()}
	for _, s := range schedules {
		if !slices.Contains(ids, s.rule) {
			ids = append(ids, s.rule)
		}
	}
	return ids
}

func newDaySchedule(week int, begin, end, rule string) daySchedule {
	end = trimSeconds(end)
	if end == "23:59" {
		end = "24:00"
	}
	return daySchedule{week, trimSeconds(begin), end, rule}
}

// trimSeconds returns the "HH:MM" part of a time, as the web UI does with the router times.
func trimSeconds(s string) string {
	if len(s) > 5 {
		return s[:5]
	}
	return s
}

// planSchedules returns the schedules of the plan for each day, ordered by day and begin.
func planSchedules(ctx context.Context, plan parentalControlGroupModel, diags *diag.Diagnostics) []daySchedule {
	var items []parentalControlScheduleModel
	if !plan.Schedules.IsNull() {
		diags.Append(plan.Schedules.ElementsAs(ctx, &items, false)...)
	}

	var days []daySchedule
	for _, item := range items {
		for _, week := range routerDays(ctx, item.Days, diags) {
			days = append(days, newDaySchedule(week, item.StartTime.ValueString(), item.EndTime.ValueString(), item.Ruleset.ValueString()))
		}
	}
	sortDaySchedules(days)
	return days
}

// daySchedules returns the router schedules, ordered by day and begin.
func daySchedules(schedules []glinet.ParentalControlSchedule) []daySchedule {
	days := make([]daySchedule, len(schedules))
	for i, s := range schedules {
		days[i] = newDaySchedule(s.Week, s.Begin, s.End, s.Rule)
	}
	sortDaySchedules(days)
	return days
}

// sortDaySchedules sorts by day, then by time: "HH:MM" times sort as strings.
func sortDaySchedules(days []daySchedule) {
	slices.SortFunc(days, func(a, b daySchedule) int {
		return cmp.Or(cmp.Compare(a.week, b.week), cmp.Compare(a.begin, b.begin), cmp.Compare(a.end, b.end), cmp.Compare(a.rule, b.rule))
	})
}

// routerSchedules returns the schedules to send. A schedule that the router has gets its router ID, as the web UI sends it.
func routerSchedules(days []daySchedule, current []glinet.ParentalControlSchedule) []glinet.ParentalControlSchedule {
	used := make([]bool, len(current))
	schedules := make([]glinet.ParentalControlSchedule, len(days))
	for i, d := range days {
		schedules[i] = glinet.ParentalControlSchedule{Week: d.week, Begin: d.begin, End: d.end, Rule: d.rule}
		for j, s := range current {
			if !used[j] && newDaySchedule(s.Week, s.Begin, s.End, s.Rule) == d {
				schedules[i].ID = s.ID
				used[j] = true
				break
			}
		}
	}
	return schedules
}

// clockMinutes returns the minutes after midnight of a valid "HH:MM" time or "24:00".
func clockMinutes(s string) int {
	hour, minute := splitClock(s)
	h, _ := strconv.Atoi(hour)
	m, _ := strconv.Atoi(minute)
	return h*60 + m
}

// groupModel returns the model of the router group. prior gives the empty collections that are not null.
func groupModel(ctx context.Context, group *glinet.ParentalControlGroup, prior parentalControlGroupModel, diags *diag.Diagnostics) *parentalControlGroupModel {
	macs := types.SetNull(caseInsensitiveStringType{})
	if len(group.MACs) > 0 {
		elements := make([]attr.Value, len(group.MACs))
		for i, mac := range group.MACs {
			elements[i] = caseInsensitiveStringValue(mac)
		}
		var d diag.Diagnostics
		macs, d = types.SetValue(caseInsensitiveStringType{}, elements)
		diags.Append(d...)
	}

	return &parentalControlGroupModel{
		ID:             types.StringValue(group.ID),
		Name:           types.StringValue(group.Name),
		DefaultRuleset: types.StringValue(group.DefaultRule),
		MACs:           emptyOr(prior.MACs, macs, len(group.MACs) == 0),
		Schedules:      emptyOr(prior.Schedules, scheduleSet(ctx, group.Schedules, diags), len(group.Schedules) == 0),
	}
}

// scheduleSet returns the router schedules, with the days of the same times and rule set in one item.
func scheduleSet(ctx context.Context, schedules []glinet.ParentalControlSchedule, diags *diag.Diagnostics) types.Set {
	objectType := types.ObjectType{AttrTypes: scheduleAttrTypes}
	if len(schedules) == 0 {
		return types.SetNull(objectType)
	}

	type key struct{ begin, end, rule string }
	var keys []key
	weeks := map[key][]int{}
	for _, d := range daySchedules(schedules) {
		k := key{d.begin, d.end, d.rule}
		if _, seen := weeks[k]; !seen {
			keys = append(keys, k)
		}
		weeks[k] = append(weeks[k], d.week)
	}

	elements := make([]attr.Value, len(keys))
	for i, k := range keys {
		object, d := types.ObjectValueFrom(ctx, scheduleAttrTypes, parentalControlScheduleModel{
			Days:      daysSet(weeks[k]),
			StartTime: types.StringValue(k.begin),
			EndTime:   types.StringValue(k.end),
			Ruleset:   types.StringValue(k.rule),
		})
		diags.Append(d...)
		elements[i] = object
	}

	set, d := types.SetValue(objectType, elements)
	diags.Append(d...)
	return set
}
