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
	_ resource.Resource                     = &lanResource{}
	_ resource.ResourceWithConfigure        = &lanResource{}
	_ resource.ResourceWithImportState      = &lanResource{}
	_ resource.ResourceWithConfigValidators = &lanResource{}
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

func NewLANResource() resource.Resource {
	return &lanResource{}
}

type lanResource struct {
	client *glinet.Client
}

type lanResourceModel struct {
	Name          types.String `tfsdk:"name"`
	IP            types.String `tfsdk:"ip"`
	Netmask       types.String `tfsdk:"netmask"`
	APIsolation   types.Bool   `tfsdk:"ap_isolation"`
	WANIsolation  types.Bool   `tfsdk:"wan_isolation"`
	DHCPEnabled   types.Bool   `tfsdk:"dhcp_enabled"`
	DHCPStart     types.String `tfsdk:"dhcp_start"`
	DHCPEnd       types.String `tfsdk:"dhcp_end"`
	DHCPLeaseTime types.String `tfsdk:"dhcp_lease_time"`
	DHCPGateway   types.String `tfsdk:"dhcp_gateway"`
	DHCPDNS       types.List   `tfsdk:"dhcp_dns"`
	DHCPLPR       types.List   `tfsdk:"dhcp_lpr_servers"`
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

func (r *lanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan"
}

func (r *lanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ipv4Validator := stringCheck{description: "must be an IPv4 address", valid: validIPv4}
	privateIPv4Validator := stringCheck{description: "must be a private IPv4 address", valid: validPrivateIPv4}

	resp.Schema = schema.Schema{
		Description: "Manages the address and the DHCP server of the LAN or of the guest network of the router (`lan.set_config` in the router API). " +
			"Use one instance of this resource for each network. Both networks always exist on the router, so create takes over the network. " +
			"The web UI writes the address group (`ip`, `netmask`, `ap_isolation`, `wan_isolation`) and the DHCP group (the other attributes) in two calls. " +
			"The provider sends a group only when one of its values differs: on create from the values of the router, on update from the state. " +
			"It sends the address group first. A create whose values are equal to those of the router sends no write. " +
			"**Danger:** each write to the LAN configuration, also for the `guest` network and also a write that changes nothing, " +
			"makes the router restart its Wi-Fi a few seconds later. When the uplink of the router is a Wi-Fi repeater, " +
			"the uplink and each VPN on it can drop and may not come back without a manual reconnect or a reboot. " +
			"This was measured on a GL-AXT1800 with firmware 4.7.0. " +
			"A change of `ip` or `netmask` of the `lan` network changes the address of the router. The provider then loses its connection, " +
			"so the write of the DHCP group and the read after the write fail: change the provider `host` to the new address and apply again. " +
			"Also a wrong DHCP configuration or a disabled DHCP server of the `lan` network can cut the access to the router of the clients that get their address by DHCP. " +
			"The router does not accept a network that contains the address of the other network or of the uplink (errors -7, -8, -9). " +
			"Only one write of this resource was tested on a router: `{\"interface\":\"guest\",\"enable\":0}`, the measurement above; the parameters come from the router's web UI. " +
			destroyNote,
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
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
			"wan_isolation": schema.BoolAttribute{
				Description: "Whether the clients of the guest network cannot reach the networks of the uplinks (`wan_isolate` in the router API, \"block WAN\" in the web UI). " +
					"Only for the `guest` network: it cannot be set for the `lan` network and is always null there. " +
					"When it is not set, the provider sends back the value of the router in each address write, so the router value does not change. " +
					"The option is in the web UI of firmware 4.8.3. Firmware 4.7.0 does not report `wan_isolate`; the provider then reads `false`. " +
					"The provider sends `wan_isolate` in each address write of the `guest` network. What firmware without this option does with it is not known.",
				Optional: true,
				Computed: true,
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
			"dhcp_lease_time": schema.StringAttribute{
				Description: "DHCP lease time in hours or minutes, for example `12h` or `720m`, from 2 minutes to 71582788 minutes (`leasetime` in the router API). " +
					"The provider sends it in minutes, as the web UI does. Two values with the same duration, such as `12h` and `720m`, are equal: " +
					"when the router reports a lease time with the same duration as the state, the state keeps its value. " +
					"Required when `dhcp_enabled` is `true`.",
				Optional: true,
				Validators: []validator.String{
					stringCheck{description: "must be a number of hours or minutes such as 12h or 720m, from 2 to 71582788 minutes", valid: validLeaseTime},
				},
			},
			"dhcp_gateway": schema.StringAttribute{
				Description: "IPv4 address of the gateway that the DHCP server gives (`gateway` in the router API). When it is not set, the clients get the router.",
				Optional:    true,
				Validators:  []validator.String{ipv4Validator},
			},
			"dhcp_dns": schema.ListAttribute{
				Description: "One or two IPv4 addresses of the DNS servers that the DHCP server gives (`dns` in the router API). When it is not set, the clients get the router.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 2),
					listvalidator.ValueStringsAre(ipv4Validator),
				},
			},
			"dhcp_lpr_servers": schema.ListAttribute{
				Description: "Up to five IPv4 addresses of LPR servers, each one time, that the DHCP server gives (`lpr` in the router API, DHCP option 9). " +
					"An empty list removes all LPR servers: the provider then sends `lpr: []`, as the web UI of firmware 4.8.3 does. " +
					"When it is not set, the provider sends back the LPR servers of the router in each DHCP write. " +
					"While the DHCP server is enabled, the state has an empty list when the router has no LPR servers. " +
					"What the router does with the LPR servers, and with `lpr: []`, was not verified. Cannot be set when `dhcp_enabled` is `false`.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtMost(5),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(ipv4Validator),
				},
			},
		},
	}
}

