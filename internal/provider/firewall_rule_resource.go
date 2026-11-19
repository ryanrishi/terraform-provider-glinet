package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

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

// removablePort is the destination port that Delete writes to a rule with no destination port
// before it removes the rule, because the router cannot remove a rule with no destination port.
const removablePort = "65535"

// protocolName is tcp udp, or one protocol name or number in lower case with the characters of the names in
// /etc/protocols, for example esp, ipv6-icmp, ax.25, tp++ or 50.
var protocolName = regexp.MustCompile(`^([a-z0-9][a-z0-9.+-]*|tcp udp)$`)

var (
	_ resource.Resource                = &firewallRuleResource{}
	_ resource.ResourceWithConfigure   = &firewallRuleResource{}
	_ resource.ResourceWithImportState = &firewallRuleResource{}
	_ resource.ResourceWithModifyPlan  = &firewallRuleResource{}
)

func NewFirewallRuleResource() resource.Resource {
	return &firewallRuleResource{}
}

type firewallRuleResource struct {
	client *glinet.Client
}

type firewallRuleResourceModel struct {
	SourceZone      types.String          `tfsdk:"source_zone"`
	SourceIP        types.String          `tfsdk:"source_ip"`
	SourceMAC       caseInsensitiveString `tfsdk:"source_mac"`
	SourcePort      types.String          `tfsdk:"source_port"`
	Protocol        types.String          `tfsdk:"protocol"`
	DestinationZone types.String          `tfsdk:"destination_zone"`
	DestinationIP   types.String          `tfsdk:"destination_ip"`
	DestinationPort types.String          `tfsdk:"destination_port"`
	Action          types.String          `tfsdk:"action"`
	Name            types.String          `tfsdk:"name"`
	Enabled         types.Bool            `tfsdk:"enabled"`
}

// ruleIdentity identifies a rule by its match fields. An empty field is a field with no value on the router.
// The router ID is not used because it changes when an earlier rule or port forward is removed.
// The source MAC address matches without letter case, because the firewall uses it without letter case.
type ruleIdentity struct {
	src, srcIP, srcMAC, srcPort, proto, dest, destIP, destPort string
}

func identityOfRule(r glinet.FirewallRule) ruleIdentity {
	return ruleIdentity{
		src: r.Src, srcIP: r.SrcIP, srcMAC: r.SrcMAC, srcPort: r.SrcPort,
		proto: r.Proto, dest: r.Dest, destIP: r.DestIP, destPort: r.DestPort,
	}
}

func (id ruleIdentity) matches(r glinet.FirewallRule) bool {
	other := identityOfRule(r)
	if !strings.EqualFold(other.srcMAC, id.srcMAC) {
		return false
	}
	other.srcMAC = id.srcMAC
	return other == id
}

func (id ruleIdentity) String() string {
	fields := []struct{ label, value string }{
		{"source zone", id.src},
		{"source IP", id.srcIP},
		{"source MAC", id.srcMAC},
		{"source port", id.srcPort},
		{"protocol", id.proto},
		{"destination zone", id.dest},
		{"destination IP", id.destIP},
		{"destination port", id.destPort},
	}

	var parts []string
	for _, f := range fields {
		value := f.value
		if value == "" {
			value = "none"
		}
		parts = append(parts, f.label+" "+value)
	}
	return strings.Join(parts, ", ")
}

func (r *firewallRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_rule"
}

