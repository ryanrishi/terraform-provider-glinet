package provider

import (
	"context"
	"fmt"

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

const dnsID = "dns"

var (
	_ resource.Resource                = &dnsResource{}
	_ resource.ResourceWithConfigure   = &dnsResource{}
	_ resource.ResourceWithImportState = &dnsResource{}
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
	ForceDNS         types.Bool   `tfsdk:"force_dns"`
	RebindProtection types.Bool   `tfsdk:"rebind_protection"`
	OverrideVPN      types.Bool   `tfsdk:"override_vpn"`
	Servers          types.List   `tfsdk:"servers"`
}

func (r *dnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns"
}

func (r *dnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the DNS settings of the router. The router has one DNS configuration, so use one instance of this resource. " +
			"The router sets `force_dns`, `rebind_protection` and `override_vpn` to `false` when a write does not have them, so the provider always writes all three. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + dnsID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				Description: "DNS mode (`mode` in the router API). The only value is `auto`: the other modes of the router need settings that this resource does not have. " +
					"When it is not set, the provider does not write the mode, and the router keeps it.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("auto"),
				},
			},
			"force_dns": schema.BoolAttribute{
				Description: "Whether the router makes the clients use its own DNS server (`force_dns` in the router API).",
				Required:    true,
			},
			"rebind_protection": schema.BoolAttribute{
				Description: "Whether DNS rebinding protection is enabled (`rebind_protection` in the router API).",
				Required:    true,
			},
			"override_vpn": schema.BoolAttribute{
				Description: "Whether the DNS settings of the router override the DNS servers of a VPN client (`override_vpn` in the router API). " +
					"A change can change the DNS servers that an active VPN client tunnel uses.",
				Required: true,
			},
			"servers": schema.ListAttribute{
				Description: "Upstream DNS servers that the router uses, each as `<interface> <ip>`, for example `wwan 192.168.4.253` (`server` in the router API). " +
					"In mode `auto` the router learns them from the uplink and the VPN, so they can change at any time. A change does not make a plan diff.",
				ElementType: types.StringType,
				Computed:    true,
			},
		},
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

	err := r.client.DNS.SetConfig(ctx, glinet.SetDNSConfigRequest{
		Mode:             configuredMode.ValueString(),
		ForceDNS:         model.ForceDNS.ValueBool(),
		RebindProtection: model.RebindProtection.ValueBool(),
		OverrideVPN:      model.OverrideVPN.ValueBool(),
	})
	if err != nil {
		diags.AddError("Unable to Set DNS Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(dnsID)
	if model.Mode.IsUnknown() {
		model.Mode = types.StringNull()
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

func (r *dnsResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.DNS.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read DNS Configuration", err.Error())
		return
	}

	servers, d := types.ListValueFrom(ctx, types.StringType, config.Server)
	diags.Append(d...)
	if diags.HasError() {
		return
	}

	diags.Append(state.Set(ctx, &dnsResourceModel{
		ID:               types.StringValue(dnsID),
		Mode:             types.StringValue(config.Mode),
		ForceDNS:         types.BoolValue(config.ForceDNS),
		RebindProtection: types.BoolValue(config.RebindProtection),
		OverrideVPN:      types.BoolValue(config.OverrideVPN),
		Servers:          servers,
	})...)
}