func (r *lanResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "the DHCP attributes go with dhcp_enabled, wan_isolation goes with the guest network, and the addresses fit the network",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config lanResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if !config.DHCPEnabled.IsNull() && !config.DHCPEnabled.IsUnknown() {
				if config.DHCPEnabled.ValueBool() {
					for name, value := range map[string]types.String{"dhcp_start": config.DHCPStart, "dhcp_end": config.DHCPEnd, "dhcp_lease_time": config.DHCPLeaseTime} {
						require(diags, name, value, "when dhcp_enabled is true")
					}
				} else {
					const reason = "when dhcp_enabled is false"
					forbid(diags, "dhcp_start", config.DHCPStart, reason)
					forbid(diags, "dhcp_end", config.DHCPEnd, reason)
					forbid(diags, "dhcp_lease_time", config.DHCPLeaseTime, reason)
					forbid(diags, "dhcp_gateway", config.DHCPGateway, reason)
					forbid(diags, "dhcp_dns", config.DHCPDNS, reason)
					forbid(diags, "dhcp_lpr_servers", config.DHCPLPR, reason)
				}
			}

			if config.Name.ValueString() == "lan" {
				forbid(diags, "wan_isolation", config.WANIsolation, "for the lan network")
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
func validateLANAddresses(config lanResourceModel, diags *diag.Diagnostics) {
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

func (r *lanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *lanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lanResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, nil, &resp.State, &resp.Diagnostics)
}

func (r *lanResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lanResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.LAN.GetConfig(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Configuration", err.Error())
		return
	}
	if config == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, lanModel(ctx, config, state.DHCPLeaseTime, &resp.Diagnostics))...)
}

func (r *lanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lanResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, &state, &resp.State, &resp.Diagnostics)
}

func addressChanged(plan, prior lanResourceModel) bool {
	return !plan.IP.Equal(prior.IP) || !plan.Netmask.Equal(prior.Netmask) || !plan.APIsolation.Equal(prior.APIsolation) ||
		!plan.WANIsolation.Equal(prior.WANIsolation)
}

func dhcpChanged(plan, prior lanResourceModel) bool {
	return !plan.DHCPEnabled.Equal(prior.DHCPEnabled) || !plan.DHCPStart.Equal(prior.DHCPStart) || !plan.DHCPEnd.Equal(prior.DHCPEnd) ||
		plan.DHCPLeaseTime.IsNull() != prior.DHCPLeaseTime.IsNull() || !sameLeaseTime(plan.DHCPLeaseTime.ValueString(), prior.DHCPLeaseTime.ValueString()) ||
		!plan.DHCPGateway.Equal(prior.DHCPGateway) || !plan.DHCPDNS.Equal(prior.DHCPDNS) || !plan.DHCPLPR.Equal(prior.DHCPLPR)
}

