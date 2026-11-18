package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ resource.Resource                = &wireGuardClientConfigResource{}
	_ resource.ResourceWithConfigure   = &wireGuardClientConfigResource{}
	_ resource.ResourceWithImportState = &wireGuardClientConfigResource{}
)

func NewWireGuardClientConfigResource() resource.Resource {
	return &wireGuardClientConfigResource{}
}

type wireGuardClientConfigResource struct {
	client *glinet.Client
}

type wireGuardClientConfigResourceModel struct {
	ID                  types.Int64  `tfsdk:"id"`
	GroupID             types.Int64  `tfsdk:"group_id"`
	Name                types.String `tfsdk:"name"`
	AddressV4           types.String `tfsdk:"address_v4"`
	AddressV6           types.String `tfsdk:"address_v6"`
	PrivateKey          types.String `tfsdk:"private_key"`
	PublicKey           types.String `tfsdk:"public_key"`
	PresharedKey        types.String `tfsdk:"preshared_key"`
	Endpoint            types.String `tfsdk:"endpoint"`
	AllowedIPs          types.List   `tfsdk:"allowed_ips"`
	DNS                 types.List   `tfsdk:"dns"`
	ListenPort          types.Int64  `tfsdk:"listen_port"`
	PersistentKeepalive types.Int64  `tfsdk:"persistent_keepalive"`
	MTU                 types.Int64  `tfsdk:"mtu"`
}

func (r *wireGuardClientConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_client_config"
}

func (r *wireGuardClientConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a WireGuard client configuration in a group of the user (`wg-client.add_config` in the router API, a peer in the API). " +
			"Create, read, update, import and destroy fail when the group is not a group of the user (Manual in the web UI). " +
			"The resource does not start or stop the tunnel. " +
			"**Warning:** the web UI shows the configuration of the running tunnel as read only. " +
			"An update or destroy of the configuration that the tunnel uses can stop or restart the tunnel; this is not tested on a router. " +
			"Destroy removes the configuration from the router. " +
			"Import the configuration with `<group_id>/<id>`, for example `terraform import glinet_wireguard_client_config.example 1234/5678`. " +
			untestedWriteNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "ID of the configuration, which the router gives (`peer_id` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"group_id": schema.Int64Attribute{
				Description: "ID of the group, for example `glinet_wireguard_client_group.example.id` (`group_id` in the router API). Changing it replaces the configuration.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the configuration (`name` in the router API): 1 to 64 characters, not only spaces, without a `.` at the start and without `~`.",
				Required:    true,
				Validators:  []validator.String{wireGuardNameValidator},
			},
			"address_v4": schema.StringAttribute{
				Description: "IPv4 address of the router in the tunnel, with or without a prefix length, for example `10.0.0.2/32` (`address_v4` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 address with or without a prefix length, such as 10.0.0.2/32", valid: func(s string) bool { return validIPOrPrefix(s, true) }},
				},
			},
			"address_v6": schema.StringAttribute{
				Description: "IPv6 address of the router in the tunnel, with or without a prefix length, for example `fd00::2/128` (`address_v6` in the router API). " +
					"The provider sends `ipv6_enable` `true` when it is set and `false` when it is not set.",
				Optional: true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv6 address with or without a prefix length, such as fd00::2/128", valid: func(s string) bool { return validIPOrPrefix(s, false) }},
				},
			},
			"private_key": schema.StringAttribute{
				Description: "Private key of the router in the tunnel, 44 characters of base64 (`private_key` in the router API). " +
					"The router returns it in each read, so it is in the Terraform state.",
				Required:   true,
				Sensitive:  true,
				Validators: []validator.String{wireGuardSecretKeyValidator},
			},
			"public_key": schema.StringAttribute{
				Description: "Public key of the WireGuard server, 44 characters of base64 (`public_key` in the router API).",
				Required:    true,
				Validators:  []validator.String{wireGuardKeyValidator},
			},
			"preshared_key": schema.StringAttribute{
				Description: "Preshared key, 44 characters of base64 (`preshared_key` in the router API). " +
					"The provider sends `presharedkey_enable` `true` when it is set and `false` when it is not set.",
				Optional:   true,
				Sensitive:  true,
				Validators: []validator.String{wireGuardSecretKeyValidator},
			},
			"endpoint": schema.StringAttribute{
				Description: "Host and port of the WireGuard server, for example `vpn.example.com:51820` or `[2001:db8::1]:51820` (`end_point` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringCheck{description: "must be host:port with a host name, an IPv4 address or an IPv6 address in brackets, and a port from 1 to 65535", valid: validEndpoint},
				},
			},
			"allowed_ips": schema.ListAttribute{
				Description: "IP networks that go through the tunnel, for example `[\"0.0.0.0/0\", \"::/0\"]` (`allowed_ips` in the router API, one string with `, ` between the items).",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.ValueStringsAre(stringCheck{description: "must be an IP address with a prefix length, such as 0.0.0.0/0", valid: validPrefix}),
				},
			},
			"dns": schema.ListAttribute{
				Description: "IP addresses of the DNS servers in the tunnel (`dns` in the router API, one string with `, ` between the items). " + wireGuardRouterValueNote,
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
			"listen_port": schema.Int64Attribute{
				Description: "UDP port of the router in the tunnel, from 1 to 65535 (`listen_port` in the router API). " + wireGuardRouterValueNote,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{int64validator.Between(1, 65535)},
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
		},
	}
}

