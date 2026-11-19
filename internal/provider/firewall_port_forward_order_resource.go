package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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

const firewallPortForwardOrderID = "firewall_port_forward_order"

// methodNotFound is the JSON-RPC error code of a method that the router does not have.
const methodNotFound = -32601

var (
	_ resource.Resource                   = &firewallPortForwardOrderResource{}
	_ resource.ResourceWithConfigure      = &firewallPortForwardOrderResource{}
	_ resource.ResourceWithImportState    = &firewallPortForwardOrderResource{}
	_ resource.ResourceWithValidateConfig = &firewallPortForwardOrderResource{}
)

func NewFirewallPortForwardOrderResource() resource.Resource {
	return &firewallPortForwardOrderResource{}
}

type firewallPortForwardOrderResource struct {
	client *glinet.Client
}

type firewallPortForwardOrderResourceModel struct {
	ID           types.String                `tfsdk:"id"`
	PortForwards []portForwardOrderItemModel `tfsdk:"port_forwards"`
}

type portForwardOrderItemModel struct {
	ExternalZone types.String `tfsdk:"external_zone"`
	Protocol     types.String `tfsdk:"protocol"`
	ExternalPort types.String `tfsdk:"external_port"`
}

func (m portForwardOrderItemModel) identity() portForwardIdentity {
	return portForwardIdentity{externalZone: m.ExternalZone.ValueString(), protocol: m.Protocol.ValueString(), externalPort: m.ExternalPort.ValueString()}
}

func (r *firewallPortForwardOrderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_port_forward_order"
}

func (r *firewallPortForwardOrderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the order of the port forwards of the firewall, which the web UI of firmware 4.8.3 shows as their priority. " +
			"The router has one order, so use one instance of this resource. " +
			"The port forwards in `port_forwards` go first, in that order. The other port forwards go after them, in their order on the router. " +
			"The provider sends the router IDs of all port forwards in the new order with `order_port_forward`, as the web UI does, " +
			"and sends no write when the order on the router is already correct. " +
			"Create and update fail when the router has no port forward with an identity of `port_forwards`. " +
			"After the write, the provider reads the port forwards again and fails when their order is not the configured order. " +
			"Terraform plans an update when the first port forwards on the router are not those of `port_forwards` in that order. " +
			"Use the attributes of `glinet_firewall_port_forward` resources in `port_forwards`, so that Terraform creates the port forwards first. " +
			"Firmware 4.7.0 does not have `order_port_forward`. " +
			"The write was not tested on a router; the parameters come from the router's web UI. How the order applies to the traffic is not known. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + firewallPortForwardOrderID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"port_forwards": schema.ListNestedAttribute{
				Description: "The port forwards that go first, in that order. " +
					"The external zone, the protocol and the external port identify a port forward, as in `glinet_firewall_port_forward`. " +
					"When the router has more than one port forward with an identity, the first one moves and the others stay with the port forwards that are not in the list.",
				Required: true,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"external_zone": schema.StringAttribute{
							Description: "Firewall zone that the traffic comes from, for example `wan` (`src` in the router API).",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
						"protocol": schema.StringAttribute{
							Description: "Protocol: `tcp`, `udp` or `tcp udp` (`proto` in the router API).",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("tcp", "udp", "tcp udp"),
							},
						},
						"external_port": schema.StringAttribute{
							Description: "External port, from 1 to 65535, or a range of ports such as `8000-8010` (`src_dport` in the router API).",
							Required:    true,
							Validators:  []validator.String{portValidator},
						},
					},
				},
			},
		},
	}
}

func (r *firewallPortForwardOrderResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var items []portForwardOrderItemModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("port_forwards"), &items)...)
	if resp.Diagnostics.HasError() {
		return
	}

	seen := map[portForwardIdentity]bool{}
	for i, item := range items {
		if item.ExternalZone.IsUnknown() || item.Protocol.IsUnknown() || item.ExternalPort.IsUnknown() {
			continue
		}
		id := item.identity()
		if seen[id] {
			resp.Diagnostics.AddAttributeError(path.Root("port_forwards").AtListIndex(i), "Duplicate Port Forward",
				fmt.Sprintf("port_forwards has the port forward with %s more than one time.", id))
		}
		seen[id] = true
	}
}

