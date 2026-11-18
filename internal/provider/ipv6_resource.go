package provider

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const ipv6ID = "ipv6"

var (
	_ resource.Resource                     = &ipv6Resource{}
	_ resource.ResourceWithConfigure        = &ipv6Resource{}
	_ resource.ResourceWithImportState      = &ipv6Resource{}
	_ resource.ResourceWithConfigValidators = &ipv6Resource{}
)

func NewIPv6Resource() resource.Resource {
	return &ipv6Resource{}
}

type ipv6Resource struct {
	client *glinet.Client
}

type ipv6ResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	LANMode    types.String `tfsdk:"lan_mode"`
	LANDNSAuto types.Bool   `tfsdk:"lan_dns_auto"`
	LANIP      types.String `tfsdk:"lan_ip"`
	LANDNS1    types.String `tfsdk:"lan_dns1"`
	LANDNS2    types.String `tfsdk:"lan_dns2"`
}

// validLANIPv6 accepts an IPv6 address with a prefix length from 48 to 64, as the web UI does.
func validLANIPv6(s string) bool {
	prefix, err := netip.ParsePrefix(s)
	return err == nil && prefix.Addr().Is6() && !prefix.Addr().Is4In6() && prefix.Bits() >= 48 && prefix.Bits() <= 64
}

func (r *ipv6Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipv6"
}

func (r *ipv6Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ipv6Validator := stringCheck{description: "must be an IPv6 address", valid: validIPv6}

	resp.Schema = schema.Schema{
		Description: "Manages IPv6 on the router and on its LAN (`ipv6.set_ipv6` in the router API). " +
			"The router has one IPv6 configuration, so use one instance of this resource. " +
			"The web UI warns that some firewall, VPN and cloud features of the router have limits with IPv6. " +
			"When `enabled` is `false`, the provider sends only `enable: false`, as the web UI does: " +
			"the other attributes are kept in the Terraform state and get to the router only when IPv6 is enabled. " +
			"While the router reports IPv6 as disabled, the provider does not compare the other attributes with the router. " +
			"The static IPv6 address of the WAN port is in `glinet_wan`. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + ipv6ID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether IPv6 is enabled (`enable` in the router API).",
				Required:    true,
			},
			"lan_mode": schema.StringAttribute{
				Description: "How the LAN gets IPv6 (`lan_mode` in the router API): `native`, `relay` (Passthrough in the web UI), `nat6` or `static`. " +
					"Required when `enabled` is `true`.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.OneOf("native", "relay", "nat6", "static"),
				},
			},
			"lan_dns_auto": schema.BoolAttribute{
				Description: "Whether the LAN clients get the IPv6 DNS servers automatically (`lan_dns_mode` in the router API). " +
					"When it is `false`, the router gives `lan_dns1` and `lan_dns2`. Must be `true` when `lan_mode` is `relay`. Defaults to `true`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"lan_ip": schema.StringAttribute{
				Description: "IPv6 address of the router in the LAN with a prefix length from 48 to 64, for example `fd00:1::1/64` (`lan_ip` in the router API). " +
					"Required when `lan_mode` is `static`, and can be set only then.",
				Optional: true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv6 address with a prefix length from 48 to 64, such as fd00:1::1/64", valid: validLANIPv6},
				},
			},
			"lan_dns1": schema.StringAttribute{
				Description: "IPv6 address of the first DNS server for the LAN clients (`lan_dns1` in the router API). " +
					"Required when `lan_dns_auto` is `false`, and can be set only then.",
				Optional:   true,
				Validators: []validator.String{ipv6Validator},
			},
			"lan_dns2": schema.StringAttribute{
				Description: "IPv6 address of the second DNS server for the LAN clients (`lan_dns2` in the router API). " +
					"Can be set only when `lan_dns_auto` is `false`. " +
					"When it is not set, the provider does not send it, as the web UI does. It is not known whether the router then removes " +
					"its second DNS server, so the value is computed: the state shows the second DNS server that the router reports.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{ipv6Validator},
			},
		},
	}
}

