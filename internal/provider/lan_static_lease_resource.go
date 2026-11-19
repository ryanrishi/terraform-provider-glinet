package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var macAddress = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)

var (
	_ resource.Resource                = &lanStaticLeaseResource{}
	_ resource.ResourceWithConfigure   = &lanStaticLeaseResource{}
	_ resource.ResourceWithImportState = &lanStaticLeaseResource{}
)

func NewLANStaticLeaseResource() resource.Resource {
	return &lanStaticLeaseResource{}
}

type lanStaticLeaseResource struct {
	client *glinet.Client
}

type lanStaticLeaseResourceModel struct {
	MAC  caseInsensitiveString `tfsdk:"mac"`
	IP   types.String          `tfsdk:"ip"`
	Name types.String          `tfsdk:"name"`
}

func (r *lanStaticLeaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_static_lease"
}

func (r *lanStaticLeaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a DHCP static IP binding of the LAN. " +
			"The router keeps one binding for each MAC address and one for each IP address, " +
			"so create and update fail when another binding has the same MAC address or IP address. " +
			"The router keeps the MAC address in the letter case of the write. " +
			"The provider finds the binding without letter case. When the router has the MAC address in another letter case, the provider does not show a difference. " +
			"Import sets `mac` in the letter case of the router. " +
			"When the router has a binding for more than one letter case of the MAC address, `mac` must be exactly the MAC address of one of them, " +
			"and update fails when `mac` would select another binding. " +
			"Destroy removes the binding from the router.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Description: "MAC address of the client, in lower case or upper case, for example `02:00:00:00:00:01`. " +
					"A change of only the letter case changes only the state. Another change replaces the binding.",
				Required:   true,
				CustomType: caseInsensitiveStringType{},
				PlanModifiers: []planmodifier.String{
					requiresReplaceIfNotLetterCase(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(macAddress, "must be a MAC address, for example 02:00:00:00:00:01"),
				},
			},
			"ip": schema.StringAttribute{
				Description: "IPv4 address that the router gives to the client.",
				Required:    true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 address", valid: validIPv4},
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the binding.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
	}
}

func (r *lanStaticLeaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *lanStaticLeaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lanStaticLeaseResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Leases", err.Error())
		return
	}
	b, err := findBind(binds, plan.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("mac"), "Unable to Find LAN Static Lease", err.Error())
		return
	}
	if b != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"LAN Static Lease Already Exists",
			fmt.Sprintf("The router already has a static lease for MAC address %s (IP address %s). "+
				"Import it with the ID %q, or remove it from the router.", b.MAC, b.IP, b.MAC),
		)
		return
	}
	checkIPFree(binds, plan.IP.ValueString(), "", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.LAN.AddStaticBind(ctx, staticBind(plan)); err != nil {
		resp.Diagnostics.AddError("Unable to Create LAN Static Lease", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, plan.MAC.ValueString(), &resp.State, &resp.Diagnostics)
}

func (r *lanStaticLeaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lanStaticLeaseResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bind, err := r.find(ctx, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Lease", err.Error())
		return
	}
	if bind == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, leaseModel(bind))...)
}

// Update writes to the router binding of the state. It fails before the write when the planned MAC address
// would find another router binding, so that a change of the letter case cannot change the binding of the resource.
func (r *lanStaticLeaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lanStaticLeaseResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Leases", err.Error())
		return
	}

	current, err := findBind(binds, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("mac"), "Unable to Find LAN Static Lease", err.Error())
		return
	}
	if current == nil {
		resp.Diagnostics.AddError("LAN Static Lease Not Found", "The router has no static lease for MAC address "+state.MAC.ValueString()+".")
		return
	}
	planned, err := findBind(binds, plan.MAC.ValueString())
	if err != nil || planned == nil || planned.MAC != current.MAC {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"Ambiguous Letter Case of MAC Address",
			fmt.Sprintf("The router has more than one static lease for MAC address %s in different letter case. "+
				"Set mac to exactly %q, the MAC address of the static lease of this resource.", plan.MAC.ValueString(), current.MAC),
		)
		return
	}

	checkIPFree(binds, plan.IP.ValueString(), current.MAC, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	bind := staticBind(plan)
	bind.MAC = current.MAC
	if err := r.client.LAN.SetStaticBind(ctx, bind); err != nil {
		resp.Diagnostics.AddError("Unable to Update LAN Static Lease", err.Error())
		return
	}

	r.readBack(ctx, plan.MAC.ValueString(), &resp.State, &resp.Diagnostics)
}

