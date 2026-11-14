package provider

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"strconv"

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

var (
	_ resource.Resource                     = &lanConfigResource{}
	_ resource.ResourceWithConfigure        = &lanConfigResource{}
	_ resource.ResourceWithImportState      = &lanConfigResource{}
	_ resource.ResourceWithConfigValidators = &lanConfigResource{}
)

// The lease time limits of the web UI, in minutes.
const (
	minLeaseMinutes = 2
	maxLeaseMinutes = 71582788
)

var (
	lanInterfaces = []string{"lan", "guest"}

	configLeaseTime = regexp.MustCompile(`^([0-9]+)([hm])$`)
	routerLeaseTime = regexp.MustCompile(`^([0-9]+)([hms]?)$`)
)

func NewLANConfigResource() resource.Resource {
	return &lanConfigResource{}
}

type lanConfigResource struct {
	client *glinet.Client
}

type lanConfigResourceModel struct {
	Interface   types.String `tfsdk:"interface"`
	IP          types.String `tfsdk:"ip"`
	Netmask     types.String `tfsdk:"netmask"`
	APIsolation types.Bool   `tfsdk:"ap_isolation"`
	DHCPEnabled types.Bool   `tfsdk:"dhcp_enabled"`
	DHCPStart   types.String `tfsdk:"dhcp_start"`
	DHCPEnd     types.String `tfsdk:"dhcp_end"`
	LeaseTime   types.String `tfsdk:"lease_time"`
	Gateway     types.String `tfsdk:"gateway"`
	DNS         types.List   `tfsdk:"dns"`
}

// leaseSeconds returns the duration of a lease time of the router, such as "12h", "720m", "43200s" or "43200".
func leaseSeconds(s string) (int64, bool) {
	m := routerLeaseTime.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	unit := int64(1)
	switch m[2] {
	case "h":
		unit = 3600
	case "m":
		unit = 60
	}
	if n > math.MaxInt64/unit {
		return 0, false
	}
	return n * unit, true
}

// sameLeaseTime reports whether a and b are the same duration, such as "12h" and "720m".
func sameLeaseTime(a, b string) bool {
	sa, okA := leaseSeconds(a)
	sb, okB := leaseSeconds(b)
	if okA && okB {
		return sa == sb
	}
	return a == b
}

// leaseMinutes returns the minutes of a lease time of the configuration, such as "12h" or "720m".
func leaseMinutes(s string) (int64, bool) {
	if !configLeaseTime.MatchString(s) {
		return 0, false
	}
	seconds, ok := leaseSeconds(s)
	return seconds / 60, ok
}

func validLeaseTime(s string) bool {
	minutes, ok := leaseMinutes(s)
	return ok && minutes >= minLeaseMinutes && minutes <= maxLeaseMinutes
}

func validPrivateIPv4(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4() && addr.IsPrivate()
}

func (r *lanConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_config"
}

