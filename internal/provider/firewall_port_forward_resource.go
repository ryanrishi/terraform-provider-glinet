package provider

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
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

var portOrRange = regexp.MustCompile(`^([1-9][0-9]{0,4})(?:-([1-9][0-9]{0,4}))?$`)

// importProtocols maps the protocol part of an import ID to the protocol value of the router.
var importProtocols = map[string]string{
	"tcp":     "tcp",
	"udp":     "udp",
	"tcp+udp": "tcp udp",
}

var (
	_ resource.Resource                = &firewallPortForwardResource{}
	_ resource.ResourceWithConfigure   = &firewallPortForwardResource{}
	_ resource.ResourceWithImportState = &firewallPortForwardResource{}
)

func NewFirewallPortForwardResource() resource.Resource {
	return &firewallPortForwardResource{}
}

type firewallPortForwardResource struct {
	client *glinet.Client
}

type firewallPortForwardResourceModel struct {
	Protocol     types.String `tfsdk:"protocol"`
	ExternalZone types.String `tfsdk:"external_zone"`
	ExternalPort types.String `tfsdk:"external_port"`
	InternalZone types.String `tfsdk:"internal_zone"`
	InternalIP   types.String `tfsdk:"internal_ip"`
	InternalPort types.String `tfsdk:"internal_port"`
	Name         types.String `tfsdk:"name"`
	Enabled      types.Bool   `tfsdk:"enabled"`
}

// portForwardIdentity identifies a port forward. The router ID is not used because it changes
// when an earlier port forward is removed.
type portForwardIdentity struct {
	externalZone, protocol, externalPort string
}

func (id portForwardIdentity) importID() string {
	return id.externalZone + "/" + strings.ReplaceAll(id.protocol, " ", "+") + "/" + id.externalPort
}

func (id portForwardIdentity) String() string {
	return fmt.Sprintf("external zone %s, protocol %s, external port %s", id.externalZone, id.protocol, id.externalPort)
}

func (id portForwardIdentity) matches(f glinet.PortForward) bool {
	return f.Src == id.externalZone && f.Proto == id.protocol && f.SrcDport == id.externalPort
}

// overlaps reports whether f takes traffic that the port forward id also takes:
// same external zone, a common protocol, and a common external port.
func (id portForwardIdentity) overlaps(f glinet.PortForward) bool {
	if f.Src != id.externalZone || !protocolsIntersect(f.Proto, id.protocol) {
		return false
	}

	lo, hi, ok := parsePorts(f.SrcDport)
	if !ok {
		return false
	}
	idLo, idHi, _ := parsePorts(id.externalPort)
	return lo <= idHi && idLo <= hi
}

// protocolsIntersect reports whether the protocol values a and b have a common protocol.
// No protocol is "tcp udp", the OpenWrt default.
func protocolsIntersect(a, b string) bool {
	if a == "" {
		a = "tcp udp"
	}
	if b == "" {
		b = "tcp udp"
	}

	bs := strings.Fields(b)
	return slices.ContainsFunc(strings.Fields(a), func(p string) bool { return slices.Contains(bs, p) })
}

// parsePorts returns the first and last port of a port such as "8080" or a range such as "8000-8010".
func parsePorts(s string) (lo, hi int, ok bool) {
	m := portOrRange.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}

	lo, _ = strconv.Atoi(m[1])
	hi = lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	return lo, hi, hi <= 65535 && lo <= hi
}

func validPorts(s string) bool {
	_, _, ok := parsePorts(s)
	return ok
}

func validIPv4(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4()
}

// stringCheck is a validator that fails when valid returns false for the value.
// The error does not show the value when sensitive is true.
type stringCheck struct {
	description string
	valid       func(string) bool
	sensitive   bool
}

func (v stringCheck) Description(context.Context) string {
	return v.description
}