func (r *firewallRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	matchField := func(description string, validators ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{
			Description: description + " Changing it replaces the rule.",
			Optional:    true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
			Validators: validators,
		}
	}
	ipValidator := stringCheck{
		description: "must be an IPv4 or IPv6 address, or a CIDR block such as 192.168.8.0/24 or 2001:db8::/48",
		valid:       func(s string) bool { return validIP(s) || validPrefix(s) },
	}

	sourceZone := matchField("Firewall zone that the traffic comes from, for example `wan` (`src` in the router API).",
		stringvalidator.LengthAtLeast(1))
	sourceZone.Optional = false
	sourceZone.Required = true

	resp.Schema = schema.Schema{
		Description: "Manages a traffic rule of the firewall. " +
			"The match attributes (`source_zone`, `source_ip`, `source_mac`, `source_port`, `protocol`, `destination_zone`, `destination_ip` and `destination_port`) " +
			"identify the rule, and a match attribute with no value is part of the identity. A change to a match attribute replaces the rule. " +
			"The router does not check rules and accepts duplicates, so create fails when a rule on the router has exactly the same match attributes. " +
			"Create does not detect other rules that match some of the same traffic, for example a rule with a port range that contains the port of this rule. " +
			"When the router has more than one rule with the identity of this resource, the resource uses the first one and shows a warning.\n\n" +
			"The firewall applies the first rule in the list that matches the traffic. The router adds a rule at the end of the list and has no method to move a rule, " +
			"so a replaced rule goes to the end of the list. To keep rules in a given order, add `replace_triggered_by` with the earlier rule " +
			"to the `lifecycle` block of each later rule: Terraform then creates the later rule after the earlier rule, and replaces it when the earlier rule is replaced.\n\n" +
			"Destroy removes the rule from the router.\n\n" +
			"Rules with no destination port: firmware 4.7.0 cannot remove a rule with no destination port, but can change it. " +
			"To delete such a rule, the provider first disables it and sets its destination port to `" + removablePort + "`, then removes it. " +
			"When the remove fails, the provider writes the original values to the rule again. " +
			"Firmware 4.8.3 has a fault: it cannot change or remove a rule with no destination port, because `set_rule` and `remove_rule` return HTTP status 500. " +
			"The router adds such a rule and the firewall uses it, but on firmware 4.8.3 Terraform cannot update or destroy it. " +
			"Thus the plan shows a warning when it creates, changes or destroys a rule with no destination port. " +
			"When a write to such a rule fails with HTTP status 500, the error tells how to remove the rule with SSH and `uci`. " +
			"The web UI uses the same router calls, so it probably cannot remove the rule either (not tested). " +
			"Other firmware versions were not tested.\n\n" +
			"Rules with IPv6 addresses, IPv4 and IPv6 CIDR blocks, the protocols `esp` and `icmpv6`, and a source MAC address in upper case were tested on a GL-AXT1800 with firmware 4.8.3: " +
			"the router accepted the write, and the nftables rule set of the firewall had the rule.\n\n" +
			"The provider runs the check and the write of one rule at a time, but a change by another client of the router between them is not detected.",
		Attributes: map[string]schema.Attribute{
			"source_zone": sourceZone,
			"source_ip": matchField("IPv4 or IPv6 address, or CIDR block, that the traffic comes from, for example `198.51.100.0/24` or `2001:db8::/48` (`src_ip` in the router API). "+
				"The router keeps the value as written, so write it in the same form as on the router.",
				ipValidator),
			"source_mac": schema.StringAttribute{
				Description: "MAC address that the traffic comes from, in lower case or upper case, for example `02:00:00:00:00:01` (`src_mac` in the router API). " +
					"The router keeps the MAC address in the letter case of the write, and the firewall uses it without letter case. " +
					"The provider finds the rule without letter case of the MAC address. A change of the MAC address in more than its letter case replaces the rule.",
				Optional:   true,
				CustomType: caseInsensitiveStringType{},
				PlanModifiers: []planmodifier.String{
					requiresReplaceIfNotLetterCase(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(macAddress, "must be a MAC address, for example 02:00:00:00:00:01"),
				},
			},
			"source_port": matchField("Port that the traffic comes from, from 1 to 65535, or a range of ports such as `8000-8010` (`src_port` in the router API).",
				portValidator),
			"protocol": matchField("Protocol, in lower case (`proto` in the router API): `tcp`, `udp`, `tcp udp`, `icmp`, `all`, or the name or number of an other protocol, for example `esp` or `icmpv6`. "+
				"The router does not check the value and gives it to the OpenWrt firewall. These values were tested on a router: `tcp`, `udp`, `tcp udp`, `icmp`, `all`, `esp` and `icmpv6`. "+
				"What the firewall does with a name that it does not know was not tested. "+
				"With no value, OpenWrt matches `tcp udp`. Ports apply only to `tcp` and `udp`.",
				stringvalidator.RegexMatches(protocolName, "must be tcp udp, or one protocol name or number in lower case (letters, digits, -, . and +), for example tcp, esp, ax.25 or 50")),
			"destination_zone": matchField("Firewall zone that the traffic goes to, for example `lan` (`dest` in the router API). "+
				"With no value, the rule matches traffic to the router itself.",
				stringvalidator.LengthAtLeast(1)),
			"destination_ip": matchField("IPv4 or IPv6 address, or CIDR block, that the traffic goes to (`dest_ip` in the router API). "+
				"The router keeps the value as written, so write it in the same form as on the router.",
				ipValidator),
			"destination_port": matchField("Port that the traffic goes to, from 1 to 65535, or a range of ports such as `8000-8010` (`dest_port` in the router API). "+
				"On firmware 4.8.3, Terraform cannot update or destroy a rule with no destination port: see the description of the resource.",
				portValidator),
			"action": schema.StringAttribute{
				Description: "What the firewall does with the traffic that the rule matches: `ACCEPT`, `DROP` or `REJECT` (`target` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("ACCEPT", "DROP", "REJECT"),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the rule (`name` in the router API). Import finds the rule by its name.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the rule is enabled (`enabled` in the router API). Defaults to `true`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

// ModifyPlan warns when the plan creates, changes or destroys a rule with no destination port,
// because firmware 4.8.3 cannot change or remove such a rule.
func (r *firewallRuleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.Equal(req.State.Raw) {
		return
	}

	var planned, current types.String
	if !req.Plan.Raw.IsNull() {
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("destination_port"), &planned)...)
	}
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("destination_port"), &current)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	if (!req.Plan.Raw.IsNull() && planned.IsNull()) || (!req.State.Raw.IsNull() && current.IsNull()) {
		resp.Diagnostics.AddWarning("Firewall Rule with No Destination Port",
			"Firmware 4.8.3 cannot change or remove a firewall rule with no destination port: set_rule and remove_rule return HTTP status 500. "+
				"The router adds such a rule and the firewall uses it, but on firmware 4.8.3 Terraform cannot update or destroy it. "+
				"Then the only way to remove the rule is SSH on the router. A rule with a destination port does not have this fault. "+
				"On firmware 4.7.0, the provider can change and remove such a rule. Other firmware versions were not tested.")
	}
}

func (r *firewallRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *firewallRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := plan.identity()
	existing, err := r.find(ctx, id, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Rules", err.Error())
		return
	}
	if existing != nil {
		howTo := fmt.Sprintf("Import it with the ID %q, or remove it from the router.", existing.Name)
		if existing.Name == "" {
			howTo = "It has no name, so it cannot be imported: give it a name on the router and import it with that name, or remove it from the router."
		}
		resp.Diagnostics.AddError("Firewall Rule Already Exists",
			fmt.Sprintf("The router already has the rule %s. %s", describeRule(*existing), howTo))
		return
	}

	if _, err := r.client.Firewall.AddRule(ctx, plan.rule("")); err != nil {
		resp.Diagnostics.AddError("Unable to Create Firewall Rule", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *firewallRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.find(ctx, state.identity(), &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Rule", err.Error())
		return
	}
	if rule == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, ruleModel(rule))...)
}

func (r *firewallRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan firewallRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := plan.identity()
	rule, err := r.find(ctx, id, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Rule", err.Error())
		return
	}
	if rule == nil {
		resp.Diagnostics.AddError("Firewall Rule Not Found",
			fmt.Sprintf("The router has no rule with %s. Run terraform apply again to create it.", id))
		return
	}

	if err := r.client.Firewall.SetRule(ctx, plan.rule(rule.ID)); err != nil {
		detail := err.Error()
		if rule.DestPort == "" && isNoDestinationPortFault(err) {
			detail = fmt.Sprintf("The write to the rule %s failed. %s Error: %s", describeRule(*rule), noDestinationPortFault(rule.ID), err)
		}
		resp.Diagnostics.AddError("Unable to Update Firewall Rule", detail)
		return
	}

	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

// Delete removes the first rule with the identity of the state. Firmware 4.7.0 cannot remove a rule with no
// destination port, so Delete first disables such a rule and sets its destination port to removablePort.
// The write keeps the position of the rule, so its ID stays valid for the remove. When the remove fails,
// Delete writes the original values again, so that the identity of the state still finds the rule.
func (r *firewallRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	rule, err := r.find(ctx, state.identity(), &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Rule", err.Error())
		return
	}
	if rule == nil {
		return
	}

	if rule.DestPort != "" {
		if err := r.client.Firewall.RemoveRule(ctx, rule.ID); err != nil {
			resp.Diagnostics.AddError("Unable to Delete Firewall Rule", err.Error())
		}
		return
	}

	removable := *rule
	removable.DestPort = removablePort
	removable.Enabled = false
	if err := r.client.Firewall.SetRule(ctx, removable); err != nil {
		fault := ""
		if isNoDestinationPortFault(err) {
			fault = noDestinationPortFault(rule.ID) + " "
		}
		resp.Diagnostics.AddError("Unable to Delete Firewall Rule",
			fmt.Sprintf("Firmware 4.7.0 cannot remove a rule with no destination port, so the provider first writes the destination port %s to the rule. "+
				"This write to the rule %s failed. The rule may be unchanged, or disabled with the destination port %s. %sError: %s",
				removablePort, describeRule(*rule), removablePort, fault, err))
		return
	}
	if err := r.client.Firewall.RemoveRule(ctx, rule.ID); err != nil {
		if restoreErr := r.client.Firewall.SetRule(ctx, *rule); restoreErr != nil {
			resp.Diagnostics.AddError("Unable to Delete Firewall Rule",
				fmt.Sprintf("The rule %s had no destination port, so the provider disabled it and set its destination port to %s before the remove. "+
					"The remove failed, and the restore of the original values also failed, so the router can have the rule %s. "+
					"Terraform cannot find this rule with the destination port %s. Remove it on the router. Remove error: %s. Restore error: %s",
					describeRule(*rule), removablePort, describeRule(removable), removablePort, err, restoreErr))
			return
		}
		resp.Diagnostics.AddError("Unable to Delete Firewall Rule",
			fmt.Sprintf("The rule %s had no destination port, so the provider disabled it and set its destination port to %s before the remove. "+
				"The remove failed, so the provider restored the original values, and the rule is unchanged on the router. Error: %s",
				describeRule(*rule), removablePort, err))
	}
}

// ImportState finds the rule with exactly the name of the import ID.
func (r *firewallRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			"The import ID of glinet_firewall_rule is the name of the rule on the router. A rule with no name cannot be imported.")
		return
	}

	rules, err := r.client.Firewall.ListRules(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Rules", err.Error())
		return
	}

	var named []glinet.FirewallRule
	for _, rule := range rules {
		if rule.Name == req.ID {
			named = append(named, rule)
		}
	}
	switch len(named) {
	case 0:
		resp.Diagnostics.AddError("Firewall Rule Not Found",
			fmt.Sprintf("The router has no rule with the name %q. A rule with no name cannot be imported: give it a name on the router first.", req.ID))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Ambiguous Firewall Rule Name",
			fmt.Sprintf("The router has %d rules with the name %q, so the name is ambiguous. Rename all of them except one on the router, then import again.",
				len(named), req.ID))
		return
	}

	rule := named[0]
	if first := rules[slices.IndexFunc(rules, identityOfRule(rule).matches)]; first.ID != rule.ID {
		resp.Diagnostics.AddError("Firewall Rule Has an Earlier Duplicate",
			fmt.Sprintf("The rule %s has the same match fields as the earlier rule %s. The resource always uses the first rule with its match fields, "+
				"so it would manage the earlier rule. Remove one of the two rules on the router, then import again.",
				describeRule(rule), describeRule(first)))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, ruleModel(&rule))...)
}

