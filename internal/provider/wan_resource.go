package provider

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ryanrishi/glinet-client-go"
)

const wanID = "wan"

var (
	_ resource.Resource                     = &wanResource{}
	_ resource.ResourceWithConfigure        = &wanResource{}
	_ resource.ResourceWithImportState      = &wanResource{}
	_ resource.ResourceWithConfigValidators = &wanResource{}
)

func NewWANResource() resource.Resource {
	return &wanResource{}
}

type wanResource struct {
	client *glinet.Client
}

type wanResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Protocol types.String `tfsdk:"protocol"`
	IPv4     types.Object `tfsdk:"ipv4"`
	IPv6     types.Object `tfsdk:"ipv6"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	VLANID   types.Int64  `tfsdk:"vlan_id"`
	TTL      types.Int64  `tfsdk:"ttl"`
	TTLIPv6  types.Int64  `tfsdk:"ttl_ipv6"`
	MTU      types.Int64  `tfsdk:"mtu"`
}

type wanIPv4Model struct {
	IP      types.String `tfsdk:"ip"`
	Netmask types.String `tfsdk:"netmask"`
	Gateway types.String `tfsdk:"gateway"`
	DNS     types.List   `tfsdk:"dns"`
}

type wanIPv6Model struct {
	IP      types.String `tfsdk:"ip"`
	Gateway types.String `tfsdk:"gateway"`
	DNS     types.List   `tfsdk:"dns"`
}

var (
	wanIPv4Types = map[string]attr.Type{
		"ip":      types.StringType,
		"netmask": types.StringType,
		"gateway": types.StringType,
		"dns":     types.ListType{ElemType: types.StringType},
	}
	wanIPv6Types = map[string]attr.Type{
		"ip":      types.StringType,
		"gateway": types.StringType,
		"dns":     types.ListType{ElemType: types.StringType},
	}
)

func validIPv6Prefix(s string) bool {
	prefix, err := netip.ParsePrefix(s)
	return err == nil && prefix.Addr().Is6() && !prefix.Addr().Is4In6()
}

func (r *wanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wan"
}

func (r *wanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ipv4Validator := stringCheck{description: "must be an IPv4 address", valid: validIPv4}
	ipv6Validator := stringCheck{description: "must be an IPv6 address", valid: validIPv6}

	resp.Schema = schema.Schema{
		Description: "Manages the wired WAN port of the router (`cable.set_config` in the router API). " +
			"The router has one wired WAN port, so use one instance of this resource. " +
			"Each write sends the full configuration of the protocol, as the web UI does: an attribute that is not set is removed on the router. " +
			"A wrong configuration cuts the wired uplink of the router. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + wanID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"protocol": schema.StringAttribute{
				Description: "How the WAN port gets its address (`protocol` in the router API): `dhcp`, `static` or `pppoe`.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("dhcp", "static", "pppoe"),
				},
			},
			"ipv4": schema.SingleNestedAttribute{
				Description: "Static IPv4 configuration (`ipv4` in the router API). Required when `protocol` is `static`, and can be set only then.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"ip": schema.StringAttribute{
						Description: "IPv4 address of the WAN port (`ip` in the router API).",
						Required:    true,
						Validators:  []validator.String{ipv4Validator},
					},
					"netmask": schema.StringAttribute{
						Description: "Netmask, for example `255.255.255.0` (`netmask` in the router API).",
						Required:    true,
						Validators: []validator.String{
							stringCheck{description: "must be an IPv4 netmask such as 255.255.255.0", valid: validNetmask},
						},
					},
					"gateway": schema.StringAttribute{
						Description: "IPv4 address of the gateway (`gateway` in the router API).",
						Required:    true,
						Validators:  []validator.String{ipv4Validator},
					},
					"dns": schema.ListAttribute{
						Description: "One or two IPv4 addresses of DNS servers (`dns` in the router API).",
						Required:    true,
						ElementType: types.StringType,
						Validators: []validator.List{
							listvalidator.SizeBetween(1, 2),
							listvalidator.ValueStringsAre(ipv4Validator),
						},
					},
				},
			},
			"ipv6": schema.SingleNestedAttribute{
				Description: "Static IPv6 configuration (`ipv6` in the router API). Can be set only when `protocol` is `static`. " +
					"The web UI sends it only while IPv6 is enabled on the router (see `glinet_ipv6`).",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"ip": schema.StringAttribute{
						Description: "IPv6 address of the WAN port with a prefix length, for example `2001:db8::2/64` (`ip` in the router API).",
						Required:    true,
						Validators: []validator.String{
							stringCheck{description: "must be an IPv6 address with a prefix length, such as 2001:db8::2/64", valid: validIPv6Prefix},
						},
					},
					"gateway": schema.StringAttribute{
						Description: "IPv6 address of the gateway (`gateway` in the router API).",
						Required:    true,
						Validators:  []validator.String{ipv6Validator},
					},
					"dns": schema.ListAttribute{
						Description: "One or two IPv6 addresses of DNS servers (`dns` in the router API).",
						Required:    true,
						ElementType: types.StringType,
						Validators: []validator.List{
							listvalidator.SizeBetween(1, 2),
							listvalidator.ValueStringsAre(ipv6Validator),
						},
					},
				},
			},
			"username": schema.StringAttribute{
				Description: "PPPoE user name, at most 256 characters (`username` in the router API). Can be set only when `protocol` is `pppoe`. " +
					"When it is not set, the provider sends an empty user name, as the web UI does.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 256),
				},
			},
			"password": schema.StringAttribute{
				Description: "PPPoE password (`password` in the router API). Can be set only when `protocol` is `pppoe`. " +
					"When it is not set, the provider sends an empty password, as the web UI does.",
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"vlan_id": schema.Int64Attribute{
				Description: "VLAN ID of the WAN port, 1 to 4094 (`vlanid` in the router API). When it is not set, the provider does not send it.",
				Optional:    true,
				Validators:  []validator.Int64{int64validator.Between(1, 4094)},
			},
			"ttl": schema.Int64Attribute{
				Description: "IPv4 TTL that the router sets on the packets that it sends to the WAN, 1 to 255 (`ttl` in the router API). " +
					"When it is not set, the provider does not send it.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(1, 255)},
			},
			"ttl_ipv6": schema.Int64Attribute{
				Description: "IPv6 hop limit that the router sets on the packets that it sends to the WAN, 1 to 255 (`ttl_ipv6` in the router API). " +
					"When it is not set, the provider does not send it.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(1, 255)},
			},
			"mtu": schema.Int64Attribute{
				Description: "MTU of the WAN port, 576 to 9200 (`mtu` in the router API). When it is not set, the provider does not send it. " +
					"The web UI warns that IPv6 needs at least 1280.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(576, 9200)},
			},
		},
	}
}

func (r *wanResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "ipv4 is required for protocol static, ipv4 and ipv6 are allowed only for static, and username and password only for pppoe",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config wanResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.Protocol.IsNull() || config.Protocol.IsUnknown() {
				return
			}

			protocol := config.Protocol.ValueString()
			if protocol == "static" {
				require(diags, "ipv4", config.IPv4, "when protocol is static")
			} else {
				forbid(diags, "ipv4", config.IPv4, "when protocol is "+protocol)
				forbid(diags, "ipv6", config.IPv6, "when protocol is "+protocol)
			}
			if protocol != "pppoe" {
				forbid(diags, "username", config.Username, "when protocol is "+protocol)
				forbid(diags, "password", config.Password, "when protocol is "+protocol)
			}
		},
	}}
}

func (r *wanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *wanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *wanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != wanID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_wan must be %q, got %q.", wanID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), wanID)...)
}

func (r *wanResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model wanResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	config := glinet.CableConfig{
		Protocol: model.Protocol.ValueString(),
		Username: model.Username.ValueString(),
		Password: model.Password.ValueString(),
		VLANID:   int(model.VLANID.ValueInt64()),
		TTL:      int(model.TTL.ValueInt64()),
		TTLIPv6:  int(model.TTLIPv6.ValueInt64()),
		MTU:      int(model.MTU.ValueInt64()),
	}
	if !model.IPv4.IsNull() {
		var ipv4 wanIPv4Model
		diags.Append(model.IPv4.As(ctx, &ipv4, basetypes.ObjectAsOptions{})...)
		config.IPv4 = &glinet.CableIPv4{
			IP:      ipv4.IP.ValueString(),
			Netmask: ipv4.Netmask.ValueString(),
			Gateway: ipv4.Gateway.ValueString(),
			DNS:     listStrings(ctx, ipv4.DNS, diags),
		}
	}
	if !model.IPv6.IsNull() {
		var ipv6 wanIPv6Model
		diags.Append(model.IPv6.As(ctx, &ipv6, basetypes.ObjectAsOptions{})...)
		config.IPv6 = &glinet.CableIPv6{
			IP:      ipv6.IP.ValueString(),
			Gateway: ipv6.Gateway.ValueString(),
			DNS:     listStrings(ctx, ipv6.DNS, diags),
		}
	}
	if diags.HasError() {
		return
	}

	if err := r.client.Cable.SetConfig(ctx, glinet.FirstWANPort, config); err != nil {
		diags.AddError("Unable to Set WAN Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(wanID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

// read takes the addresses and the PPPoE account only for the protocol that uses them, because the
// router can keep the values of another protocol.
func (r *wanResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.Cable.GetConfig(ctx, glinet.FirstWANPort)
	if err != nil {
		diags.AddError("Unable to Read WAN Configuration", err.Error())
		return
	}

	model := wanResourceModel{
		ID:       types.StringValue(wanID),
		Protocol: types.StringValue(config.Protocol),
		IPv4:     types.ObjectNull(wanIPv4Types),
		IPv6:     types.ObjectNull(wanIPv6Types),
		Username: types.StringNull(),
		Password: types.StringNull(),
		VLANID:   int64OrNull(config.VLANID),
		TTL:      int64OrNull(config.TTL),
		TTLIPv6:  int64OrNull(config.TTLIPv6),
		MTU:      int64OrNull(config.MTU),
	}
	switch config.Protocol {
	case "static":
		if config.IPv4 != nil {
			ipv4, d := types.ObjectValueFrom(ctx, wanIPv4Types, wanIPv4Model{
				IP:      types.StringValue(config.IPv4.IP),
				Netmask: types.StringValue(config.IPv4.Netmask),
				Gateway: types.StringValue(config.IPv4.Gateway),
				DNS:     stringList(ctx, config.IPv4.DNS, diags),
			})
			diags.Append(d...)
			model.IPv4 = ipv4
		}
		if config.IPv6 != nil {
			ipv6, d := types.ObjectValueFrom(ctx, wanIPv6Types, wanIPv6Model{
				IP:      types.StringValue(config.IPv6.IP),
				Gateway: types.StringValue(config.IPv6.Gateway),
				DNS:     stringList(ctx, config.IPv6.DNS, diags),
			})
			diags.Append(d...)
			model.IPv6 = ipv6
		}
	case "pppoe":
		model.Username = stringOrNull(config.Username)
		model.Password = stringOrNull(config.Password)
	}
	if diags.HasError() {
		return
	}

	diags.Append(state.Set(ctx, &model)...)
}
