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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var lowerCaseMAC = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

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
	MAC  types.String `tfsdk:"mac"`
	IP   types.String `tfsdk:"ip"`
	Name types.String `tfsdk:"name"`
}

func (r *lanStaticLeaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lan_static_lease"
}

func (r *lanStaticLeaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a DHCP static IP binding of the LAN. " +
			"The router keeps one binding for each MAC address and one for each IP address, " +
			"so create and update fail when another binding has the same MAC address or IP address.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Description: "MAC address of the client, in lower case, for example `02:00:00:00:00:01`. Changing it replaces the binding.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(lowerCaseMAC, "must be a MAC address in lower case, for example 02:00:00:00:00:01"),
				},
			},
			"ip": schema.StringAttribute{
				Description: "IPv4 address that the router gives to the client.",
				Required:    true,
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

	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Leases", err.Error())
		return
	}
	for _, b := range binds {
		if strings.EqualFold(b.MAC, plan.MAC.ValueString()) {
			resp.Diagnostics.AddAttributeError(
				path.Root("mac"),
				"LAN Static Lease Already Exists",
				fmt.Sprintf("The router already has a static lease for MAC address %s (IP address %s). "+
					"Import it with the ID %q, or remove it from the router.", b.MAC, b.IP, b.MAC),
			)
			return
		}
	}
	checkIPFree(binds, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.LAN.AddStaticBind(ctx, staticBind(plan)); err != nil {
		resp.Diagnostics.AddError("Unable to Create LAN Static Lease", err.Error())
		return
	}

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

	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read LAN Static Leases", err.Error())
		return
	}
	checkIPFree(binds, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.LAN.SetStaticBind(ctx, staticBind(plan)); err != nil {
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

	if err := r.client.LAN.RemoveStaticBind(ctx, state.MAC.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Delete LAN Static Lease", err.Error())
	}
}

func (r *lanStaticLeaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("mac"), req, resp)
}

// find returns the binding with exactly the MAC address mac, because the router compares MAC addresses with letter case.
func (r *lanStaticLeaseResource) find(ctx context.Context, mac string) (*glinet.StaticBind, error) {
	binds, err := r.client.LAN.ListStaticBinds(ctx)
	if err != nil {
		return nil, err
	}

	for _, b := range binds {
		if b.MAC == mac {
			return &b, nil
		}
	}
	return nil, nil
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
		if b.IP == plan.IP.ValueString() && b.MAC != plan.MAC.ValueString() {
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
		MAC:  types.StringValue(b.MAC),
		IP:   types.StringValue(b.IP),
		Name: name,
	}
}
