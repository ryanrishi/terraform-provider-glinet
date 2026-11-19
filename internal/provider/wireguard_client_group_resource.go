package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ resource.Resource                = &wireGuardClientGroupResource{}
	_ resource.ResourceWithConfigure   = &wireGuardClientGroupResource{}
	_ resource.ResourceWithImportState = &wireGuardClientGroupResource{}
)

func NewWireGuardClientGroupResource() resource.Resource {
	return &wireGuardClientGroupResource{}
}

type wireGuardClientGroupResource struct {
	client *glinet.Client
}

type wireGuardClientGroupResourceModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (r *wireGuardClientGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_client_group"
}

func (r *wireGuardClientGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a group of WireGuard client configurations (`wg-client.add_group` in the router API). " +
			"The resource manages only groups of the user (Manual in the web UI), not the presets of VPN service providers. " +
			"Add configurations to the group with `glinet_wireguard_client_config`. " +
			"Destroy removes the group from the router. Destroy fails while the group has configurations, " +
			"also configurations that Terraform does not manage, because the router API removes a group with its configurations, " +
			"and the web UI does not offer to remove the group of the running tunnel. " +
			"What the router does when it gets the removal of the group of the running tunnel is not known. " +
			"Import the group with its ID, for example `terraform import glinet_wireguard_client_group.example 1234`. " +
			untestedWriteNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "ID of the group, which the router gives (`group_id` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the group (`group_name` in the router API): 1 to 64 characters, not only spaces, without a `.` at the start and without `~`.",
				Required:    true,
				Validators:  []validator.String{wireGuardNameValidator},
			},
		},
	}
}

func (r *wireGuardClientGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wireGuardClientGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wireGuardClientGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id, err := r.client.WireGuardClient.AddGroup(ctx, plan.Name.ValueString())
	if err != nil {
		addCreateError(&resp.Diagnostics, "Unable to Create WireGuard Client Group", "group", err)
		return
	}

	plan.ID = types.Int64Value(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardClientGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wireGuardClientGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := findWireGuardGroup(ctx, r.client, state.ID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Client Groups", err.Error())
		return
	}
	if group == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if group.Type != glinet.WireGuardClientGroupCustom {
		resp.Diagnostics.AddError("Not a Custom WireGuard Client Group", notCustomGroup(group))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wireGuardClientGroupModel(group))...)
}

func (r *wireGuardClientGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan wireGuardClientGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := plan.ID.ValueInt64()
	if err := r.client.WireGuardClient.RenameGroup(ctx, id, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Update WireGuard Client Group", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	r.readBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *wireGuardClientGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wireGuardClientGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	id := state.ID.ValueInt64()
	group, err := findWireGuardGroup(ctx, r.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Client Groups", err.Error())
		return
	}
	if group == nil {
		return
	}

	peers, err := r.client.WireGuardClient.ListPeers(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read WireGuard Client Configurations", err.Error())
		return
	}
	if len(peers) > 0 {
		names := make([]string, len(peers))
		for i, p := range peers {
			names[i] = fmt.Sprintf("%q (ID %d)", p.Name, p.ID)
		}
		resp.Diagnostics.AddError("WireGuard Client Group Has Configurations",
			fmt.Sprintf("The WireGuard client group %q (ID %d) has the configurations %s. "+
				"The provider does not remove a group with configurations. Remove them first.", group.Name, id, strings.Join(names, ", ")))
		return
	}

	if err := r.client.WireGuardClient.RemoveGroup(ctx, id); err != nil {
		resp.Diagnostics.AddError("Unable to Delete WireGuard Client Group", err.Error())
	}
}

func (r *wireGuardClientGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, ok := parseRouterID(req.ID)
	if !ok {
		invalidImportID(resp, "glinet_wireguard_client_group", "the group ID", "1234", req.ID)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *wireGuardClientGroupResource) readBack(ctx context.Context, id int64, state *tfsdk.State, diags *diag.Diagnostics) {
	group, err := findWireGuardGroup(ctx, r.client, id)
	if err != nil {
		diags.AddError("Unable to Read WireGuard Client Groups", err.Error())
		return
	}
	if group == nil {
		diags.AddError("WireGuard Client Group Not Found", fmt.Sprintf("The router has no WireGuard client group with the ID %d after the write.", id))
		return
	}

	diags.Append(state.Set(ctx, wireGuardClientGroupModel(group))...)
}

// findWireGuardGroup returns the group with the ID id, or nil when the router has no such group.
func findWireGuardGroup(ctx context.Context, client *glinet.Client, id int64) (*glinet.WireGuardClientGroup, error) {
	groups, err := client.WireGuardClient.ListGroups(ctx)
	if err != nil {
		return nil, err
	}

	i := slices.IndexFunc(groups, func(g glinet.WireGuardClientGroup) bool { return g.ID == id })
	if i < 0 {
		return nil, nil
	}
	return &groups[i], nil
}

func notCustomGroup(group *glinet.WireGuardClientGroup) string {
	return fmt.Sprintf("The WireGuard client group %q (ID %d) is not a group of the user (Manual in the web UI). "+
		"The provider manages only groups of the user and their configurations.", group.Name, group.ID)
}

func wireGuardClientGroupModel(group *glinet.WireGuardClientGroup) *wireGuardClientGroupResourceModel {
	return &wireGuardClientGroupResourceModel{
		ID:   types.Int64Value(group.ID),
		Name: types.StringValue(group.Name),
	}
}