func (r *wireGuardClientConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wireGuardClientConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wireGuardClientConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	groupID := int(plan.GroupID.ValueInt64())
	if !r.checkGroup(ctx, groupID, &resp.Diagnostics) {
		return
	}

	peer := plan.peer(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.client.WireGuardClient.AddPeer(ctx, groupID, peer)
	if err != nil {
		addCreateError(&resp.Diagnostics, "Unable to Create WireGuard Client Configuration", "configuration", err)
		return
	}

	plan.ID = types.Int64Value(int64(id))
	plan.nullUnknown()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, groupID, id, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardClientConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wireGuardClientConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peer := r.find(ctx, int(state.GroupID.ValueInt64()), int(state.ID.ValueInt64()), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if peer == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wireGuardClientConfigModel(ctx, int(state.GroupID.ValueInt64()), peer, &resp.Diagnostics))...)
}

func (r *wireGuardClientConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan wireGuardClientConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	groupID := int(plan.GroupID.ValueInt64())
	if !r.checkGroup(ctx, groupID, &resp.Diagnostics) {
		return
	}

	peer := plan.peer(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.WireGuardClient.SetPeer(ctx, groupID, peer); err != nil {
		resp.Diagnostics.AddError("Unable to Update WireGuard Client Configuration", err.Error())
		return
	}

	plan.nullUnknown()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, groupID, peer.ID, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardClientConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wireGuardClientConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	groupID, id := int(state.GroupID.ValueInt64()), int(state.ID.ValueInt64())
	peer := r.find(ctx, groupID, id, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if peer == nil {
		return
	}

	if err := r.client.WireGuardClient.RemovePeer(ctx, groupID, id); err != nil {
		resp.Diagnostics.AddError("Unable to Delete WireGuard Client Configuration", err.Error())
	}
}

func (r *wireGuardClientConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	groupPart, idPart, _ := strings.Cut(req.ID, "/")
	groupID, groupOK := parseRouterID(groupPart)
	id, idOK := parseRouterID(idPart)
	if !groupOK || !idOK {
		invalidImportID(resp, "glinet_wireguard_client_config", "<group_id>/<id>", "1234/5678", req.ID)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), int64(groupID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), int64(id))...)
}

// checkGroup adds an error and returns false when the group groupID is not a group of the user.
func (r *wireGuardClientConfigResource) checkGroup(ctx context.Context, groupID int, diags *diag.Diagnostics) bool {
	group, err := findWireGuardGroup(ctx, r.client, groupID)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Client Groups", err.Error())
		return false
	}
	if group == nil {
		diags.AddAttributeError(path.Root("group_id"), "WireGuard Client Group Not Found",
			fmt.Sprintf("The router has no WireGuard client group with the ID %d.", groupID))
		return false
	}
	if group.Type != glinet.WireGuardGroupCustom {
		diags.AddAttributeError(path.Root("group_id"), "Not a Custom WireGuard Client Group", notCustomGroup(group))
		return false
	}
	return true
}

