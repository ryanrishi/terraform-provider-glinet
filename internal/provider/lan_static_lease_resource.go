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

var (
	lowerCaseMAC = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
	macAddress   = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)
)

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
	if b := findBind(binds, plan.MAC.ValueString()); b != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"LAN Static Lease Already Exists",
			fmt.Sprintf("The router already has a static lease for MAC address %s (IP address %s). "+
				"Import it with the ID %q, or remove it from the router.", b.MAC, b.IP, b.MAC),
		)
		return
	}
	checkIPFree(binds, plan, &resp.Diagnostics)
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

func (r *lanStaticLeaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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
	checkIPFree(binds, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	bind := staticBind(plan)
	if b := findBind(binds, bind.MAC); b != nil {
		bind.MAC = b.MAC
	}
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
	return findBind(binds, mac), nil
}

// findBind returns the binding with exactly the MAC address mac, else the first binding with mac in another letter case.
// The router compares MAC addresses with letter case, so it can have a binding for each letter case.
func findBind(binds []glinet.StaticBind, mac string) *glinet.StaticBind {
	for _, b := range binds {
		if b.MAC == mac {
			return &b
		}
	}
	for _, b := range binds {
		if strings.EqualFold(b.MAC, mac) {
			return &b
		}
	}
	return nil
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

// checkIPFree adds an error when a binding with another MAC address has the IP address of plan.
// The router would remove that binding without an error.
func checkIPFree(binds []glinet.StaticBind, plan lanStaticLeaseResourceModel, diags *diag.Diagnostics) {
	for _, b := range binds {
		if b.IP == plan.IP.ValueString() && !strings.EqualFold(b.MAC, plan.MAC.ValueString()) {
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
