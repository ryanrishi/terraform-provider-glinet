package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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

const (
	dnsID       = "dns"
	dotProtocol = "DoT"
)

// dotProviders has the values of dot_provider. The index is the router code (dot_provider "0" to "3").
var dotProviders = []string{"cloudflare", "nextdns", "controld", "controld_free"}

var (
	_ resource.Resource                     = &dnsResource{}
	_ resource.ResourceWithConfigure        = &dnsResource{}
	_ resource.ResourceWithImportState      = &dnsResource{}
	_ resource.ResourceWithConfigValidators = &dnsResource{}
	_ resource.ResourceWithModifyPlan       = &dnsResource{}
)

func NewDNSResource() resource.Resource {
	return &dnsResource{}
}

type dnsResource struct {
	client *glinet.Client
}

type dnsResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Mode             types.String `tfsdk:"mode"`
	ForceDNS         types.Bool   `tfsdk:"force_dns_enabled"`
	RebindProtection types.Bool   `tfsdk:"rebind_protection_enabled"`
	OverrideVPN      types.Bool   `tfsdk:"override_vpn_enabled"`
	Servers          types.List   `tfsdk:"servers"`
	Protocol         types.String `tfsdk:"protocol"`
	DoTProvider      types.String `tfsdk:"dot_provider"`
	DoTResolverID    types.String `tfsdk:"dot_resolver_id"`
}

func (r *dnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns"
}

func (r *dnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the DNS settings of the router. The router has one DNS configuration, so use one instance of this resource. " +
			"The router sets `force_dns`, `rebind_protection` and `override_vpn` to `false` when a write does not have them, so the provider always writes all three. " +
			"Only the writes in mode `auto` were tested on a router. The parameters of the modes `manual`, `proxy` and `secure` come from the router's web UI. " +
			destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + dnsID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				Description: "DNS mode (`mode` in the router API): `auto` (the router uses the DNS servers of the uplink and the VPN), " +
					"`manual` (the router uses the DNS servers of `servers`), `proxy` (the router sends the DNS queries to one DNS server and port), " +
					"or `secure` (encrypted DNS, see `protocol`). " +
					"When it is not set, the provider does not write the mode, and the router keeps it. Then `servers`, `protocol`, `dot_provider` and `dot_resolver_id` cannot be set. " +
					"The web UI does not write the mode while AdGuard Home is enabled.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("auto", "manual", "proxy", "secure"),
				},
			},
			"force_dns_enabled": schema.BoolAttribute{
				Description: "Whether the router makes the clients use its own DNS server (`force_dns` in the router API).",
				Required:    true,
			},
			"rebind_protection_enabled": schema.BoolAttribute{
				Description: "Whether DNS rebinding protection is enabled (`rebind_protection` in the router API).",
				Required:    true,
			},
			"override_vpn_enabled": schema.BoolAttribute{
				Description: "Whether the DNS settings of the router override the DNS servers of a VPN client (`override_vpn` in the router API). " +
					"A change can change the DNS servers that an active VPN client tunnel uses.",
				Required: true,
			},
			"servers": schema.ListAttribute{
				Description: "DNS servers (`server` in the router API). The value depends on `mode`. " +
					"In mode `manual` it is required: 1 to 4 IPv4 or IPv6 addresses, and at least one address must be IPv4. " +
					"In mode `proxy` it is required: one item `<ip>#<port>`, for example `192.0.2.10#5353`, with a port from 1 to 65535. " +
					"In mode `secure` with `protocol` `DoH`, `DNSCrypt` or `oDoH` it is required: one or more server names from the data source `glinet_dns_secure_servers`. " +
					"The provider does not check the names, and the router gets them as they are. " +
					"In mode `auto` it cannot be set: it is the upstream DNS servers that the router uses, each as `<interface> <ip>`, for example `wwan 192.168.100.53`. " +
					"The router learns them from the uplink and the VPN, so they can change at any time. " +
					"In mode `secure` with `protocol` `DoT`, and when `mode` is not set, it cannot be set, and it is the value that the router returns. It is not known what the router returns for `DoT`. " +
					"In these three cases, a change of the router value does not make a plan diff.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"protocol": schema.StringAttribute{
				Description: "Encrypted DNS protocol (`proto` in the router API): `DoT` (DNS over TLS), `DNSCrypt`, `DoH` (DNS over HTTPS) or `oDoH` (Oblivious DNS over HTTPS). " +
					"Required when `mode` is `secure`, and can be set only then.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf(dotProtocol, "DNSCrypt", "DoH", "oDoH"),
				},
			},
			"dot_provider": schema.StringAttribute{
				Description: "DNS over TLS provider (`dot_provider` in the router API): `cloudflare` (`0`), `nextdns` (`1`), `controld` (Control D with a paid plan, `2`) " +
					"or `controld_free` (Control D with a free resolver, `3`). Required when `protocol` is `DoT`, and can be set only then. " +
					"The state has no value when the router gives a code that is not in this list.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf(dotProviders...),
				},
			},
			"dot_resolver_id": schema.StringAttribute{
				Description: "Resolver ID of the DNS over TLS provider: the NextDNS configuration ID (`nextdns_id` in the router API), " +
					"the Control D resolver ID of a paid plan (`controld_id`), or the Control D free resolver, for example `p0` (`free_controld_id`). " +
					"Required when `dot_provider` is `controld` or `controld_free`, optional when it is `nextdns`, and cannot be set for `cloudflare`. " +
					"The provider writes and reads only the ID of the configured provider. It is not known whether the router keeps the IDs of the other providers.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
	}
}