// find returns the configuration id of the group groupID, or nil when the router has no such group or configuration.
// It reads the groups first, because the router API has no documented result for the configurations of a group that does not exist.
// It adds an error when the group is not a group of the user, so that the resource does not import or remove a configuration
// of a preset group.
func (r *wireGuardClientConfigResource) find(ctx context.Context, groupID, id int, diags *diag.Diagnostics) *glinet.WireGuardPeer {
	group, err := findWireGuardGroup(ctx, r.client, groupID)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Client Groups", err.Error())
		return nil
	}
	if group == nil {
		return nil
	}
	if group.Type != glinet.WireGuardGroupCustom {
		diags.AddAttributeError(path.Root("group_id"), "Not a Custom WireGuard Client Group", notCustomGroup(group))
		return nil
	}

	peer, err := r.client.WireGuardClient.FindPeer(ctx, groupID, id)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Client Configuration", err.Error())
	}
	return peer
}

func (r *wireGuardClientConfigResource) readBack(ctx context.Context, groupID, id int, state *tfsdk.State, diags *diag.Diagnostics) {
	peer, err := r.client.WireGuardClient.FindPeer(ctx, groupID, id)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Client Configuration", err.Error())
		return
	}
	if peer == nil {
		diags.AddError("WireGuard Client Configuration Not Found",
			fmt.Sprintf("The router has no WireGuard client configuration with the ID %d in the group %d after the write.", id, groupID))
		return
	}

	diags.Append(state.Set(ctx, wireGuardClientConfigModel(ctx, groupID, peer, diags))...)
}

func (m wireGuardClientConfigResourceModel) peer(ctx context.Context, diags *diag.Diagnostics) glinet.WireGuardPeer {
	return glinet.WireGuardPeer{
		ID:                  int(m.ID.ValueInt64()),
		Name:                m.Name.ValueString(),
		AddressV4:           m.AddressV4.ValueString(),
		AddressV6:           m.AddressV6.ValueString(),
		IPv6Enable:          !m.AddressV6.IsNull(),
		PrivateKey:          m.PrivateKey.ValueString(),
		PublicKey:           m.PublicKey.ValueString(),
		PresharedKeyEnable:  !m.PresharedKey.IsNull(),
		PresharedKey:        m.PresharedKey.ValueString(),
		AllowedIPs:          joinList(ctx, m.AllowedIPs, diags),
		Endpoint:            m.Endpoint.ValueString(),
		DNS:                 joinList(ctx, m.DNS, diags),
		ListenPort:          int(m.ListenPort.ValueInt64()),
		PersistentKeepalive: int(m.PersistentKeepalive.ValueInt64()),
		MTU:                 int(m.MTU.ValueInt64()),
	}
}

// nullUnknown sets the attributes that the router gives to null when they are unknown, so that the state
// that is saved before the read-back has no unknown value.
func (m *wireGuardClientConfigResourceModel) nullUnknown() {
	if m.DNS.IsUnknown() {
		m.DNS = types.ListNull(types.StringType)
	}
	for _, v := range []*types.Int64{&m.ListenPort, &m.PersistentKeepalive, &m.MTU} {
		if v.IsUnknown() {
			*v = types.Int64Null()
		}
	}
}

// wireGuardClientConfigModel returns the model of peer. address_v6 is null while IPv6 is disabled and preshared_key
// is null while the preshared key is disabled, because the router can keep their values.
func wireGuardClientConfigModel(ctx context.Context, groupID int, peer *glinet.WireGuardPeer, diags *diag.Diagnostics) *wireGuardClientConfigResourceModel {
	model := &wireGuardClientConfigResourceModel{
		ID:                  types.Int64Value(int64(peer.ID)),
		GroupID:             types.Int64Value(int64(groupID)),
		Name:                types.StringValue(peer.Name),
		AddressV4:           types.StringValue(peer.AddressV4),
		AddressV6:           types.StringNull(),
		PrivateKey:          types.StringValue(peer.PrivateKey),
		PublicKey:           types.StringValue(peer.PublicKey),
		PresharedKey:        types.StringNull(),
		Endpoint:            types.StringValue(peer.Endpoint),
		AllowedIPs:          listOrNull(ctx, peer.AllowedIPs, diags),
		DNS:                 listOrNull(ctx, peer.DNS, diags),
		ListenPort:          int64OrNull(peer.ListenPort),
		PersistentKeepalive: int64OrNull(peer.PersistentKeepalive),
		MTU:                 int64OrNull(peer.MTU),
	}
	if peer.IPv6Enable {
		model.AddressV6 = stringOrNull(peer.AddressV6)
	}
	if peer.PresharedKeyEnable {
		model.PresharedKey = stringOrNull(peer.PresharedKey)
	}
	return model
}