func (r *ipv6Resource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "lan_mode is required when enabled is true, lan_ip goes with lan_mode static, and lan_dns1 and lan_dns2 go with lan_dns_auto false",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config ipv6ResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if config.Enabled.ValueBool() {
				require(diags, "lan_mode", config.LANMode, "when enabled is true")
			}

			if !config.LANMode.IsUnknown() {
				if config.LANMode.ValueString() == "static" {
					require(diags, "lan_ip", config.LANIP, "when lan_mode is static")
				} else {
					forbid(diags, "lan_ip", config.LANIP, "when lan_mode is not static")
				}
			}

			if config.LANDNSAuto.IsUnknown() {
				return
			}
			if config.LANDNSAuto.IsNull() || config.LANDNSAuto.ValueBool() {
				forbid(diags, "lan_dns1", config.LANDNS1, "when lan_dns_auto is true")
				forbid(diags, "lan_dns2", config.LANDNS2, "when lan_dns_auto is true")
				return
			}
			require(diags, "lan_dns1", config.LANDNS1, "when lan_dns_auto is false")
			if config.LANMode.ValueString() == "relay" {
				diags.AddAttributeError(path.Root("lan_dns_auto"), invalidCombination,
					"lan_dns_auto must be true when lan_mode is relay: the web UI sets the DNS servers automatically in this mode.")
			}
		},
	}}
}

func (r *ipv6Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *ipv6Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ipv6Resource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *ipv6Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ipv6Resource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *ipv6Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != ipv6ID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_ipv6 must be %q, got %q.", ipv6ID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ipv6ID)...)
}

func (r *ipv6Resource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model ipv6ResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	err := r.client.IPv6.SetConfig(ctx, glinet.IPv6Config{
		Enable:     model.Enabled.ValueBool(),
		LANMode:    model.LANMode.ValueString(),
		LANDNSMode: model.LANDNSAuto.ValueBool(),
		LANIP:      model.LANIP.ValueString(),
		LANDNS1:    model.LANDNS1.ValueString(),
		LANDNS2:    model.LANDNS2.ValueString(),
	})
	if err != nil {
		diags.AddError("Unable to Set IPv6 Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(ipv6ID)
	if model.LANDNS2.IsUnknown() {
		model.LANDNS2 = types.StringNull()
	}
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

// read keeps the attributes of the state while the router reports IPv6 as disabled, because a disabled write
// sends only enable. After an import, the state has no values, so the read takes those of the router.
// It takes lan_ip only for lan_mode static and the DNS servers only when they are not automatic, because
// the router can keep the values of another mode.
func (r *ipv6Resource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	var prior ipv6ResourceModel
	diags.Append(state.Get(ctx, &prior)...)
	if diags.HasError() {
		return
	}

	config, err := r.client.IPv6.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read IPv6 Configuration", err.Error())
		return
	}

	if !config.Enable && !prior.Enabled.IsNull() {
		prior.Enabled = types.BoolValue(false)
		diags.Append(state.Set(ctx, &prior)...)
		return
	}

	model := ipv6ResourceModel{
		ID:         types.StringValue(ipv6ID),
		Enabled:    types.BoolValue(config.Enable),
		LANMode:    stringOrNull(config.LANMode),
		LANDNSAuto: types.BoolValue(config.LANDNSMode),
		LANIP:      types.StringNull(),
		LANDNS1:    types.StringNull(),
		LANDNS2:    types.StringNull(),
	}
	if config.LANMode == "static" {
		model.LANIP = stringOrNull(config.LANIP)
	}
	if !config.LANDNSMode {
		model.LANDNS1 = stringOrNull(config.LANDNS1)
		model.LANDNS2 = stringOrNull(config.LANDNS2)
	}

	diags.Append(state.Set(ctx, &model)...)
}