func (r *dnsResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "servers, protocol, dot_provider and dot_resolver_id are required or cannot be set, as given by mode, protocol and dot_provider",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config dnsResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.Mode.IsUnknown() {
				return
			}

			mode := config.Mode.ValueString()
			when := "when mode is " + mode
			if config.Mode.IsNull() {
				when = "when mode is not set"
			}
			if mode != "secure" {
				forbid(diags, "protocol", config.Protocol, when)
				forbid(diags, "dot_provider", config.DoTProvider, when)
				forbid(diags, "dot_resolver_id", config.DoTResolverID, when)
			}

			switch mode {
			case "", "auto":
				forbid(diags, "servers", config.Servers, when)
			case "manual":
				require(diags, "servers", config.Servers, when)
				validateManualServers(ctx, config.Servers, diags)
			case "proxy":
				require(diags, "servers", config.Servers, when)
				validateProxyServers(ctx, config.Servers, diags)
			case "secure":
				validateSecure(config, diags)
			}
		},
	}}
}

func validateSecure(config dnsResourceModel, diags *diag.Diagnostics) {
	require(diags, "protocol", config.Protocol, "when mode is secure")
	if config.Protocol.IsNull() || config.Protocol.IsUnknown() {
		return
	}

	when := "when protocol is " + config.Protocol.ValueString()
	if config.Protocol.ValueString() != dotProtocol {
		require(diags, "servers", config.Servers, when)
		forbid(diags, "dot_provider", config.DoTProvider, when)
		forbid(diags, "dot_resolver_id", config.DoTResolverID, when)
		return
	}

	forbid(diags, "servers", config.Servers, when)
	require(diags, "dot_provider", config.DoTProvider, when)
	if config.DoTProvider.IsNull() || config.DoTProvider.IsUnknown() {
		return
	}

	when = "when dot_provider is " + config.DoTProvider.ValueString()
	switch config.DoTProvider.ValueString() {
	case "cloudflare":
		forbid(diags, "dot_resolver_id", config.DoTResolverID, when)
	case "controld", "controld_free":
		require(diags, "dot_resolver_id", config.DoTResolverID, when)
	}
}

// knownServers returns the servers of the configuration, and false when the list is null or unknown.
// An unknown item is an unknown value in the result.
func knownServers(ctx context.Context, list types.List, diags *diag.Diagnostics) ([]types.String, bool) {
	if list.IsNull() || list.IsUnknown() {
		return nil, false
	}
	var servers []types.String
	diags.Append(list.ElementsAs(ctx, &servers, false)...)
	return servers, !diags.HasError()
}

func addServerError(diags *diag.Diagnostics, index int, detail string) {
	diags.AddAttributeError(path.Root("servers").AtListIndex(index), "Invalid Attribute Value", detail)
}

func validateManualServers(ctx context.Context, list types.List, diags *diag.Diagnostics) {
	servers, ok := knownServers(ctx, list, diags)
	if !ok {
		return
	}
	if len(servers) > 4 {
		diags.AddAttributeError(path.Root("servers"), "Invalid Attribute Value",
			fmt.Sprintf("servers must have 1 to 4 items when mode is manual, got: %d.", len(servers)))
	}

	hasIPv4, allKnown := false, true
	for i, s := range servers {
		if s.IsUnknown() {
			allKnown = false
			continue
		}
		switch {
		case validIPv4(s.ValueString()):
			hasIPv4 = true
		case !validIPv6(s.ValueString()):
			addServerError(diags, i, fmt.Sprintf("Each item of servers must be an IPv4 or IPv6 address when mode is manual, got: %q.", s.ValueString()))
		}
	}
	if allKnown && !hasIPv4 {
		diags.AddAttributeError(path.Root("servers"), "Invalid Attribute Value",
			"servers must have at least one IPv4 address when mode is manual.")
	}
}

func validateProxyServers(ctx context.Context, list types.List, diags *diag.Diagnostics) {
	servers, ok := knownServers(ctx, list, diags)
	if !ok {
		return
	}
	if len(servers) != 1 {
		diags.AddAttributeError(path.Root("servers"), "Invalid Attribute Value",
			fmt.Sprintf("servers must have exactly 1 item when mode is proxy, got: %d.", len(servers)))
	}
	for i, s := range servers {
		if !s.IsUnknown() && !validProxyServer(s.ValueString()) {
			addServerError(diags, i, fmt.Sprintf("Each item of servers must be <ip>#<port> with a port from 1 to 65535 when mode is proxy, for example 192.0.2.10#5353, got: %q.", s.ValueString()))
		}
	}
}