func (v stringCheck) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringCheck) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || v.valid(req.ConfigValue.ValueString()) {
		return
	}

	detail := fmt.Sprintf("Attribute %s %s", req.Path, v.Description(ctx))
	if !v.sensitive {
		detail += fmt.Sprintf(", got: %q", req.ConfigValue.ValueString())
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value", detail)
}

var portValidator = stringCheck{
	description: "must be a port from 1 to 65535, or a range of ports such as 8000-8010",
	valid:       validPorts,
}

func (r *firewallPortForwardResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_port_forward"
}

func (r *firewallPortForwardResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a port forward of the firewall. " +
			"The external zone, the protocol and the external port identify the port forward, so a change to one of them replaces it. " +
			"The router does not check port forwards and accepts duplicates, so create fails when a port forward on the router has the same external zone, " +
			"a common protocol and a common external port. " +
			"When the router has more than one port forward with the identity of this resource, the resource uses the first one, as the firewall does, and shows a warning. " +
			"The provider runs the check and the write of one port forward at a time, but a change by another client of the router between them is not detected.",
		Attributes: map[string]schema.Attribute{
			"protocol": schema.StringAttribute{
				Description: "Protocol: `tcp`, `udp` or `tcp udp` (`proto` in the router API). Changing it replaces the port forward.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("tcp", "udp", "tcp udp"),
				},
			},
			"external_zone": schema.StringAttribute{
				Description: "Firewall zone that the traffic comes from, for example `wan` (`src` in the router API). Changing it replaces the port forward.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"external_port": schema.StringAttribute{
				Description: "External port, or a range of ports such as `8000-8010` (`src_dport` in the router API). Changing it replaces the port forward.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{portValidator},
			},
			"internal_zone": schema.StringAttribute{
				Description: "Firewall zone of the destination, for example `lan` (`dest` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"internal_ip": schema.StringAttribute{
				Description: "IPv4 address of the destination (`dest_ip` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 address", valid: validIPv4},
				},
			},
			"internal_port": schema.StringAttribute{
				Description: "Port of the destination, or a range of ports such as `8000-8010` (`dest_port` in the router API).",
				Required:    true,
				Validators:  []validator.String{portValidator},
			},
			"name": schema.StringAttribute{
				Description: "Name of the port forward, shown as Description in the web UI (`name` in the router API).",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the port forward is enabled (`enabled` in the router API). Defaults to `true`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *firewallPortForwardResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *firewallPortForwardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallPortForwardResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	forwards, err := r.client.Firewall.ListPortForwards(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Port Forwards", err.Error())
		return
	}
	id := plan.identity()
	for _, f := range forwards {
		if !id.overlaps(f) {
			continue
		}
		if id.matches(f) {
			resp.Diagnostics.AddError("Firewall Port Forward Already Exists",
				fmt.Sprintf("The router already has the port forward %s. Import it with the ID %q, or remove it from the router.",
					describePortForward(f), id.importID()))
		} else {
			resp.Diagnostics.AddError("Firewall Port Forward Overlaps",
				fmt.Sprintf("The router has the port forward %s, which overlaps with %s. "+
					"Change this port forward, or remove the other one from the router.", describePortForward(f), id))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.Firewall.AddPortForward(ctx, plan.portForward("")); err != nil {
		resp.Diagnostics.AddError("Unable to Create Firewall Port Forward", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *firewallPortForwardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallPortForwardResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	forward, err := r.find(ctx, state.identity(), &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Port Forward", err.Error())
		return
	}
	if forward == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, portForwardModel(forward))...)
}

func (r *firewallPortForwardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan firewallPortForwardResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := plan.identity()
	forward, err := r.find(ctx, id, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Port Forward", err.Error())
		return
	}
	if forward == nil {
		resp.Diagnostics.AddError("Firewall Port Forward Not Found",
			fmt.Sprintf("The router has no port forward with %s. Run terraform apply again to create it.", id))
		return
	}

	if err := r.client.Firewall.SetPortForward(ctx, plan.portForward(forward.ID)); err != nil {
		resp.Diagnostics.AddError("Unable to Update Firewall Port Forward", err.Error())
		return
	}

	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *firewallPortForwardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallPortForwardResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	forward, err := r.find(ctx, state.identity(), &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall Port Forward", err.Error())
		return
	}
	if forward == nil {
		return
	}

	if err := r.client.Firewall.RemovePortForward(ctx, forward.ID); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Firewall Port Forward", err.Error())
	}
}

func (r *firewallPortForwardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || importProtocols[parts[1]] == "" || !validPorts(parts[2]) {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_firewall_port_forward must be <external_zone>/<protocol>/<external_port>, "+
				"where protocol is tcp, udp or tcp+udp, for example wan/tcp/8080 or wan/tcp+udp/8000-8010. Got %q.", req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("external_zone"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("protocol"), importProtocols[parts[1]])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("external_port"), parts[2])...)
}