func (r *lanStaticLeaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lanStaticLeaseResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bind, err := r.find(ctx, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Lease", err.Error())
		return
	}
	if bind == nil {
		return
	}

	if err := r.client.LAN.RemoveStaticBind(ctx, bind.MAC); err != nil {
		resp.Diagnostics.AddError("Unable to Delete LAN Static Lease", err.Error())
	}
}

// ImportState sets mac in the letter case of the router, so that a configuration in that letter case has no difference.
func (r *lanStaticLeaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !macAddress.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("The import ID must be a MAC address, for example 02:00:00:00:00:01, got %q.", req.ID))
		return
	}

	mac := req.ID
	bind, err := r.find(ctx, mac)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Lease", err.Error())
		return
	}
	if bind != nil {
		mac = bind.MAC
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), mac)...)
}

func (r *lanStaticLeaseResource) find(ctx context.Context, mac string) (*glinet.StaticBind, error) {
	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		return nil, err
	}
	return findBind(binds, mac)
}

// findBind returns the binding with exactly the MAC address mac, else the one binding with mac in another letter case.
// The router compares MAC addresses with letter case, so it can have a binding for each letter case.
// When no binding has exactly mac and more than one binding has mac in another letter case, findBind returns an error.
func findBind(binds []glinet.StaticBind, mac string) (*glinet.StaticBind, error) {
	var matches []glinet.StaticBind
	for _, b := range binds {
		if b.MAC == mac {
			return &b, nil
		}
		if strings.EqualFold(b.MAC, mac) {
			matches = append(matches, b)
		}
	}

	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	}
	var macs []string
	for _, b := range matches {
		macs = append(macs, b.MAC)
	}
	return nil, fmt.Errorf("the router has more than one static lease for MAC address %s in different letter case (%s); "+
		"set mac to exactly one of them", mac, strings.Join(macs, ", "))
}

func (r *lanStaticLeaseResource) readBack(ctx context.Context, mac string, state *tfsdk.State, diags *diag.Diagnostics) {
	bind, err := r.find(ctx, mac)
	if err != nil {
		diags.AddError("Unable to Read LAN Static Lease", err.Error())
		return
	}
	if bind == nil {
		diags.AddError("LAN Static Lease Not Found", "The router has no static lease for MAC address "+mac+" after the write.")
		return
	}

	diags.Append(state.Set(ctx, leaseModel(bind))...)
}

// checkIPFree adds an error when a binding other than the binding with exactly the MAC address ownMAC has the IP address ip.
// The router would remove that binding without an error.
func checkIPFree(binds []glinet.StaticBind, ip, ownMAC string, diags *diag.Diagnostics) {
	for _, b := range binds {
		if b.IP == ip && b.MAC != ownMAC {
			diags.AddAttributeError(
				path.Root("ip"),
				"IP Address Already Has a LAN Static Lease",
				fmt.Sprintf("The router has a static lease for IP address %s with MAC address %s. "+
					"The router would remove that lease. Use another IP address, or remove that lease first.", b.IP, b.MAC),
			)
			return
		}
	}
}

func staticBind(m lanStaticLeaseResourceModel) glinet.StaticBind {
	return glinet.StaticBind{
		MAC:  m.MAC.ValueString(),
		IP:   m.IP.ValueString(),
		Name: m.Name.ValueString(),
	}
}

func leaseModel(b *glinet.StaticBind) *lanStaticLeaseResourceModel {
	name := types.StringNull()
	if b.Name != "" {
		name = types.StringValue(b.Name)
	}

	return &lanStaticLeaseResourceModel{
		MAC:  caseInsensitiveStringValue(b.MAC),
		IP:   types.StringValue(b.IP),
		Name: name,
	}
}
