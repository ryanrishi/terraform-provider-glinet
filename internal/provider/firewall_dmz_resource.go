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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const firewallDMZID = "firewall_dmz"

var (
	_ resource.Resource                = &firewallDMZResource{}
	_ resource.ResourceWithConfigure   = &firewallDMZResource{}
	_ resource.ResourceWithImportState = &firewallDMZResource{}
)

func NewFirewallDMZResource() resource.Resource {
	return &firewallDMZResource{}
}

type firewallDMZResource struct {
	client *glinet.Client
}

type firewallDMZResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	InternalIP     types.String `tfsdk:"internal_ip"`
	HigherPriority types.Bool   `tfsdk:"higher_priority"`
}

func (r *firewallDMZResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_dmz"
}

func (r *firewallDMZResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the DMZ host of the firewall. When the DMZ is enabled, the router forwards incoming traffic from the zone `wan` to the DMZ host. The router has one DMZ configuration, so use one instance of this resource. " +
			"Destroy disables the DMZ on the router and removes the DMZ host, so that no host stays exposed. " +
			"When the router has no DMZ host, Terraform plans to create this resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + firewallDMZID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the DMZ is enabled (`enabled` in the router API).",
				Required:    true,
			},
			"internal_ip": schema.StringAttribute{
				Description: "IPv4 address of the DMZ host (`dest_ip` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringCheck{description: "must be an IPv4 address", valid: validIPv4},
				},
			},
			"higher_priority": schema.BoolAttribute{
				Description: "Whether the DMZ has the higher priority (`priority` in the router API). " +
					"The web UI of firmware 4.8.3 shows `true` as the higher priority and `false` as the lower priority. It shows the DMZ on the port forward page, " +
					"but to what the priority is compared is not known. " +
					"When it is not set, the provider sends back the value of the router, so the router value does not change. " +
					"The provider sends `priority` only when `enabled` is `true`, as the web UI does. While the router reports the DMZ as disabled, " +
					"the provider keeps the value of the state and does not compare it with the router. " +
					"When the provider sends a priority in a write that enables the DMZ and the router then reports another priority or no priority, the apply fails. " +
					"Before the first write, the router reports no priority: the provider reads null, and when `higher_priority` is not set, it sends no `priority`. " +
					"The web UI then shows and sends `true`. Firmware 4.7.0 reports `false` after each write without `priority`. " +
					"The write of `priority` was not tested on a router, and what firmware without this option does with it is not known.",
				Optional: true,
				Computed: true,
			},
		},
	}
}

func (r *firewallDMZResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *firewallDMZResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

// Read removes the resource from the state when the router has no DMZ host, because internal_ip is required.
func (r *firewallDMZResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior firewallDMZResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	dmz, err := r.client.Firewall.GetDMZ(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall DMZ", err.Error())
		return
	}
	if dmz.DestIP == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, dmzModel(dmz, prior.HigherPriority))...)
}

func (r *firewallDMZResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *firewallDMZResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	routerWrites.Lock()
	defer routerWrites.Unlock()

	if err := r.client.Firewall.SetDMZ(ctx, glinet.DMZ{Enabled: false}); err != nil {
		resp.Diagnostics.AddError("Unable to Disable Firewall DMZ", err.Error())
	}
}

func (r *firewallDMZResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != firewallDMZID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_firewall_dmz must be %q, got %q.", firewallDMZID, req.ID),
		)
		return
	}

	dmz, err := r.client.Firewall.GetDMZ(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Firewall DMZ", err.Error())
		return
	}
	if dmz.DestIP == "" {
		resp.Diagnostics.AddError("Firewall DMZ Host Not Found",
			"The router has no DMZ host, so there is nothing to import. Remove the import and run terraform apply to create the DMZ.")
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), firewallDMZID)...)
}

// set writes the DMZ of plan. When higher_priority is not set, it sends back the priority of the router,
// and no priority when the router reports none.
func (r *firewallDMZResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model firewallDMZResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	current, err := r.client.Firewall.GetDMZ(ctx)
	if err != nil {
		diags.AddError("Unable to Read Firewall DMZ", err.Error())
		return
	}

	priority := current.Priority
	if !model.HigherPriority.IsUnknown() {
		priority = model.HigherPriority.ValueBoolPointer()
	}

	err = r.client.Firewall.SetDMZ(ctx, glinet.DMZ{
		Enabled:  model.Enabled.ValueBool(),
		DestIP:   model.InternalIP.ValueString(),
		Priority: priority,
	})
	if err != nil {
		diags.AddError("Unable to Set Firewall DMZ", err.Error())
		return
	}

	model.ID = types.StringValue(firewallDMZID)
	model.HigherPriority = types.BoolPointerValue(priority)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	dmz, err := r.client.Firewall.GetDMZ(ctx)
	if err != nil {
		diags.AddError("Unable to Read Firewall DMZ", err.Error())
		return
	}
	if dmz.DestIP == "" {
		diags.AddError("Firewall DMZ Host Not Found", "The router has no DMZ host after the write.")
		return
	}
	if dmz.Enabled && priority != nil && (dmz.Priority == nil || *dmz.Priority != *priority) {
		reported := "no DMZ priority"
		if dmz.Priority != nil {
			reported = fmt.Sprintf("the DMZ priority %t", *dmz.Priority)
		}
		diags.AddAttributeError(path.Root("higher_priority"), "Firewall DMZ Priority Not Applied",
			fmt.Sprintf("The router reports %s after the write of %t. The firmware of the router may not have the DMZ priority.", reported, *priority))
		return
	}

	diags.Append(state.Set(ctx, dmzModel(dmz, model.HigherPriority))...)
}

// dmzModel takes the priority kept while the router reports the DMZ as disabled, because a disabled write does not send it.
// When kept is null, as after an import, it takes the priority of the router, which is null when the router reports none.
func dmzModel(dmz *glinet.DMZ, kept types.Bool) *firewallDMZResourceModel {
	priority := types.BoolPointerValue(dmz.Priority)
	if !dmz.Enabled && !kept.IsNull() && !kept.IsUnknown() {
		priority = kept
	}

	return &firewallDMZResourceModel{
		ID:             types.StringValue(firewallDMZID),
		Enabled:        types.BoolValue(dmz.Enabled),
		InternalIP:     types.StringValue(dmz.DestIP),
		HigherPriority: priority,
	}
}