func (r *lanConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ipv4Validator := stringCheck{description: "must be an IPv4 address", valid: validIPv4}
	privateIPv4Validator := stringCheck{description: "must be a private IPv4 address", valid: validPrivateIPv4}

	resp.Schema = schema.Schema{
		Description: "Manages the address and the DHCP server of the LAN or of the guest network of the router (`lan.set_config` in the router API). " +
			"Use one instance of this resource for each network. Both networks always exist on the router, so create takes over the network and writes the configured values. " +
			"The web UI writes the address group (`ip`, `netmask`, `ap_isolation`) and the DHCP group (the other attributes) in two calls; " +
			"the provider sends a group only when one of its attributes changed, and the address group first. " +
			"**Danger:** a change of `ip` or `netmask` of the `lan` network changes the address of the router. The provider then loses its connection, " +
			"so the write of the DHCP group and the read after the write fail: change the provider `host` to the new address and apply again. " +
			"Also a wrong DHCP configuration or a disabled DHCP server of the `lan` network can cut the access to the router of the clients that get their address by DHCP. " +
			"A write probably restarts the network and the DHCP server. " +
			"The router does not accept a network that contains the address of the other network or of the uplink (errors -7, -8, -9). " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"interface": schema.StringAttribute{
				Description: "The network: `lan` or `guest` (`interface` in the router API). Changing it replaces the resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf(lanInterfaces...),
				},
			},
			"ip": schema.StringAttribute{
				Description: "Private IPv4 address of the router in the network (`ip` in the router API). It cannot be the network or the broadcast address.",
				Required:    true,
				Validators:  []validator.String{privateIPv4Validator},
			},
			"netmask": schema.StringAttribute{
				Description: "Netmask of the network, for example `255.255.255.0` (`netmask` in the router API). " +
					"The prefix length must be 8 to 30, at least 12 for a `172.*` address and at least 16 for a `192.*` address.",
				Required: true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 netmask such as 255.255.255.0", valid: validNetmask},
				},
			},
			"ap_isolation": schema.BoolAttribute{
				Description: "Whether the Wi-Fi clients of the network cannot reach each other (`ap_isolate` in the router API).",
				Required:    true,
			},
			"dhcp_enabled": schema.BoolAttribute{
				Description: "Whether the DHCP server of the network is enabled (`enable` in the router API). " +
					"When it is `false`, the provider sends only `enable: 0`, as the web UI does, and the other DHCP attributes cannot be set.",
				Required: true,
			},
			"dhcp_start": schema.StringAttribute{
				Description: "First IPv4 address that the DHCP server gives, for example `192.168.8.100` (`start` in the router API). " +
					"Must be in the network and less than `dhcp_end`. Required when `dhcp_enabled` is `true`.",
				Optional:   true,
				Validators: []validator.String{privateIPv4Validator},
			},
			"dhcp_end": schema.StringAttribute{
				Description: "Last IPv4 address that the DHCP server gives, for example `192.168.8.249` (`end` in the router API). " +
					"Must be in the network. Required when `dhcp_enabled` is `true`.",
				Optional:   true,
				Validators: []validator.String{privateIPv4Validator},
			},
			"lease_time": schema.StringAttribute{
				Description: "DHCP lease time in hours or minutes, for example `12h` or `720m`, from 2 minutes to 71582788 minutes (`leasetime` in the router API). " +
					"The provider sends it in minutes, as the web UI does. Two values with the same duration, such as `12h` and `720m`, are equal: " +
					"when the router reports a lease time with the same duration as the state, the state keeps its value. " +
					"Required when `dhcp_enabled` is `true`.",
				Optional: true,
				Validators: []validator.String{
					stringCheck{description: "must be a number of hours or minutes such as 12h or 720m, from 2 to 71582788 minutes", valid: validLeaseTime},
				},
			},
			"gateway": schema.StringAttribute{
				Description: "IPv4 address of the gateway that the DHCP server gives (`gateway` in the router API). When it is not set, the clients get the router.",
				Optional:    true,
				Validators:  []validator.String{ipv4Validator},
			},
			"dns": schema.ListAttribute{
				Description: "One or two IPv4 addresses of the DNS servers that the DHCP server gives (`dns` in the router API). When it is not set, the clients get the router.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 2),
					listvalidator.ValueStringsAre(ipv4Validator),
				},
			},
		},
	}
}

func (r *lanConfigResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "the DHCP attributes go with dhcp_enabled, and the addresses fit the network",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config lanConfigResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if !config.DHCPEnabled.IsNull() && !config.DHCPEnabled.IsUnknown() {
				if config.DHCPEnabled.ValueBool() {
					for name, value := range map[string]types.String{"dhcp_start": config.DHCPStart, "dhcp_end": config.DHCPEnd, "lease_time": config.LeaseTime} {
						require(diags, name, value, "when dhcp_enabled is true")
					}
				} else {
					const reason = "when dhcp_enabled is false"
					forbid(diags, "dhcp_start", config.DHCPStart, reason)
					forbid(diags, "dhcp_end", config.DHCPEnd, reason)
					forbid(diags, "lease_time", config.LeaseTime, reason)
					forbid(diags, "gateway", config.Gateway, reason)
					forbid(diags, "dns", config.DNS, reason)
				}
			}

			validateLANAddresses(config, diags)
		},
	}}
}

