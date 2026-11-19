package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
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

const multiWANID = "multiwan"

const multiWANWarning = "~> **Warning:** a write can change the uplink that the router uses, and so can cut the Internet access of the router and of its clients.\n\n"

const (
	multiWANFailover    = "failover"
	multiWANLoadBalance = "load_balance"
)

// multiWANModes has the mode names at the index of their router value.
var multiWANModes = []string{multiWANFailover, multiWANLoadBalance}

var (
	_ resource.Resource                     = &multiWANResource{}
	_ resource.ResourceWithConfigure        = &multiWANResource{}
	_ resource.ResourceWithImportState      = &multiWANResource{}
	_ resource.ResourceWithConfigValidators = &multiWANResource{}
)

func NewMultiWANResource() resource.Resource {
	return &multiWANResource{}
}

type multiWANResource struct {
	client *glinet.Client
}

type multiWANResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Mode     types.String `tfsdk:"mode"`
	Priority types.List   `tfsdk:"priority"`
	Weights  types.Map    `tfsdk:"weights"`
}

// enumName returns the name at the index value of names, and false when names has no such index.
func enumName(names []string, value int) (string, bool) {
	if value < 0 || value >= len(names) {
		return "", false
	}
	return names[value], true
}

func (r *multiWANResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multiwan"
}

func (r *multiWANResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the multi-WAN mode of the router (`kmwan.set_config` in the router API): failover or load balance between the WAN interfaces, " +
			"for example `wan`, `secondwan`, `wwan` (repeater) and `tethering`. " +
			"The router has one multi-WAN configuration, so use one instance of this resource.\n\n" +
			multiWANWarning +
			"In failover mode, the router uses the online interface that comes first in `priority`. " +
			"In load balance mode, it divides the traffic between the online interfaces by their `weights`. " +
			"The tracking of each interface is in `glinet_multiwan_interface`, the sensitivity of the tracking is in `glinet_multiwan_sensitivity`, " +
			"and the state of the interfaces is in the data source `glinet_multiwan_status`. " +
			"The provider writes only when the router values differ from the configuration, so a create whose values are equal to those of the router sends no write. " +
			"A failover write does not send the weights, and a load balance write does not send the metrics, as the web UI does. " +
			"It is not known whether the router keeps them. So the provider reads `priority` only in failover mode and `weights` only in load balance mode; " +
			"the attribute of the other mode is null. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + multiWANID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				Description: "Multi-WAN mode: `failover` or `load_balance` (`mode` `0` or `1` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(multiWANModes...),
				},
			},
			"priority": schema.ListAttribute{
				Description: "Names of the interfaces in the failover order, the first one first. Can be set only when `mode` is `failover`. " +
					"The provider sends the metrics 1, 2, 3 and so on in this order (`metric` in the router API). " +
					"As the web UI does, it sends all interfaces of the router: the interfaces that are not in the list come after the listed ones, in their current order on the router. " +
					"So the list can have only the first interfaces of the order; the provider then compares the list with the first interfaces of the router order. " +
					"Create and update fail without a write when an interface is not on the router. " +
					"When it is not set, the order on the router does not change: the provider sends the current order.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"weights": schema.MapAttribute{
				Description: "Weight from 0 to 10 of each interface, by interface name (`weight` in the router API). Can be set only when `mode` is `load_balance`. " +
					"The provider sends the weights of all interfaces of the router: an interface that is not in the map keeps its current weight. " +
					"The web UI does not send the interfaces that it does not show (`hidden_features` of `system.get_info`); the provider sends them with their current weight. " +
					"The provider compares only the interfaces of the map with the router. " +
					"Create and update fail without a write when an interface is not on the router. " +
					"The web UI warns when fewer than two interfaces have a weight that is not 0. What weight 0 does is not known. " +
					"When it is not set, the weights on the router do not change.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.KeysAre(stringvalidator.LengthAtLeast(1)),
					mapvalidator.ValueInt64sAre(int64validator.Between(0, 10)),
				},
			},
		},
	}
}

func (r *multiWANResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "priority goes with mode failover, and weights goes with mode load_balance",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config multiWANResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.Mode.IsNull() || config.Mode.IsUnknown() {
				return
			}

			if config.Mode.ValueString() == multiWANFailover {
				forbid(diags, "weights", config.Weights, "when mode is failover")
			} else {
				forbid(diags, "priority", config.Priority, "when mode is load_balance")
			}
		},
	}}
}

func (r *multiWANResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *multiWANResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior multiWANResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.read(ctx, prior, &resp.State, &resp.Diagnostics)
}

func (r *multiWANResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *multiWANResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != multiWANID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_multiwan must be %q, got %q.", multiWANID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), multiWANID)...)
}

