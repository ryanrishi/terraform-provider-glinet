package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

// defaultRulesetColor is the color that create sends when color is not set. The web UI proposes a random color.
const defaultRulesetColor = "#808080"

var (
	rulesetColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	// blockListLine has the characters that the web UI keeps in a line of the block list.
	blockListLine = regexp.MustCompile(`^[A-Za-z0-9.\[\]~:;/?&=%\-#]+$`)
)

var blocklistURLValidator = stringCheck{
	description: "must be a URL with the scheme http, https, ftp or ftps and a host",
	valid:       validBlocklistURL,
}

func validBlocklistURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && slices.Contains([]string{"http", "https", "ftp", "ftps"}, u.Scheme) && u.Host != ""
}

var (
	_ resource.Resource                = &parentalControlRulesetResource{}
	_ resource.ResourceWithConfigure   = &parentalControlRulesetResource{}
	_ resource.ResourceWithImportState = &parentalControlRulesetResource{}
)

func NewParentalControlRulesetResource() resource.Resource {
	return &parentalControlRulesetResource{}
}

type parentalControlRulesetResource struct {
	client *glinet.Client
}

type parentalControlRulesetModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Color        types.String `tfsdk:"color"`
	Apps         types.Set    `tfsdk:"apps"`
	BlockList    types.List   `tfsdk:"block_list"`
	BlockListURL types.String `tfsdk:"block_list_url"`
}

func (r *parentalControlRulesetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_parental_control_ruleset"
}

func (r *parentalControlRulesetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a rule set of parental control (`parental-control.add_rule` in the router API). " +
			"A rule set blocks apps of the app feature library and domains: the domains of a block list in `block_list`, or of a block list that the router loads from `block_list_url`. " +
			"Groups (`glinet_parental_control_group`) use rule sets as their default rule set and in their schedules. " +
			"The resource does not manage the preset rule sets `drop` (blocks all traffic) and `accept` (blocks nothing), which cannot be changed or removed. " +
			parentalControlIDNote + " " +
			"Rule set names are unique on the router (API reference), so create fails when a rule set with the same name exists. " +
			"Import the rule set with its name, for example `terraform import glinet_parental_control_ruleset.example homework`. " +
			"A write can take up to 30 seconds. " +
			untestedWriteNote +
			"Destroy removes the rule set from the router. Destroy fails while a group uses the rule set as its default rule set, in a schedule, " +
			"or as its temporary rule set.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "ID of the rule set, which the router gives. Use it in `glinet_parental_control_group`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the rule set: 1 to 64 characters. The names `drop_all` and `accept_all` of the preset rule sets are not permitted.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.NoneOf(parentalControlPresetNames...),
				},
			},
			"color": schema.StringAttribute{
				Description: "Color of the label of the rule set in the web UI, in the format `#rrggbb`. " +
					"When it is not set, create sends `" + defaultRulesetColor + "`, and an update keeps the router value.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(rulesetColor, "must be a color in the format #rrggbb"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"apps": schema.SetAttribute{
				Description: "IDs of the apps that the rule set blocks. Get the IDs from the `glinet_parental_control_apps` data source. " +
					"The API reference also permits the app types 1 to 8, which mean all apps of that type. " +
					"The web UI shows the apps only when the router has the app feature library (`app_filter_enabled` of `glinet_parental_control`).",
				ElementType: types.Int64Type,
				Optional:    true,
				Validators: []validator.Set{
					setvalidator.ValueInt64sAre(int64validator.AtLeast(1)),
				},
			},
			"block_list": schema.ListAttribute{
				Description: "Lines of the block list of the rule set (`blacklist` in the router API), for example `www.example.com`. " +
					"Each line can have the characters `A-Z`, `a-z`, `0-9` and `.[]~:;/?&=%-#`. " +
					"The API reference permits at most 32 lines, and also shows lines in the syntax of the app feature library, for example `[tcp;;;www.example.com;;]`. " +
					"The router rejects a domain that is not valid. " +
					"Conflicts with `block_list_url`. When neither is set, the rule set blocks only the apps. Refresh ignores empty lines.",
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(stringvalidator.RegexMatches(blockListLine,
						"must not be empty and can have only the characters A-Z, a-z, 0-9 and .[]~:;/?&=%-#")),
					listvalidator.ConflictsWith(path.MatchRoot("block_list_url")),
				},
			},
			"block_list_url": schema.StringAttribute{
				Description: "URL of a block list that the router loads (`manual: false` in the router API). Conflicts with `block_list`. " +
					"It was not verified that the router returns the URL in its configuration; when it does not, refresh keeps the value of the state.",
				Optional: true,
				Validators: []validator.String{
					blocklistURLValidator,
				},
			},
		},
	}
}