// knownAddress returns the address of a known, valid value. The attribute validators report invalid values.
func knownAddress(v types.String) (netip.Addr, bool) {
	if v.IsNull() || v.IsUnknown() {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(v.ValueString())
	return addr, err == nil && addr.Is4()
}

// validateLANAddresses applies the rules of the web UI for the network of ip and netmask and for the DHCP range.
func validateLANAddresses(config lanConfigResourceModel, diags *diag.Diagnostics) {
	ip, ipKnown := knownAddress(config.IP)
	if config.Netmask.IsNull() || config.Netmask.IsUnknown() {
		return
	}
	bits, ok := netmaskBits(config.Netmask.ValueString())
	if !ok || !ipKnown {
		return
	}

	minBits := 8
	switch ip.As4()[0] {
	case 172:
		minBits = 12
	case 192:
		minBits = 16
	}
	if bits < minBits || bits > 30 {
		diags.AddAttributeError(path.Root("netmask"), invalidCombination,
			fmt.Sprintf("The prefix length of netmask must be %d to 30 for the address %s, got %d.", minBits, ip, bits))
		return
	}

	network := netip.PrefixFrom(ip, bits).Masked()
	if ip == network.Addr() || ip == broadcast(network) {
		diags.AddAttributeError(path.Root("ip"), invalidCombination,
			fmt.Sprintf("ip cannot be the network or the broadcast address of %s.", network))
	}

	start, startKnown := knownAddress(config.DHCPStart)
	end, endKnown := knownAddress(config.DHCPEnd)
	if startKnown && !network.Contains(start) {
		diags.AddAttributeError(path.Root("dhcp_start"), invalidCombination, fmt.Sprintf("dhcp_start must be in the network %s.", network))
	}
	if endKnown && !network.Contains(end) {
		diags.AddAttributeError(path.Root("dhcp_end"), invalidCombination, fmt.Sprintf("dhcp_end must be in the network %s.", network))
	}
	if startKnown && endKnown && !start.Less(end) {
		diags.AddAttributeError(path.Root("dhcp_end"), invalidCombination, "dhcp_start must be less than dhcp_end.")
	}
}

func broadcast(network netip.Prefix) netip.Addr {
	b := network.Addr().As4()
	host := ^uint32(0) >> network.Bits()
	b[0] |= byte(host >> 24)
	b[1] |= byte(host >> 16)
	b[2] |= byte(host >> 8)
	b[3] |= byte(host)
	return netip.AddrFrom4(b)
}

func (r *lanConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *lanConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lanConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, nil, &resp.State, &resp.Diagnostics)
}

func (r *lanConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lanConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.LAN.GetConfig(ctx, state.Interface.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Configuration", err.Error())
		return
	}
	if config == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, lanConfigModel(ctx, config, state.LeaseTime, &resp.Diagnostics))...)
}

func (r *lanConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lanConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, &state, &resp.State, &resp.Diagnostics)
}

func addressChanged(plan, prior lanConfigResourceModel) bool {
	return !plan.IP.Equal(prior.IP) || !plan.Netmask.Equal(prior.Netmask) || !plan.APIsolation.Equal(prior.APIsolation)
}

func dhcpChanged(plan, prior lanConfigResourceModel) bool {
	return !plan.DHCPEnabled.Equal(prior.DHCPEnabled) || !plan.DHCPStart.Equal(prior.DHCPStart) || !plan.DHCPEnd.Equal(prior.DHCPEnd) ||
		plan.LeaseTime.IsNull() != prior.LeaseTime.IsNull() || !sameLeaseTime(plan.LeaseTime.ValueString(), prior.LeaseTime.ValueString()) ||
		!plan.Gateway.Equal(prior.Gateway) || !plan.DNS.Equal(prior.DNS)
}

func (r *lanConfigResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *lanConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != "lan" && req.ID != "guest" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_lan_config must be \"lan\" or \"guest\", got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("interface"), req.ID)...)
}