// find returns the first rule with the identity id, because the firewall applies the first matching rule.
// It adds a warning when the router has more than one.
func (r *firewallRuleResource) find(ctx context.Context, id ruleIdentity, diags *diag.Diagnostics) (*glinet.FirewallRule, error) {
	rules, err := r.client.Firewall.ListRules(ctx)
	if err != nil {
		return nil, err
	}

	var found []glinet.FirewallRule
	for _, rule := range rules {
		if id.matches(rule) {
			found = append(found, rule)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}

	for _, rule := range found[1:] {
		diags.AddWarning("Duplicate Firewall Rule",
			fmt.Sprintf("The router has more than one rule with %s. This resource uses the first one, %s. "+
				"Remove the duplicate %s from the router.", id, describeRule(found[0]), describeRule(rule)))
	}
	return &found[0], nil
}

func (r *firewallRuleResource) readBack(ctx context.Context, id ruleIdentity, state *tfsdk.State, diags *diag.Diagnostics) {
	rule, err := r.find(ctx, id, diags)
	if err != nil {
		diags.AddError("Unable to Read Firewall Rule", err.Error())
		return
	}
	if rule == nil {
		diags.AddError("Firewall Rule Not Found", fmt.Sprintf("The router has no rule with %s after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, ruleModel(rule))...)
}

// isNoDestinationPortFault reports whether err is the HTTP status 500 that firmware 4.8.3 returns for set_rule and
// remove_rule of a rule with no destination port.
func isNoDestinationPortFault(err error) bool {
	var httpErr *glinet.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusInternalServerError && httpErr.Module == "firewall" &&
		(httpErr.Method == "set_rule" || httpErr.Method == "remove_rule")
}

// noDestinationPortFault explains the fault of firmware 4.8.3 with a rule with no destination port, and how to remove the rule with the router ID id.
func noDestinationPortFault(id string) string {
	return fmt.Sprintf("The rule has no destination port. Firmware 4.8.3 cannot change or remove such a rule: set_rule and remove_rule return HTTP status 500 (a fault in the firmware). "+
		"The web UI uses the same router calls, so it probably cannot remove the rule either (not tested). "+
		"To remove the rule, connect to the router with SSH and run: uci delete firewall.%s && uci commit firewall && /etc/init.d/firewall reload "+
		"(%s is the router ID of the rule now; it changes when an earlier rule or port forward is removed). "+
		"Then run Terraform again: it creates the rule again with the values of the configuration, or completes the destroy.",
		id, id)
}

func describeRule(r glinet.FirewallRule) string {
	details := fmt.Sprintf("action %s, enabled %t, %s", orNone(r.Target), r.Enabled, identityOfRule(r))
	if r.Name == "" {
		return "(" + details + ")"
	}
	return fmt.Sprintf("%q (%s)", r.Name, details)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (m firewallRuleResourceModel) identity() ruleIdentity {
	return identityOfRule(m.rule(""))
}

func (m firewallRuleResourceModel) rule(id string) glinet.FirewallRule {
	return glinet.FirewallRule{
		ID:       id,
		Name:     m.Name.ValueString(),
		Src:      m.SourceZone.ValueString(),
		SrcIP:    m.SourceIP.ValueString(),
		SrcMAC:   m.SourceMAC.ValueString(),
		SrcPort:  m.SourcePort.ValueString(),
		Proto:    m.Protocol.ValueString(),
		Dest:     m.DestinationZone.ValueString(),
		DestIP:   m.DestinationIP.ValueString(),
		DestPort: m.DestinationPort.ValueString(),
		Target:   m.Action.ValueString(),
		Enabled:  m.Enabled.ValueBool(),
	}
}

func ruleModel(r *glinet.FirewallRule) *firewallRuleResourceModel {
	return &firewallRuleResourceModel{
		SourceZone:      stringOrNull(r.Src),
		SourceIP:        stringOrNull(r.SrcIP),
		SourceMAC:       caseInsensitiveString{StringValue: stringOrNull(r.SrcMAC)},
		SourcePort:      stringOrNull(r.SrcPort),
		Protocol:        stringOrNull(r.Proto),
		DestinationZone: stringOrNull(r.Dest),
		DestinationIP:   stringOrNull(r.DestIP),
		DestinationPort: stringOrNull(r.DestPort),
		Action:          stringOrNull(r.Target),
		Name:            stringOrNull(r.Name),
		Enabled:         types.BoolValue(r.Enabled),
	}
}