// write sends the mode of plan with its priority or its weights when they differ from the router. An unknown
// priority or weights, which is not in the configuration, keeps the router values. It holds routerWrites from the
// read to the write, so that the write sends back the router values of the interfaces that plan does not have.
func (r *multiWANResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model multiWANResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.MultiWAN.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Multi-WAN Configuration", err.Error())
		return
	}
	current := multiWANModel(ctx, config, model, diags)
	if diags.HasError() {
		return
	}
	modeChanged := !model.Mode.Equal(current.Mode)

	if model.Mode.ValueString() == multiWANFailover {
		var priority []string
		if !model.Priority.IsUnknown() {
			priority = listStrings(ctx, model.Priority, diags)
		}
		checkMultiWANInterfaces(config, "priority", priority, diags)
		if diags.HasError() {
			return
		}
		if modeChanged || (!model.Priority.IsUnknown() && !model.Priority.Equal(current.Priority)) {
			if err := r.client.MultiWAN.SetFailover(ctx, failoverOrder(config, priority)); err != nil {
				diags.AddError("Unable to Set Multi-WAN Failover", err.Error())
				return
			}
		}
	} else {
		weights := map[string]int64{}
		if !model.Weights.IsUnknown() {
			diags.Append(model.Weights.ElementsAs(ctx, &weights, false)...)
		}
		checkMultiWANInterfaces(config, "weights", mapKeys(weights), diags)
		if diags.HasError() {
			return
		}
		if modeChanged || (!model.Weights.IsUnknown() && !model.Weights.Equal(current.Weights)) {
			if err := r.client.MultiWAN.SetLoadBalance(ctx, loadBalanceWeights(config, weights)); err != nil {
				diags.AddError("Unable to Set Multi-WAN Load Balance", err.Error())
				return
			}
		}
	}

	saved := model
	saved.ID = types.StringValue(multiWANID)
	if saved.Priority.IsUnknown() {
		saved.Priority = types.ListNull(types.StringType)
	}
	if saved.Weights.IsUnknown() {
		saved.Weights = types.MapNull(types.Int64Type)
	}
	diags.Append(state.Set(ctx, &saved)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, model, state, diags)
}

// read sets the router values in the state, with the priority and the weights shaped as those of prior.
func (r *multiWANResource) read(ctx context.Context, prior multiWANResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.MultiWAN.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Multi-WAN Configuration", err.Error())
		return
	}

	model := multiWANModel(ctx, config, prior, diags)
	if diags.HasError() {
		return
	}
	diags.Append(state.Set(ctx, model)...)
}

// multiWANModel returns the model of config. It has the priority only in failover mode and the weights only in load
// balance mode. When shape has a priority, the priority has only as many interfaces as that of shape, because a
// configured priority can have only the first interfaces of the order. When shape has weights, the weights have
// only the interfaces of shape. Else they have all interfaces.
func multiWANModel(ctx context.Context, config *glinet.MultiWANConfig, shape multiWANResourceModel, diags *diag.Diagnostics) *multiWANResourceModel {
	mode, ok := enumName(multiWANModes, config.Mode)
	if !ok {
		diags.AddError("Unknown Multi-WAN Mode", fmt.Sprintf("The router has the multi-WAN mode %d, which the provider does not know.", config.Mode))
		return nil
	}

	model := &multiWANResourceModel{
		ID:       types.StringValue(multiWANID),
		Mode:     types.StringValue(mode),
		Priority: types.ListNull(types.StringType),
		Weights:  types.MapNull(types.Int64Type),
	}

	if mode == multiWANFailover {
		order := interfacesByMetric(config)
		if n := len(shape.Priority.Elements()); n > 0 && n < len(order) {
			order = order[:n]
		}
		model.Priority = stringList(ctx, order, diags)
		return model
	}

	var keys []string
	if !shape.Weights.IsNull() && !shape.Weights.IsUnknown() {
		keys = mapKeys(shape.Weights.Elements())
	}
	weights := map[string]int64{}
	for _, iface := range config.Interfaces {
		if keys == nil || slices.Contains(keys, iface.Interface) {
			weights[iface.Interface] = int64(iface.Weight)
		}
	}
	m, d := types.MapValueFrom(ctx, types.Int64Type, weights)
	diags.Append(d...)
	model.Weights = m
	return model
}

func mapKeys[V any](m map[string]V) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func multiWANInterfaceNames(config *glinet.MultiWANConfig) []string {
	names := make([]string, len(config.Interfaces))
	for i, iface := range config.Interfaces {
		names[i] = iface.Interface
	}
	return names
}

// checkMultiWANInterfaces adds an error for each name that is not an interface of config.
func checkMultiWANInterfaces(config *glinet.MultiWANConfig, attribute string, names []string, diags *diag.Diagnostics) {
	known := multiWANInterfaceNames(config)
	for _, name := range names {
		if !slices.Contains(known, name) {
			diags.AddAttributeError(path.Root(attribute), "Multi-WAN Interface Not Found",
				fmt.Sprintf("The router has no multi-WAN interface %q. The interfaces of the router are: %s.", name, strings.Join(known, ", ")))
		}
	}
}

// interfacesByMetric returns the interface names of config in the failover order: the lowest metric first.
func interfacesByMetric(config *glinet.MultiWANConfig) []string {
	interfaces := slices.Clone(config.Interfaces)
	slices.SortStableFunc(interfaces, func(a, b glinet.MultiWANInterface) int { return a.Metric - b.Metric })
	names := make([]string, len(interfaces))
	for i, iface := range interfaces {
		names[i] = iface.Interface
	}
	return names
}

// failoverOrder returns priority, then the other interfaces of config in their current failover order.
func failoverOrder(config *glinet.MultiWANConfig, priority []string) []string {
	order := slices.Clone(priority)
	for _, name := range interfacesByMetric(config) {
		if !slices.Contains(priority, name) {
			order = append(order, name)
		}
	}
	return order
}

// loadBalanceWeights returns each interface of config with its weight in weights, or else with its current weight.
func loadBalanceWeights(config *glinet.MultiWANConfig, weights map[string]int64) []glinet.MultiWANWeight {
	result := make([]glinet.MultiWANWeight, len(config.Interfaces))
	for i, iface := range config.Interfaces {
		weight := iface.Weight
		if w, ok := weights[iface.Interface]; ok {
			weight = int(w)
		}
		result[i] = glinet.MultiWANWeight{Interface: iface.Interface, Weight: weight}
	}
	return result
}