// write sends the address group and the DHCP group of plan: on create (prior is nil) both, else each one only
// when one of its attributes changed. The DHCP write sends back the lpr values of the router, which the provider
// does not manage, as the web UI does. When the DHCP write fails after the address write, the state gets the
// new address.
func (r *lanConfigResource) write(ctx context.Context, plan lanConfigResourceModel, prior *lanConfigResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	iface := plan.Interface.ValueString()
	address, dhcp := true, true
	if prior != nil {
		address, dhcp = addressChanged(plan, *prior), dhcpChanged(plan, *prior)
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	current, err := r.client.LAN.GetConfig(ctx, iface)
	if err != nil {
		diags.AddError("Unable to Read LAN Configuration", err.Error())
		return
	}
	if current == nil {
		diags.AddAttributeError(path.Root("interface"), "LAN Interface Not Found", fmt.Sprintf("The router has no network %q.", iface))
		return
	}

	if address {
		err := r.client.LAN.SetAddress(ctx, glinet.LANAddress{
			Interface: iface,
			IP:        plan.IP.ValueString(),
			Netmask:   plan.Netmask.ValueString(),
			APIsolate: boolInt(plan.APIsolation.ValueBool()),
		})
		if err != nil {
			diags.AddError("Unable to Set LAN Address", err.Error())
			return
		}
	}

	if dhcp {
		settings := glinet.LANDHCP{Interface: iface}
		if plan.DHCPEnabled.ValueBool() {
			minutes, _ := leaseMinutes(plan.LeaseTime.ValueString())
			settings = glinet.LANDHCP{
				Interface: iface,
				Enable:    1,
				Start:     plan.DHCPStart.ValueString(),
				End:       plan.DHCPEnd.ValueString(),
				LeaseTime: fmt.Sprintf("%dm", minutes),
				Gateway:   plan.Gateway.ValueString(),
				DNS:       listStrings(ctx, plan.DNS, diags),
				LPR:       current.LPR,
			}
		}
		if diags.HasError() {
			return
		}
		if err := r.client.LAN.SetDHCP(ctx, settings); err != nil {
			diags.AddError("Unable to Set LAN DHCP Server", err.Error())
			if address && prior != nil {
				written := *prior
				written.IP, written.Netmask, written.APIsolation = plan.IP, plan.Netmask, plan.APIsolation
				diags.Append(state.Set(ctx, &written)...)
			}
			return
		}
	}

	diags.Append(state.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}

	config, err := r.client.LAN.GetConfig(ctx, iface)
	if err != nil {
		diags.AddError("Unable to Read LAN Configuration", err.Error())
		return
	}
	if config == nil {
		diags.AddError("LAN Interface Not Found", fmt.Sprintf("The router has no network %q after the write.", iface))
		return
	}

	diags.Append(state.Set(ctx, lanConfigModel(ctx, config, plan.LeaseTime, diags))...)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lanConfigModel returns the model of the network config. It keeps the lease time prior when it is the same
// duration as that of the router. The DHCP attributes are null while the DHCP server is disabled.
func lanConfigModel(ctx context.Context, config *glinet.LANConfig, prior types.String, diags *diag.Diagnostics) *lanConfigResourceModel {
	model := &lanConfigResourceModel{
		Interface:   types.StringValue(config.Interface),
		IP:          types.StringValue(config.IP),
		Netmask:     types.StringValue(config.Netmask),
		APIsolation: types.BoolValue(config.APIsolate != 0),
		DHCPEnabled: types.BoolValue(config.Enable != 0),
		DHCPStart:   types.StringNull(),
		DHCPEnd:     types.StringNull(),
		LeaseTime:   types.StringNull(),
		Gateway:     types.StringNull(),
		DNS:         types.ListNull(types.StringType),
	}
	if config.Enable == 0 {
		return model
	}

	model.DHCPStart = stringOrNull(config.Start)
	model.DHCPEnd = stringOrNull(config.End)
	model.LeaseTime = stringOrNull(config.LeaseTime)
	if !prior.IsNull() && !prior.IsUnknown() && sameLeaseTime(prior.ValueString(), config.LeaseTime) {
		model.LeaseTime = prior
	}
	model.Gateway = stringOrNull(config.Gateway)
	if len(config.DNS) > 0 {
		model.DNS = stringList(ctx, config.DNS, diags)
	}
	return model
}