func (r *firewallPortForwardOrderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *firewallPortForwardOrderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *firewallPortForwardOrderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallPortForwardOrderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	forwards, err := r.client.Firewall.ListPortForwards(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Port Forwards", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, portForwardOrderModel(forwards, state.PortForwards))...)
}

func (r *firewallPortForwardOrderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *firewallPortForwardOrderResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState leaves port_forwards null, so the read takes all port forwards of the router.
func (r *firewallPortForwardOrderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != firewallPortForwardOrderID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_firewall_port_forward_order must be %q, got %q.", firewallPortForwardOrderID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), firewallPortForwardOrderID)...)
}

func (r *firewallPortForwardOrderResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model firewallPortForwardOrderResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	forwards, err := r.client.Firewall.ListPortForwards(ctx)
	if err != nil {
		diags.AddError("Unable to Read Firewall Port Forwards", err.Error())
		return
	}

	ids, missing := orderedPortForwardIDs(forwards, model.PortForwards)
	for _, id := range missing {
		diags.AddError("Firewall Port Forward Not Found", fmt.Sprintf("The router has no port forward with %s.", id))
	}
	if diags.HasError() {
		return
	}

	current := make([]string, len(forwards))
	for i, f := range forwards {
		current[i] = f.ID
	}
	if !slices.Equal(ids, current) {
		if err := r.client.Firewall.OrderPortForwards(ctx, ids); err != nil {
			diags.AddError("Unable to Order Firewall Port Forwards", orderError(err))
			return
		}

		if forwards, err = r.client.Firewall.ListPortForwards(ctx); err != nil {
			diags.AddError("Unable to Read Firewall Port Forwards", err.Error())
			return
		}
	}

	read := portForwardOrderModel(forwards, model.PortForwards)
	if !slices.EqualFunc(read.PortForwards, model.PortForwards, func(a, b portForwardOrderItemModel) bool { return a.identity() == b.identity() }) {
		diags.AddError("Firewall Port Forward Order Not Applied",
			fmt.Sprintf("After the write, the first port forwards of the router are %s.", describeOrder(read.PortForwards)))
		return
	}

	diags.Append(state.Set(ctx, read)...)
}

// orderedPortForwardIDs returns the router IDs of forwards with the first port forward of each identity of items
// first, in the order of items, and then the other port forwards in their order. It also returns the identities
// of items that no port forward has.
func orderedPortForwardIDs(forwards []glinet.PortForward, items []portForwardOrderItemModel) (ids []string, missing []portForwardIdentity) {
	moved := make([]bool, len(forwards))
	for _, item := range items {
		id := item.identity()
		i := slices.IndexFunc(forwards, id.matches)
		if i < 0 {
			missing = append(missing, id)
			continue
		}
		moved[i] = true
		ids = append(ids, forwards[i].ID)
	}

	for i, f := range forwards {
		if !moved[i] {
			ids = append(ids, f.ID)
		}
	}
	return ids, missing
}

// portForwardOrderModel returns the identities of the first len(prior) port forwards of the router.
// When prior is nil, as after an import, it returns the identities of all port forwards.
func portForwardOrderModel(forwards []glinet.PortForward, prior []portForwardOrderItemModel) *firewallPortForwardOrderResourceModel {
	n := len(forwards)
	if prior != nil {
		n = min(len(prior), n)
	}

	items := make([]portForwardOrderItemModel, n)
	for i, f := range forwards[:n] {
		items[i] = portForwardOrderItemModel{
			ExternalZone: types.StringValue(f.Src),
			Protocol:     types.StringValue(f.Proto),
			ExternalPort: types.StringValue(f.SrcDport),
		}
	}

	return &firewallPortForwardOrderResourceModel{
		ID:           types.StringValue(firewallPortForwardOrderID),
		PortForwards: items,
	}
}

func describeOrder(items []portForwardOrderItemModel) string {
	if len(items) == 0 {
		return "none"
	}

	res := make([]string, len(items))
	for i, item := range items {
		res[i] = "(" + item.identity().String() + ")"
	}
	return strings.Join(res, ", ")
}

func orderError(err error) string {
	var rpcErr *glinet.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == methodNotFound {
		return "The router does not have the method firewall.order_port_forward. " +
			"Firmware 4.7.0 does not have it; the web UI of firmware 4.8.3 has it. Update the firmware of the router, or remove this resource. " +
			err.Error()
	}
	return err.Error()
}
