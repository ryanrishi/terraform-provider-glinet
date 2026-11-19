package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const adGuardHomeID = "adguardhome"

var (
	_ resource.Resource                = &adGuardHomeResource{}
	_ resource.ResourceWithConfigure   = &adGuardHomeResource{}
	_ resource.ResourceWithImportState = &adGuardHomeResource{}
)

func NewAdGuardHomeResource() resource.Resource {
	return &adGuardHomeResource{}
}

type adGuardHomeResource struct {
	client *glinet.Client
}

type adGuardHomeResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	DNSEnabled types.Bool   `tfsdk:"dns_enabled"`
}

func (r *adGuardHomeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_adguardhome"
}

func (r *adGuardHomeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages AdGuard Home on the router. The router has one AdGuard Home configuration, so use one instance of this resource. " +
			"The router accepts a write at once and reports the new values at once, but the AdGuard Home service starts some seconds later. " +
			"After AdGuard Home is enabled, the DNS server of the router can stop answering for up to about 16 seconds. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + adGuardHomeID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether AdGuard Home runs (`enabled` in the router API).",
				Required:    true,
			},
			"dns_enabled": schema.BoolAttribute{
				Description: "Whether AdGuard Home handles the DNS requests of the clients (`dns_enabled` in the router API). " +
					"It can be changed while AdGuard Home is disabled.",
				Required: true,
			},
		},
	}
}

func (r *adGuardHomeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *adGuardHomeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *adGuardHomeResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *adGuardHomeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *adGuardHomeResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *adGuardHomeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != adGuardHomeID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_adguardhome must be %q, got %q.", adGuardHomeID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), adGuardHomeID)...)
}

func (r *adGuardHomeResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model adGuardHomeResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	err := r.client.AdGuardHome.SetConfig(ctx, glinet.AdGuardHomeConfig{
		Enabled:    model.Enabled.ValueBool(),
		DNSEnabled: model.DNSEnabled.ValueBool(),
	})
	if err != nil {
		diags.AddError("Unable to Set AdGuard Home Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(adGuardHomeID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *adGuardHomeResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.AdGuardHome.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read AdGuard Home Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &adGuardHomeResourceModel{
		ID:         types.StringValue(adGuardHomeID),
		Enabled:    types.BoolValue(config.Enabled),
		DNSEnabled: types.BoolValue(config.DNSEnabled),
	})...)
}