func (r *lanResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *lanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != "lan" && req.ID != "guest" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_lan must be \"lan\" or \"guest\", got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

// write sends the address group and the DHCP group of plan, each one only when one of its attributes differs
// from prior, or on create (prior is nil) from the router values. An unknown wan_isolation or dhcp_lpr_servers,
// which is not in the configuration, gets the router value, so that a write sends it back unchanged. When the
// DHCP write of an update fails after the address write, the state gets the new address.
func (r *lanResource) write(ctx context.Context, plan lanResourceModel, prior *lanResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	iface := plan.Name.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	current, err := r.client.LAN.GetConfig(ctx, iface)
	if err != nil {
		diags.AddError("Unable to Read LAN Configuration", err.Error())
		return
	}
	if current == nil {
		diags.AddAttributeError(path.Root("name"), "LAN Interface Not Found", fmt.Sprintf("The router has no network %q.", iface))
		return
	}

	if plan.WANIsolation.IsUnknown() {
		plan.WANIsolation = wanIsolationValue(current)
	}
	if plan.DHCPLPR.IsUnknown() {
		plan.DHCPLPR = types.ListNull(types.StringType)
		if plan.DHCPEnabled.ValueBool() {
			plan.DHCPLPR = stringList(ctx, append([]string{}, current.LPR...), diags)
		}
	}

	compared := prior
	if compared == nil {
		compared = lanModel(ctx, current, plan.DHCPLeaseTime, diags)
		if diags.HasError() {
			return
		}
	}
	address, dhcp := addressChanged(plan, *compared), dhcpChanged(plan, *compared)

	if address {
		err := r.client.LAN.SetAddress(ctx, glinet.LANAddress{
			Interface:  iface,
			IP:         plan.IP.ValueString(),
			Netmask:    plan.Netmask.ValueString(),
			APIsolate:  boolInt(plan.APIsolation.ValueBool()),
			WANIsolate: boolInt(plan.WANIsolation.ValueBool()),
		})
		if err != nil {
			diags.AddError("Unable to Set LAN Address", err.Error())
			return
		}
	}

	if dhcp {
		settings := glinet.LANDHCP{Interface: iface}
		if plan.DHCPEnabled.ValueBool() {
			minutes, _ := leaseMinutes(plan.DHCPLeaseTime.ValueString())
			settings = glinet.LANDHCP{
				Interface: iface,
				Enable:    1,
				Start:     plan.DHCPStart.ValueString(),
				End:       plan.DHCPEnd.ValueString(),
				LeaseTime: fmt.Sprintf("%dm", minutes),
				Gateway:   plan.DHCPGateway.ValueString(),
				DNS:       listStrings(ctx, plan.DHCPDNS, diags),
				LPR:       listStrings(ctx, plan.DHCPLPR, diags),
			}
		}
		if diags.HasError() {
			return
		}
		if err := r.client.LAN.SetDHCP(ctx, settings); err != nil {
			diags.AddError("Unable to Set LAN DHCP Server", err.Error())
			if address && prior != nil {
				written := *prior
				written.IP, written.Netmask, written.APIsolation, written.WANIsolation = plan.IP, plan.Netmask, plan.APIsolation, plan.WANIsolation
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

	diags.Append(state.Set(ctx, lanModel(ctx, config, plan.DHCPLeaseTime, diags))...)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// wanIsolationValue returns wan_isolation of the network config: null for the lan network, and false for a
// guest network that has no wan_isolate, as the web UI shows it.
func wanIsolationValue(config *glinet.LANConfig) types.Bool {
	if config.Interface != "guest" {
		return types.BoolNull()
	}
	return types.BoolValue(config.WANIsolate != 0)
}

// lanModel returns the model of the network config. It keeps the lease time prior when it is the same
// duration as that of the router. The DHCP attributes are null while the DHCP server is disabled. While it is enabled,
// dhcp_lpr_servers is an empty list when the router has no LPR servers.
func lanModel(ctx context.Context, config *glinet.LANConfig, prior types.String, diags *diag.Diagnostics) *lanResourceModel {
	model := &lanResourceModel{
		Name:          types.StringValue(config.Interface),
		IP:            types.StringValue(config.IP),
		Netmask:       types.StringValue(config.Netmask),
		APIsolation:   types.BoolValue(config.APIsolate != 0),
		WANIsolation:  wanIsolationValue(config),
		DHCPEnabled:   types.BoolValue(config.Enable != 0),
		DHCPStart:     types.StringNull(),
		DHCPEnd:       types.StringNull(),
		DHCPLeaseTime: types.StringNull(),
		DHCPGateway:   types.StringNull(),
		DHCPDNS:       types.ListNull(types.StringType),
		DHCPLPR:       types.ListNull(types.StringType),
	}
	if config.Enable == 0 {
		return model
	}

	model.DHCPStart = stringOrNull(config.Start)
	model.DHCPEnd = stringOrNull(config.End)
	model.DHCPLeaseTime = stringOrNull(config.LeaseTime)
	if !prior.IsNull() && !prior.IsUnknown() && sameLeaseTime(prior.ValueString(), config.LeaseTime) {
		model.DHCPLeaseTime = prior
	}
	model.DHCPGateway = stringOrNull(config.Gateway)
	if len(config.DNS) > 0 {
		model.DHCPDNS = stringList(ctx, config.DNS, diags)
	}
	model.DHCPLPR = stringList(ctx, append([]string{}, config.LPR...), diags)
	return model
}
