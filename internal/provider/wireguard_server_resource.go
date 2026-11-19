package provider

import (
	"context"
	"fmt"
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

const wireGuardServerID = "wireguard_server"

var (
	_ resource.Resource                = &wireGuardServerResource{}
	_ resource.ResourceWithConfigure   = &wireGuardServerResource{}
	_ resource.ResourceWithImportState = &wireGuardServerResource{}
)

func NewWireGuardServerResource() resource.Resource {
	return &wireGuardServerResource{}
}

type wireGuardServerResource struct {
	client *glinet.Client
}

type wireGuardServerResourceModel struct {
	ID             types.String `tfsdk:"id"`
	AddressV4      types.String `tfsdk:"address_v4"`
	AddressV6      types.String `tfsdk:"address_v6"`
	ListenPort     types.Int64  `tfsdk:"listen_port"`
	PrivateKey     types.String `tfsdk:"private_key"`
	PublicKey      types.String `tfsdk:"public_key"`
	ClientToClient types.Bool   `tfsdk:"client_to_client"`
	LocalAccess    types.Bool   `tfsdk:"local_access"`
	Masquerade     types.Bool   `tfsdk:"masquerade"`
	MTU            types.Int64  `tfsdk:"mtu"`
}

// validWireGuardServerAddressV4 is the rule of the web UI: a private IPv4 address with a prefix length up to 30.
func validWireGuardServerAddressV4(s string) bool {
	prefix, err := netip.ParsePrefix(s)
	return err == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && prefix.Bits() <= 30
}

// validWireGuardServerAddressV6 is the rule of the web UI of firmware 4.8.3: empty, or an IPv6 address in fd00::/8 with the prefix length 64.
func validWireGuardServerAddressV6(s string) bool {
	if s == "" {
		return true
	}
	prefix, err := netip.ParsePrefix(s)
	return err == nil && prefix.Addr().Is6() && prefix.Addr().As16()[0] == 0xfd && prefix.Bits() == 64
}

func (r *wireGuardServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_server"
}

func (r *wireGuardServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the configuration and the options of the WireGuard server of the router (`wg-server.set_config` and `wg-server.set_setting` in the router API). " +
			"The router has one WireGuard server, so use one instance of this resource. The server always exists on the router, so create takes over the server. " +
			"When the server is not initialized, create initializes it (`wg-server.generate_key`), as the \"initialize\" button of the web UI does: the router makes the keys of the server. " +
			"The initialization cannot be undone. While the router reports the server as not initialized, the resource does not exist. " +
			"Each attribute that is not set keeps the value of the router: the provider sends back the value that it reads from the router just before the write. " +
			"The provider sends the configuration call only when `address_v4`, `address_v6`, `listen_port` or `private_key` differs, and the options call only when an option differs: " +
			"on create from the values of the router, on update from the state. " +
			"The web UI permits a change of the configuration only while the server is stopped, so a configuration write fails while the server runs or starts; stop the server in the web UI first. " +
			"The resource does not start or stop the server. Use `glinet_wireguard_server_peer` for the peers and `glinet_wireguard_server_route` for the route rules. " +
			"With a configuration that has an IPv6 address, the provider sends `ipv6_enable` with the value of the last read, as the client does. " +
			"Firmware 4.8.3 does not give `ipv6_enable`, so the provider then sends `false`; the web UI of firmware 4.8.3 does not send it, and what the router does with it is not known. " +
			"The state keeps the private key as clear text; protect the state. " +
			"Import the server with the ID `" + wireGuardServerID + "`. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + wireGuardServerID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"address_v4": schema.StringAttribute{
				Description: "IPv4 address of the server in the tunnel with a prefix length, for example `10.0.0.1/24` (`address_v4` in the router API). " +
					"It must be a private address, and the prefix length must be 30 or less. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringCheck{description: "must be a private IPv4 address with a prefix length up to 30, such as 10.0.0.1/24", valid: validWireGuardServerAddressV4},
				},
			},
			"address_v6": schema.StringAttribute{
				Description: "IPv6 address of the server in the tunnel with the prefix length 64, in `fd00::/8`, for example `fd00:db8::1/64` (`address_v6` in the router API). " +
					"`\"\"` removes the address, as the web UI sends it for an empty input; what the router does with `\"\"` is not known. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringCheck{description: "must be \"\" or an IPv6 address in fd00::/8 with the prefix length 64, such as fd00:db8::1/64", valid: validWireGuardServerAddressV6},
				},
			},
			"listen_port": schema.Int64Attribute{
				Description: "UDP port of the server, from 1 to 65535 (`port` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				Validators:  []validator.Int64{int64validator.Between(1, 65535)},
			},
			"private_key": schema.StringAttribute{
				Description: "Private key of the server, 44 characters of base64 (`private_key` in the router API). " +
					"When it is not set, the router value does not change. The provider never sends an empty key, because it is not known what the router does with it.",
				Optional:   true,
				Computed:   true,
				Sensitive:  true,
				Validators: []validator.String{wireGuardSecretKeyValidator},
			},
			"public_key": schema.StringAttribute{
				Description: "Public key of the server, which the router gives from the private key (`public_key` in the router API).",
				Computed:    true,
			},
			"client_to_client": schema.BoolAttribute{
				Description: "Whether the peers can reach each other (`client_to_client` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
			"local_access": schema.BoolAttribute{
				Description: "Whether the peers can reach the LAN of the router (`local_access` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
			"masquerade": schema.BoolAttribute{
				Description: "Whether the router translates the source address of the traffic of the peers (NAT) (`masq` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
			"mtu": schema.Int64Attribute{
				Description: "MTU of the tunnel, from 68 to 65535, or `0` for no MTU value (`mtu` in the router API). " +
					"The web UI sends `0` for an empty input; what the router does with `0` is not known. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.Int64{
					int64validator.Any(int64validator.OneOf(0), int64validator.Between(68, 65535)),
				},
			},
		},
	}
}

