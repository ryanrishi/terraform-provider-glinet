package provider

import (
	"context"
	"fmt"
	"math"
	"net/netip"

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

var (
	_ resource.Resource                   = &wireGuardServerRouteResource{}
	_ resource.ResourceWithConfigure      = &wireGuardServerRouteResource{}
	_ resource.ResourceWithImportState    = &wireGuardServerRouteResource{}
	_ resource.ResourceWithValidateConfig = &wireGuardServerRouteResource{}
)

func NewWireGuardServerRouteResource() resource.Resource {
	return &wireGuardServerRouteResource{}
}

type wireGuardServerRouteResource struct {
	client *glinet.Client
}

type wireGuardServerRouteResourceModel struct {
	Destination types.String `tfsdk:"destination"`
	Gateway     types.String `tfsdk:"gateway"`
	Metric      types.Int64  `tfsdk:"metric"`
	MTU         types.Int64  `tfsdk:"mtu"`
}

func (r *wireGuardServerRouteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_server_route"
}

func (r *wireGuardServerRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a route rule of the WireGuard server of the router (`wg-server.add_route` in the router API). " +
			"The destination network identifies the route rule, so a change of `destination` replaces it. " +
			"The router gives each route rule an ID (`rule_id`); the provider finds the ID from `destination` before each call and does not keep it in the state, because it is not known whether the ID stays the same. " +
			"The web UI permits one route rule for each destination, so create fails when the router already has a route rule for `destination`. " +
			"An IPv6 destination makes an IPv6 route rule, and an IPv4 destination an IPv4 route rule. " +
			"The resource does not initialize, start or stop the server. Create fails when the server is not initialized; initialize it with `glinet_wireguard_server` or in the web UI. " +
			"The provider sends `gateway`, `metric` and `mtu` in each write, with `\"\"` and `0` for a value that is not set, as the web UI of firmware 4.8.3 does; what the router does with `\"\"` and `0` is not known. " +
			"The web UI of firmware 4.8.3 does not send a scope, so the provider does not manage it. " +
			"Destroy removes the route rule from the router. " +
			"Import the route rule with its destination, for example `terraform import glinet_wireguard_server_route.example 192.168.10.0/24`. " +
			untestedWriteNote,
		Attributes: map[string]schema.Attribute{
			"destination": schema.StringAttribute{
				Description: "Destination network with a prefix length, for example `192.168.10.0/24` or `fd00:db8:10::/64` (`dest` in the router API). Changing it replaces the route rule.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringCheck{description: "must be an IP address with a prefix length, such as 192.168.10.0/24", valid: validPrefix},
				},
			},
			"gateway": schema.StringAttribute{
				Description: "IP address of the gateway, of the same address family as `destination` (`gateway` in the router API).",
				Optional:    true,
				Validators: []validator.String{
					stringCheck{description: "must be an IP address", valid: validIP},
				},
			},
			"metric": schema.Int64Attribute{
				Description: "Metric of the route, from 1 to 4294967295 (`metric` in the router API).",
				Optional:    true,
				Validators:  []validator.Int64{int64validator.Between(1, math.MaxUint32)},
			},
			"mtu": schema.Int64Attribute{
				Description: "MTU of the route, from 68 to 65535 (`mtu` in the router API).",
				Optional:    true,
				Validators:  []validator.Int64{int64validator.Between(68, 65535)},
			},
		},
	}
}

// ValidateConfig checks that gateway has the address family of destination, as the web UI does.
func (r *wireGuardServerRouteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model wireGuardServerRouteResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || model.Destination.IsNull() || model.Destination.IsUnknown() || model.Gateway.IsNull() || model.Gateway.IsUnknown() {
		return
	}

	dest, err := netip.ParsePrefix(model.Destination.ValueString())
	gateway, gerr := netip.ParseAddr(model.Gateway.ValueString())
	if err != nil || gerr != nil || dest.Addr().Is4() == gateway.Is4() {
		return
	}
	resp.Diagnostics.AddAttributeError(path.Root("gateway"), invalidCombination,
		fmt.Sprintf("gateway %s must have the address family of destination %s.", gateway, dest))
}

