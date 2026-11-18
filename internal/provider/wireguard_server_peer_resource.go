package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ resource.Resource                = &wireGuardServerPeerResource{}
	_ resource.ResourceWithConfigure   = &wireGuardServerPeerResource{}
	_ resource.ResourceWithImportState = &wireGuardServerPeerResource{}
)

func NewWireGuardServerPeerResource() resource.Resource {
	return &wireGuardServerPeerResource{}
}

type wireGuardServerPeerResource struct {
	client *glinet.Client
}

type wireGuardServerPeerResourceModel struct {
	ID                  types.Int64  `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	PresharedKey        types.String `tfsdk:"preshared_key"`
	AllowedIPs          types.List   `tfsdk:"allowed_ips"`
	DNS                 types.List   `tfsdk:"dns"`
	PersistentKeepalive types.Int64  `tfsdk:"persistent_keepalive"`
	MTU                 types.Int64  `tfsdk:"mtu"`
	ClientIP            types.String `tfsdk:"client_ip"`
}

func (r *wireGuardServerPeerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_server_peer"
}

func (r *wireGuardServerPeerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a peer of the WireGuard server of the router: a client device that can connect (`wg-server.add_peer` in the router API). " +
			"The router gives the peer its tunnel address and its keys. " +
			"The resource does not give the configuration file of the client device; export it in the web UI. " +
			"The resource does not initialize, start or stop the server. Create fails when the server is not initialized; initialize it in the web UI. " +
			"Destroy removes the peer from the router. " +
			"Import the peer with its ID, for example `terraform import glinet_wireguard_server_peer.example 1234`. " +
			untestedWriteNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "ID of the peer, which the router gives (`peer_id` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the peer (`name` in the router API): 1 to 64 characters, not only spaces, without a `.` at the start and without `~`.",
				Required:    true,
				Validators:  []validator.String{wireGuardNameValidator},
			},
			"preshared_key": schema.StringAttribute{
				Description: "Preshared key, 44 characters of base64 (`presharedkey` in the router API). " +
					"The provider sends `presharedkey_enable` `true` when it is set and `false` when it is not set.",
				Optional:   true,
				Sensitive:  true,
				Validators: []validator.String{wireGuardSecretKeyValidator},
			},
			"allowed_ips": schema.ListAttribute{
				Description: "1 to 5 IP networks for the configuration of the client device, for example `[\"0.0.0.0/0\"]` " +
					"(`allowed_ips` in the router API, one string with `, ` between the items). " + wireGuardRouterValueNote,
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 5),
					listvalidator.ValueStringsAre(stringCheck{description: "must be an IP address with a prefix length, such as 0.0.0.0/0", valid: validPrefix}),
				},
			},
			"dns": schema.ListAttribute{
				Description: "IP addresses of the DNS servers for the configuration of the client device (`dns` in the router API, one string with `, ` between the items). " +
					wireGuardRouterValueNote,
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.ValueStringsAre(stringCheck{description: "must be an IP address", valid: validIP}),
				},
			},
			"persistent_keepalive": schema.Int64Attribute{
				Description: "Interval of the keepalive packets in seconds, from 1 to 65535 (`persistent_keepalive` in the router API). " + wireGuardRouterValueNote,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{int64validator.Between(1, 65535)},
			},
			"mtu": schema.Int64Attribute{
				Description: "MTU of the tunnel, from 576 to 65535 (`mtu` in the router API). " + wireGuardRouterValueNote,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{int64validator.Between(576, 65535)},
			},
			"client_ip": schema.StringAttribute{
				Description: "Address of the client device in the tunnel, which the router gives (`client_ip` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *wireGuardServerPeerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wireGuardServerPeerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wireGuardServerPeerResourceModel
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
			"The WireGuard server of the router is not initialized, so it has no keys. Initialize it in the web UI, then run terraform apply again.")
		return
	}

	peer := plan.peer(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.client.WireGuardServer.AddPeer(ctx, peer)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Create WireGuard Server Peer", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(id))
	plan.nullUnknown()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerPeerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wireGuardServerPeerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peer, err := r.find(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Peers", err.Error())
		return
	}
	if peer == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wireGuardServerPeerModel(ctx, peer, &resp.Diagnostics))...)
}