func (r *wireGuardServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wireGuardServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wireGuardServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, nil, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	_, model, err := r.read(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Server", err.Error())
		return
	}
	if model == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *wireGuardServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wireGuardServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, &state, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardServerResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *wireGuardServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != wireGuardServerID {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_wireguard_server must be %q, got %q.", wireGuardServerID, req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), wireGuardServerID)...)
}

// write initializes the server when it is not initialized, then sends the configuration and the options of plan
// only when they differ from prior, or on create (prior is nil) from the router values. An unknown attribute, which
// is not in the configuration, gets the router value.
func (r *wireGuardServerResource) write(ctx context.Context, plan wireGuardServerResourceModel, prior *wireGuardServerResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, current, err := r.read(ctx)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Server", err.Error())
		return
	}
	if current == nil {
		if err := r.client.WireGuardServer.Initialize(ctx); err != nil {
			diags.AddError("Unable to Initialize WireGuard Server", err.Error())
			return
		}
		config, current, err = r.read(ctx)
		if err != nil {
			diags.AddError("Unable to Read WireGuard Server", err.Error())
			return
		}
		if current == nil {
			diags.AddError("WireGuard Server Not Initialized", "The router reports the WireGuard server as not initialized after the initialization.")
			return
		}
	}

	for _, v := range []struct{ plan, current *types.String }{
		{&plan.AddressV4, &current.AddressV4}, {&plan.AddressV6, &current.AddressV6}, {&plan.PrivateKey, &current.PrivateKey},
	} {
		if v.plan.IsUnknown() {
			*v.plan = *v.current
		}
	}
	for _, v := range []struct{ plan, current *types.Int64 }{{&plan.ListenPort, &current.ListenPort}, {&plan.MTU, &current.MTU}} {
		if v.plan.IsUnknown() {
			*v.plan = *v.current
		}
	}
	for _, v := range []struct{ plan, current *types.Bool }{
		{&plan.ClientToClient, &current.ClientToClient}, {&plan.LocalAccess, &current.LocalAccess}, {&plan.Masquerade, &current.Masquerade},
	} {
		if v.plan.IsUnknown() {
			*v.plan = *v.current
		}
	}

	compared := prior
	if compared == nil {
		compared = current
	}
	configChanged := !plan.AddressV4.Equal(compared.AddressV4) || !plan.AddressV6.Equal(compared.AddressV6) ||
		!plan.ListenPort.Equal(compared.ListenPort) || !plan.PrivateKey.Equal(compared.PrivateKey)
	settingChanged := !plan.ClientToClient.Equal(compared.ClientToClient) || !plan.LocalAccess.Equal(compared.LocalAccess) ||
		!plan.Masquerade.Equal(compared.Masquerade) || !plan.MTU.Equal(compared.MTU)

	if configChanged {
		status, err := r.client.WireGuardServer.GetStatus(ctx)
		if err != nil {
			diags.AddError("Unable to Read WireGuard Server Status", err.Error())
			return
		}
		if status.Server.Status != 0 {
			diags.AddError("WireGuard Server Runs",
				"The web UI permits a change of the WireGuard server configuration only while the server is stopped, "+
					"and the router reports the server as running or starting. Stop the server in the web UI, run terraform apply again, then start the server.")
			return
		}
		if err := r.client.WireGuardServer.SetConfig(ctx, glinet.WireGuardServerConfig{
			AddressV4:  plan.AddressV4.ValueString(),
			AddressV6:  plan.AddressV6.ValueString(),
			IPv6Enable: config.IPv6Enable,
			Port:       int(plan.ListenPort.ValueInt64()),
			PrivateKey: plan.PrivateKey.ValueString(),
		}); err != nil {
			diags.AddError("Unable to Set WireGuard Server Configuration", err.Error())
			return
		}
	}

	if settingChanged {
		if err := r.client.WireGuardServer.SetSetting(ctx, glinet.WireGuardServerSetting{
			ClientToClient: plan.ClientToClient.ValueBool(),
			LocalAccess:    plan.LocalAccess.ValueBool(),
			Masq:           plan.Masquerade.ValueBool(),
			MTU:            int(plan.MTU.ValueInt64()),
		}); err != nil {
			diags.AddError("Unable to Set WireGuard Server Options", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(wireGuardServerID)
	plan.PublicKey = current.PublicKey
	diags.Append(state.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}

	_, model, err := r.read(ctx)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Server", err.Error())
		return
	}
	if model == nil {
		diags.AddError("WireGuard Server Not Initialized", "The router reports the WireGuard server as not initialized after the write.")
		return
	}
	diags.Append(state.Set(ctx, model)...)
}

// read returns the configuration of the router and the model of the router values, or a nil model when the server is
// not initialized.
func (r *wireGuardServerResource) read(ctx context.Context) (*glinet.WireGuardServerConfig, *wireGuardServerResourceModel, error) {
	config, err := r.client.WireGuardServer.GetConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !config.Initialization {
		return config, nil, nil
	}
	setting, err := r.client.WireGuardServer.GetSetting(ctx)
	if err != nil {
		return nil, nil, err
	}

	return config, &wireGuardServerResourceModel{
		ID:             types.StringValue(wireGuardServerID),
		AddressV4:      types.StringValue(config.AddressV4),
		AddressV6:      types.StringValue(config.AddressV6),
		ListenPort:     types.Int64Value(int64(config.Port)),
		PrivateKey:     types.StringValue(config.PrivateKey),
		PublicKey:      types.StringValue(config.PublicKey),
		ClientToClient: types.BoolValue(setting.ClientToClient),
		LocalAccess:    types.BoolValue(setting.LocalAccess),
		Masquerade:     types.BoolValue(setting.Masq),
		MTU:            types.Int64Value(int64(setting.MTU)),
	}, nil
}