func (r *parentalControlRulesetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *parentalControlRulesetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan parentalControlRulesetModel
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
	if i := slices.IndexFunc(config.Rules, func(rule glinet.ParentalControlRule) bool { return rule.Name == name }); i >= 0 {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Parental Control Rule Set Exists",
			fmt.Sprintf("The router already has the parental control rule set %q (ID %q). Import it with the ID %q, or use another name.", name, config.Rules[i].ID, name))
		return
	}

	if plan.Color.IsUnknown() {
		plan.Color = types.StringValue(defaultRulesetColor)
	}
	settings := rulesetSettings(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := r.client.ParentalControl.AddRule(ctx, settings)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Create Parental Control Rule Set", rulesetWriteError(err))
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlRulesetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state parentalControlRulesetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	rule, err := findRuleset(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Rule Set", err.Error())
		return
	}
	if rule == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, rulesetModel(ctx, rule, state, &resp.Diagnostics))...)
}

func (r *parentalControlRulesetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state parentalControlRulesetModel
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
	rule, err := findRuleset(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Rule Set", err.Error())
		return
	}
	if rule == nil {
		resp.Diagnostics.AddError("Parental Control Rule Set Not Found",
			fmt.Sprintf("The router has no parental control rule set with the ID %q or the name %q.", state.ID.ValueString(), state.Name.ValueString()))
		return
	}
	name := plan.Name.ValueString()
	if i := slices.IndexFunc(config.Rules, func(other glinet.ParentalControlRule) bool { return other.Name == name && other.ID != rule.ID }); i >= 0 {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Parental Control Rule Set Exists",
			fmt.Sprintf("The router already has another parental control rule set with the name %q (ID %q).", name, config.Rules[i].ID))
		return
	}

	if plan.Color.IsUnknown() {
		plan.Color = types.StringValue(rule.Color)
	}
	settings := rulesetSettings(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.ParentalControl.SetRule(ctx, rule.ID, settings); err != nil {
		resp.Diagnostics.AddError("Unable to Update Parental Control Rule Set", rulesetWriteError(err))
		return
	}

	plan.ID = types.StringValue(rule.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, rule.ID, plan, &resp.State, &resp.Diagnostics)
}

func (r *parentalControlRulesetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state parentalControlRulesetModel
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
	rule, err := findRuleset(config, state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Find Parental Control Rule Set", err.Error())
		return
	}
	if rule == nil {
		return
	}

	if users := rulesetUsers(config, rule.ID); len(users) > 0 {
		resp.Diagnostics.AddError("Parental Control Rule Set In Use",
			fmt.Sprintf("The parental control rule set %q (ID %q) is in use: %s. "+
				"The provider does not remove a rule set that a group uses as its default rule set or in a schedule. Change the groups first.",
				rule.Name, rule.ID, users))
		return
	}

	if err := r.client.ParentalControl.RemoveRule(ctx, rule.ID); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Parental Control Rule Set", rulesetRemoveError(err, rule))
	}
}

