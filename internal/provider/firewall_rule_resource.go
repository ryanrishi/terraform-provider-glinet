package provider

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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

var (
	_ resource.Resource                = &firewallRuleResource{}
	_ resource.ResourceWithConfigure   = &firewallRuleResource{}
	_ resource.ResourceWithImportState = &firewallRuleResource{}
)

func NewFirewallRuleResource() resource.Resource {
	return &firewallRuleResource{}
}

type firewallRuleResource struct {
	client *glinet.Client
}

type firewallRuleResourceModel struct {
	SourceZone      types.String `tfsdk:"source_zone"`
	SourceIP        types.String `tfsdk:"source_ip"`
	SourceMAC       types.String `tfsdk:"source_mac"`
	SourcePort      types.String `tfsdk:"source_port"`
	Protocol        types.String `tfsdk:"protocol"`
	DestinationZone types.String `tfsdk:"destination_zone"`
	DestinationIP   types.String `tfsdk:"destination_ip"`
	DestinationPort types.String `tfsdk:"destination_port"`
	Action          types.String `tfsdk:"action"`
	Name            types.String `tfsdk:"name"`
	Enabled         types.Bool   `tfsdk:"enabled"`
}

// ruleIdentity identifies a rule by its match fields. An empty field is a field with no value on the router.
// The router ID is not used because it changes when an earlier rule or port forward is removed.
type ruleIdentity struct {
	src, srcIP, srcMAC, srcPort, proto, dest, destIP, destPort string
}

func identityOfRule(r glinet.Rule) ruleIdentity {
	return ruleIdentity{
		src: r.Src, srcIP: r.SrcIP, srcMAC: r.SrcMAC, srcPort: r.SrcPort,
		proto: r.Proto, dest: r.Dest, destIP: r.DestIP, destPort: r.DestPort,
	}
}

func (id ruleIdentity) matches(r glinet.Rule) bool {
	return identityOfRule(r) == id
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

func validIPv4OrCIDR(s string) bool {
	if validIPv4(s) {
		return true
	}
	prefix, err := netip.ParsePrefix(s)
	return err == nil && prefix.Addr().Is4()
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
	ipValidator := stringCheck{description: "must be an IPv4 address or an IPv4 CIDR block such as 192.168.8.0/24", valid: validIPv4OrCIDR}

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
			"Destroy removes the rule from the router. The router cannot remove a rule with no destination port. To delete such a rule, the provider first disables it and sets its destination port to " +
			"`" + removablePort + "`, then removes it. When the remove fails, the provider writes the original values to the rule again.\n\n" +
			"The provider runs the check and the write of one rule at a time, but a change by another client of the router between them is not detected.",
		Attributes: map[string]schema.Attribute{
			"source_zone": sourceZone,
			"source_ip": matchField("IPv4 address or CIDR block that the traffic comes from (`src_ip` in the router API).",
				ipValidator),
			"source_mac": matchField("MAC address that the traffic comes from, in lower case, for example `02:00:00:00:00:01` (`src_mac` in the router API).",
				stringvalidator.RegexMatches(lowerCaseMAC, "must be a MAC address in lower case, for example 02:00:00:00:00:01")),
			"source_port": matchField("Port that the traffic comes from, from 1 to 65535, or a range of ports such as `8000-8010` (`src_port` in the router API).",
				portValidator),
			"protocol": matchField("Protocol: `tcp`, `udp`, `tcp udp`, `icmp` or `all` (`proto` in the router API). "+
				"With no value, OpenWrt matches `tcp udp`. Ports apply only to `tcp` and `udp`.",
				stringvalidator.OneOf("tcp", "udp", "tcp udp", "icmp", "all")),
			"destination_zone": matchField("Firewall zone that the traffic goes to, for example `lan` (`dest` in the router API). "+
				"With no value, the rule matches traffic to the router itself.",
				stringvalidator.LengthAtLeast(1)),
			"destination_ip": matchField("IPv4 address or CIDR block that the traffic goes to (`dest_ip` in the router API).",
				ipValidator),
			"destination_port": matchField("Port that the traffic goes to, from 1 to 65535, or a range of ports such as `8000-8010` (`dest_port` in the router API).",
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
		resp.Diagnostics.AddError("Unable to Update Firewall Rule", err.Error())
		return
	}

	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

// Delete removes the first rule with the identity of the state. The router cannot remove a rule with no
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
		resp.Diagnostics.AddError("Unable to Delete Firewall Rule",
			fmt.Sprintf("The router cannot remove a rule with no destination port, and the write of the destination port %s to the rule %s failed. "+
				"The rule may be unchanged, or disabled with the destination port %s. Remove it on the router. Error: %s",
				removablePort, describeRule(*rule), removablePort, err))
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

	var named []glinet.Rule
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
func (r *firewallRuleResource) find(ctx context.Context, id ruleIdentity, diags *diag.Diagnostics) (*glinet.Rule, error) {
	rules, err := r.client.Firewall.ListRules(ctx)
	if err != nil {
		return nil, err
	}

	var found []glinet.Rule
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

func describeRule(r glinet.Rule) string {
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

func (m firewallRuleResourceModel) rule(id string) glinet.Rule {
	return glinet.Rule{
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

func ruleModel(r *glinet.Rule) *firewallRuleResourceModel {
	return &firewallRuleResourceModel{
		SourceZone:      stringOrNull(r.Src),
		SourceIP:        stringOrNull(r.SrcIP),
		SourceMAC:       stringOrNull(r.SrcMAC),
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