func (r *wireGuardServerPeerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan wireGuardServerPeerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	peer := plan.peer(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.WireGuardServer.SetPeer(ctx, peer); err != nil {
		resp.Diagnostics.AddError("Unable to Update WireGuard Server Peer", err.Error())
		return
	}

	plan.nullUnknown()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, peer.ID, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerPeerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wireGuardServerPeerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := int(state.ID.ValueInt64())
	peer, err := r.find(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server Peers", err.Error())
		return
	}
	if peer == nil {
		return
	}

	if err := r.client.WireGuardServer.RemovePeer(ctx, id); err != nil {
		resp.Diagnostics.AddError("Unable to Delete WireGuard Server Peer", err.Error())
	}
}

func (r *wireGuardServerPeerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, ok := parseRouterID(req.ID)
	if !ok {
		invalidImportID(resp, "glinet_wireguard_server_peer", "the peer ID", "1234", req.ID)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), int64(id))...)
}

// find returns the peer id, or nil when the router has no such peer.
func (r *wireGuardServerPeerResource) find(ctx context.Context, id int) (*glinet.WireGuardServerPeer, error) {
	peers, err := r.client.WireGuardServer.ListPeers(ctx)
	if err != nil {
		return nil, err
	}

	i := slices.IndexFunc(peers, func(p glinet.WireGuardServerPeer) bool { return p.ID == id })
	if i < 0 {
		return nil, nil
	}
	return &peers[i], nil
}

func (r *wireGuardServerPeerResource) readBack(ctx context.Context, id int, state *tfsdk.State, diags *diag.Diagnostics) {
	peer, err := r.find(ctx, id)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Server Peers", err.Error())
		return
	}
	if peer == nil {
		diags.AddError("WireGuard Server Peer Not Found", fmt.Sprintf("The router has no WireGuard server peer with the ID %d after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, wireGuardServerPeerModel(ctx, peer, diags))...)
}

func (m wireGuardServerPeerResourceModel) peer(ctx context.Context, diags *diag.Diagnostics) glinet.WireGuardServerPeer {
	return glinet.WireGuardServerPeer{
		ID:                  int(m.ID.ValueInt64()),
		Name:                m.Name.ValueString(),
		PresharedKeyEnable:  !m.PresharedKey.IsNull(),
		PresharedKey:        m.PresharedKey.ValueString(),
		AllowedIPs:          joinList(ctx, m.AllowedIPs, diags),
		DNS:                 joinList(ctx, m.DNS, diags),
		PersistentKeepalive: int(m.PersistentKeepalive.ValueInt64()),
		MTU:                 int(m.MTU.ValueInt64()),
	}
}

// nullUnknown sets the attributes that the router gives to null when they are unknown, so that the state
// that is saved before the read-back has no unknown value.
func (m *wireGuardServerPeerResourceModel) nullUnknown() {
	for _, v := range []*types.List{&m.AllowedIPs, &m.DNS} {
		if v.IsUnknown() {
			*v = types.ListNull(types.StringType)
		}
	}
	for _, v := range []*types.Int64{&m.PersistentKeepalive, &m.MTU} {
		if v.IsUnknown() {
			*v = types.Int64Null()
		}
	}
	if m.ClientIP.IsUnknown() {
		m.ClientIP = types.StringNull()
	}
}

// wireGuardServerPeerModel returns the model of peer. preshared_key is null while the preshared key is disabled,
// because the router can keep its value.
func wireGuardServerPeerModel(ctx context.Context, peer *glinet.WireGuardServerPeer, diags *diag.Diagnostics) *wireGuardServerPeerResourceModel {
	model := &wireGuardServerPeerResourceModel{
		ID:                  types.Int64Value(int64(peer.ID)),
		Name:                types.StringValue(peer.Name),
		PresharedKey:        types.StringNull(),
		AllowedIPs:          listOrNull(ctx, peer.AllowedIPs, diags),
		DNS:                 listOrNull(ctx, peer.DNS, diags),
		PersistentKeepalive: int64OrNull(peer.PersistentKeepalive),
		MTU:                 int64OrNull(peer.MTU),
		ClientIP:            stringOrNull(peer.ClientIP),
	}
	if peer.PresharedKeyEnable {
		model.PresharedKey = stringOrNull(peer.PresharedKey)
	}
	return model
}