// ImportState sets the name. The next read finds the rule set by its name.
func (r *parentalControlRulesetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if slices.Contains(parentalControlPresetNames, req.ID) {
		resp.Diagnostics.AddError("Preset Parental Control Rule Set",
			fmt.Sprintf("%q is the name of a preset rule set. The preset rule sets cannot be changed or removed, so the provider does not manage them. "+
				"Use their IDs \"drop\" and \"accept\" directly in glinet_parental_control_group.", req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

func (r *parentalControlRulesetResource) readBack(ctx context.Context, id string, prior parentalControlRulesetModel, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.ParentalControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Parental Control Configuration", err.Error())
		return
	}
	i := slices.IndexFunc(config.Rules, func(rule glinet.ParentalControlRule) bool { return rule.ID == id })
	if i < 0 {
		diags.AddError("Parental Control Rule Set Not Found", fmt.Sprintf("The router has no parental control rule set with the ID %q after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, rulesetModel(ctx, &config.Rules[i], prior, diags))...)
}

// findRuleset returns the rule set of the state, or nil when the router does not have it. It ignores the presets.
func findRuleset(config *glinet.ParentalControlConfig, state parentalControlRulesetModel) (*glinet.ParentalControlRule, error) {
	rules := customRules(config)
	i, err := findParentalControlItem(rules, ruleKey, state.ID.ValueString(), state.Name.ValueString(), "rule set")
	if err != nil || i < 0 {
		return nil, err
	}
	return &rules[i], nil
}

// rulesetUsers describes the groups that use the rule set id as their default rule set or in a schedule, or returns "".
func rulesetUsers(config *glinet.ParentalControlConfig, id string) string {
	var users []string
	for _, g := range config.Groups {
		if g.DefaultRule == id {
			users = append(users, fmt.Sprintf("group %q (ID %q) uses it as default rule set", g.Name, g.ID))
		}
		if slices.ContainsFunc(g.Schedules, func(s glinet.ParentalControlSchedule) bool { return s.Rule == id }) {
			users = append(users, fmt.Sprintf("group %q (ID %q) uses it in a schedule", g.Name, g.ID))
		}
	}
	return strings.Join(users, "; ")
}

func rulesetWriteError(err error) string {
	var rpcErr *glinet.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -2 {
		return err.Error() + "\n\nThe router rejects a domain of the block list (`block_list`) that is not valid."
	}
	return err.Error()
}

func rulesetRemoveError(err error, rule *glinet.ParentalControlRule) string {
	var rpcErr *glinet.RPCError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case -2:
			return fmt.Sprintf("%s\n\nA group uses the rule set %q (ID %q) as its default rule set. Change the group first.", err, rule.Name, rule.ID)
		case -3:
			return fmt.Sprintf("%s\n\nA group uses the rule set %q (ID %q) as its temporary rule set. "+
				"Stop the temporary rule set of the group in the web UI first.", err, rule.Name, rule.ID)
		}
	}
	return err.Error()
}

func rulesetSettings(ctx context.Context, plan parentalControlRulesetModel, diags *diag.Diagnostics) glinet.ParentalControlRuleSettings {
	var apps []int64
	if !plan.Apps.IsNull() {
		diags.Append(plan.Apps.ElementsAs(ctx, &apps, false)...)
	}
	slices.Sort(apps)

	settings := glinet.ParentalControlRuleSettings{
		Name:   plan.Name.ValueString(),
		Color:  plan.Color.ValueString(),
		Apps:   []int{},
		Manual: plan.BlockListURL.IsNull(),
	}
	for _, app := range apps {
		settings.Apps = append(settings.Apps, int(app))
	}
	if settings.Manual {
		settings.Blacklist = listStrings(ctx, plan.BlockList, diags)
	} else {
		settings.URL = plan.BlockListURL.ValueString()
	}
	return settings
}

// rulesetModel returns the model of the router rule set. prior gives the values that the router does not return:
// an empty apps or block_list that is not null, and the URL when the router returns none.
func rulesetModel(ctx context.Context, rule *glinet.ParentalControlRule, prior parentalControlRulesetModel, diags *diag.Diagnostics) *parentalControlRulesetModel {
	apps := make([]int64, len(rule.Apps))
	for i, app := range rule.Apps {
		apps[i] = int64(app)
	}

	model := &parentalControlRulesetModel{
		ID:           types.StringValue(rule.ID),
		Name:         types.StringValue(rule.Name),
		Color:        types.StringValue(rule.Color),
		Apps:         emptyOr(prior.Apps, int64Set(ctx, apps, diags), len(apps) == 0),
		BlockList:    types.ListNull(types.StringType),
		BlockListURL: types.StringNull(),
	}

	if !rule.Manual {
		model.BlockListURL = stringOrNull(rule.URL)
		if rule.URL == "" {
			model.BlockListURL = prior.BlockListURL
		}
		return model
	}

	var lines []string
	for _, line := range rule.Blacklist {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		model.BlockList = stringList(ctx, lines, diags)
	}
	model.BlockList = emptyOr(prior.BlockList, model.BlockList, len(lines) == 0)
	return model
}