func (r *wireGuardServerRouteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wireGuardServerRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wireGuardServerRouteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.WireGuardServer.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Configuration", err.Error())
		return
	}
	if !config.Initialization {
		resp.Diagnostics.AddError("WireGuard Server Not Initialized",
			"The WireGuard server of the router is not initialized. Initialize it with glinet_wireguard_server or in the web UI, then run terraform apply again.")
		return
	}

	dest := plan.Destination.ValueString()
	route, err := r.find(ctx, dest)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Route Rules", err.Error())
		return
	}
	if route != nil {
		resp.Diagnostics.AddError("WireGuard Server Route Rule Already Exists",
			fmt.Sprintf("The router already has a WireGuard server route rule for %s. Import it with the ID %q, or remove it from the router.", dest, dest))
		return
	}

	if err := r.client.WireGuardServer.AddRoute(ctx, plan.route(0)); err != nil {
		resp.Diagnostics.AddError("Unable to Create WireGuard Server Route Rule", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, dest, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wireGuardServerRouteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	route, err := r.find(ctx, state.Destination.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Route Rules", err.Error())
		return
	}
	if route == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wireGuardServerRouteModel(route))...)
}

func (r *wireGuardServerRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan wireGuardServerRouteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	dest := plan.Destination.ValueString()
	route, err := r.find(ctx, dest)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Route Rules", err.Error())
		return
	}
	if route == nil {
		resp.Diagnostics.AddError("WireGuard Server Route Rule Not Found",
			fmt.Sprintf("The router has no WireGuard server route rule for %s. Run terraform apply again to create it.", dest))
		return
	}

	if err := r.client.WireGuardServer.SetRoute(ctx, plan.route(route.ID)); err != nil {
		resp.Diagnostics.AddError("Unable to Update WireGuard Server Route Rule", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, dest, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wireGuardServerRouteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	route, err := r.find(ctx, state.Destination.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Route Rules", err.Error())
		return
	}
	if route == nil {
		return
	}

	if err := r.client.WireGuardServer.RemoveRoute(ctx, *route); err != nil {
		resp.Diagnostics.AddError("Unable to Delete WireGuard Server Route Rule", err.Error())
	}
}

func (r *wireGuardServerRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validPrefix(req.ID) {
		invalidImportID(resp, "glinet_wireguard_server_route", "the destination of the route rule", "192.168.10.0/24", req.ID)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("destination"), req.ID)...)
}

// find returns the route rule for destination, or nil when the router has none.
func (r *wireGuardServerRouteResource) find(ctx context.Context, dest string) (*glinet.VPNRoute, error) {
	routes, err := r.client.WireGuardServer.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}

	for _, list := range [][]glinet.VPNRoute{routes.IPv4, routes.IPv6} {
		for i := range list {
			if list[i].Dest == dest {
				return &list[i], nil
			}
		}
	}
	return nil, nil
}

func (r *wireGuardServerRouteResource) readBack(ctx context.Context, dest string, state *tfsdk.State, diags *diag.Diagnostics) {
	route, err := r.find(ctx, dest)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Server Route Rules", err.Error())
		return
	}
	if route == nil {
		diags.AddError("WireGuard Server Route Rule Not Found", fmt.Sprintf("The router has no WireGuard server route rule for %s after the write.", dest))
		return
	}

	diags.Append(state.Set(ctx, wireGuardServerRouteModel(route))...)
}

func (m wireGuardServerRouteResourceModel) route(id int64) glinet.VPNRoute {
	return glinet.VPNRoute{
		ID:      id,
		Dest:    m.Destination.ValueString(),
		Gateway: m.Gateway.ValueString(),
		Metric:  m.Metric.ValueInt64(),
		MTU:     int(m.MTU.ValueInt64()),
	}
}

func wireGuardServerRouteModel(route *glinet.VPNRoute) *wireGuardServerRouteResourceModel {
	return &wireGuardServerRouteResourceModel{
		Destination: types.StringValue(route.Dest),
		Gateway:     stringOrNull(route.Gateway),
		Metric:      int64OrNull(route.Metric),
		MTU:         int64OrNull(route.MTU),
	}
}