// find returns the first port forward with the identity id, because the firewall uses the first one.
// It adds a warning when the router has more than one.
func (r *firewallPortForwardResource) find(ctx context.Context, id portForwardIdentity, diags *diag.Diagnostics) (*glinet.PortForward, error) {
	forwards, err := r.client.Firewall.ListPortForwards(ctx)
	if err != nil {
		return nil, err
	}

	var found []glinet.PortForward
	for _, f := range forwards {
		if id.matches(f) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}

	for _, f := range found[1:] {
		diags.AddWarning("Duplicate Firewall Port Forward",
			fmt.Sprintf("The router has more than one port forward with %s. This resource uses the first one, %s, as the firewall does. "+
				"Remove the duplicate %s from the router.", id, describePortForward(found[0]), describePortForward(f)))
	}
	return &found[0], nil
}

func (r *firewallPortForwardResource) readBack(ctx context.Context, id portForwardIdentity, state *tfsdk.State, diags *diag.Diagnostics) {
	forward, err := r.find(ctx, id, diags)
	if err != nil {
		diags.AddError("Unable to Read Firewall Port Forward", err.Error())
		return
	}
	if forward == nil {
		diags.AddError("Firewall Port Forward Not Found", fmt.Sprintf("The router has no port forward with %s after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, portForwardModel(forward))...)
}

func describePortForward(f glinet.PortForward) string {
	id := portForwardIdentity{externalZone: f.Src, protocol: f.Proto, externalPort: f.SrcDport}
	if f.Name == "" {
		return "(" + id.String() + ")"
	}
	return fmt.Sprintf("%q (%s)", f.Name, id)
}

func (m firewallPortForwardResourceModel) identity() portForwardIdentity {
	return portForwardIdentity{externalZone: m.ExternalZone.ValueString(), protocol: m.Protocol.ValueString(), externalPort: m.ExternalPort.ValueString()}
}

func (m firewallPortForwardResourceModel) portForward(id string) glinet.PortForward {
	return glinet.PortForward{
		ID:       id,
		Name:     m.Name.ValueString(),
		Proto:    m.Protocol.ValueString(),
		Src:      m.ExternalZone.ValueString(),
		SrcDport: m.ExternalPort.ValueString(),
		Dest:     m.InternalZone.ValueString(),
		DestIP:   m.InternalIP.ValueString(),
		DestPort: m.InternalPort.ValueString(),
		Enabled:  m.Enabled.ValueBool(),
	}
}

func portForwardModel(f *glinet.PortForward) *firewallPortForwardResourceModel {
	name := types.StringNull()
	if f.Name != "" {
		name = types.StringValue(f.Name)
	}

	return &firewallPortForwardResourceModel{
		Protocol:     types.StringValue(f.Proto),
		ExternalZone: types.StringValue(f.Src),
		ExternalPort: types.StringValue(f.SrcDport),
		InternalZone: types.StringValue(f.Dest),
		InternalIP:   types.StringValue(f.DestIP),
		InternalPort: types.StringValue(f.DestPort),
		Name:         name,
		Enabled:      types.BoolValue(f.Enabled),
	}
}