func validProxyServer(s string) bool {
	host, port, ok := strings.Cut(s, "#")
	if !ok || (!validIPv4(host) && !validIPv6(host)) {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535 && strconv.Itoa(p) == port
}

// ModifyPlan plans null for protocol, dot_provider and dot_resolver_id when the configured mode does not use them,
// so that a router value that the configuration does not have makes a plan diff. The read gives null for them in
// these modes. When the plan changes the resource and servers is not configured, servers is unknown.
func (r *dnsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var config dnsResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Mode.IsNull() || config.Mode.IsUnknown() {
		return
	}

	for name, value := range map[string]types.String{
		"protocol":        config.Protocol,
		"dot_provider":    config.DoTProvider,
		"dot_resolver_id": config.DoTResolverID,
	} {
		if value.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), types.StringNull())...)
		}
	}

	if config.Servers.IsNull() && !req.State.Raw.IsNull() && !resp.Plan.Raw.Equal(req.State.Raw) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("servers"), types.ListUnknown(types.StringType))...)
	}
}

func (r *dnsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *dnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Config, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *dnsResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *dnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Config, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *dnsResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *dnsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != dnsID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_dns must be %q, got %q.", dnsID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), dnsID)...)
}

func (r *dnsResource) set(ctx context.Context, config tfsdk.Config, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model dnsResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	var configuredMode types.String
	diags.Append(config.GetAttribute(ctx, path.Root("mode"), &configuredMode)...)
	if diags.HasError() {
		return
	}

	request := glinet.SetDNSConfigRequest{
		Mode:             configuredMode.ValueString(),
		ForceDNS:         model.ForceDNS.ValueBool(),
		RebindProtection: model.RebindProtection.ValueBool(),
		OverrideVPN:      model.OverrideVPN.ValueBool(),
	}
	switch request.Mode {
	case "manual", "proxy":
		request.Server = listStrings(ctx, model.Servers, diags)
	case "secure":
		request.Proto = model.Protocol.ValueString()
		if request.Proto == dotProtocol {
			setDoTProvider(&request, model.DoTProvider.ValueString(), model.DoTResolverID.ValueString())
		} else {
			request.Server = listStrings(ctx, model.Servers, diags)
		}
	}
	if diags.HasError() {
		return
	}

	if err := r.client.DNS.SetConfig(ctx, request); err != nil {
		diags.AddError("Unable to Set DNS Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(dnsID)
	for _, value := range []*types.String{&model.Mode, &model.Protocol, &model.DoTProvider, &model.DoTResolverID} {
		if value.IsUnknown() {
			*value = types.StringNull()
		}
	}
	if model.Servers.IsUnknown() {
		model.Servers = types.ListNull(types.StringType)
	}
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func setDoTProvider(request *glinet.SetDNSConfigRequest, provider, resolverID string) {
	request.DoTProvider = strconv.Itoa(slices.Index(dotProviders, provider))
	switch provider {
	case "nextdns":
		request.NextDNSID = resolverID
	case "controld":
		request.ControlDID = resolverID
	case "controld_free":
		request.FreeControlDID = resolverID
	}
}

// dotProvider returns the dot_provider and dot_resolver_id of the router configuration c in mode secure with
// protocol DoT. They are null for a code that is not known, and the ID is null when it is empty.
func dotProvider(c *glinet.DNSConfig) (types.String, types.String) {
	code, err := strconv.Atoi(c.DoTProvider)
	if err != nil || code < 0 || code >= len(dotProviders) || strconv.Itoa(code) != c.DoTProvider {
		return types.StringNull(), types.StringNull()
	}

	provider := dotProviders[code]
	resolverID := map[string]string{
		"nextdns":       c.NextDNSID,
		"controld":      c.ControlDID,
		"controld_free": c.FreeControlDID,
	}[provider]
	return types.StringValue(provider), stringOrNull(resolverID)
}

func (r *dnsResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.DNS.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read DNS Configuration", err.Error())
		return
	}

	servers := stringList(ctx, config.Server, diags)
	if diags.HasError() {
		return
	}

	model := dnsResourceModel{
		ID:               types.StringValue(dnsID),
		Mode:             types.StringValue(config.Mode),
		ForceDNS:         types.BoolValue(config.ForceDNS),
		RebindProtection: types.BoolValue(config.RebindProtection),
		OverrideVPN:      types.BoolValue(config.OverrideVPN),
		Servers:          servers,
		Protocol:         types.StringNull(),
		DoTProvider:      types.StringNull(),
		DoTResolverID:    types.StringNull(),
	}
	if config.Mode == "secure" {
		model.Protocol = stringOrNull(config.Proto)
		if config.Proto == dotProtocol {
			model.DoTProvider, model.DoTResolverID = dotProvider(config)
		}
	}

	diags.Append(state.Set(ctx, &model)...)
}
