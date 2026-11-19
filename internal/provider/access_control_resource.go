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

const accessControlID = "access_control"

// accessControlModes maps the mode names of the schema to the modes of the router API.
var accessControlModes = map[string]string{
	"block_list": glinet.AccessControlBlockList,
	"allow_list": glinet.AccessControlAllowList,
}

// accessControlModeName returns the schema name of a router mode, or the router mode when it has no name.
func accessControlModeName(mode string) string {
	for name, m := range accessControlModes {
		if m == mode {
			return name
		}
	}
	return mode
}

// accessControlList returns the MAC address list of mode.
func accessControlList(config *glinet.AccessControlConfig, mode string) []string {
	if mode == glinet.AccessControlAllowList {
		return config.WhiteMAC
	}
	return config.BlackMAC
}

var (
	_ resource.Resource                = &accessControlResource{}
	_ resource.ResourceWithConfigure   = &accessControlResource{}
	_ resource.ResourceWithImportState = &accessControlResource{}
)

func NewAccessControlResource() resource.Resource {
	return &accessControlResource{}
}

type accessControlResource struct {
	client *glinet.Client
}

type accessControlResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Mode types.String `tfsdk:"mode"`
}

func (r *accessControlResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_control"
}

func (r *accessControlResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets the access control mode of the router (`black_white_list`). " +
			"The mode selects the MAC address list that the router uses: the block list (`glinet_client_block`) or the allow list (`glinet_client_allow`). " +
			"The router has one access control configuration, so use one instance of this resource.\n\n" +
			"~> **Warning:** in allow-list mode, the router blocks each client that is not in the allow list, " +
			"also the computer that runs Terraform. Read the description of `mode` before you set `allow_list`.\n\n" +
			"A mode change sends the new mode and the current list of that mode on the router, as the web UI does, so the entries of the list do not change. " +
			"The provider sends the list as the router gives it. " +
			"A change to allow-list mode fails when the allow list of the router is empty. " +
			"When the mode on the router is the configured mode, the provider does not write. " +
			"It is not known what a mode change does to the list of the other mode. " +
			"The mode change is from the web UI code of firmware 4.8.3 and was not tested on a router. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + accessControlID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				Description: "Access control mode: `block_list` (`black` in the router API) blocks the clients in the block list; " +
					"`allow_list` (`white`) blocks each client that is not in the allow list, also the computer that runs Terraform. " +
					"Then Terraform and the web UI can lose the access to the router. " +
					"Before you set `allow_list`, make sure that the allow list of the router has the MAC address of each client that must keep its access. " +
					"`glinet_client_allow` writes only in allow-list mode, so add the first entries in the web UI. " +
					"The web UI does not let a user save an allow list without the MAC address of the client that shows the web UI; " +
					"the provider cannot do this check, and only refuses an empty allow list.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf("block_list", "allow_list"),
				},
			},
		},
	}
}

func (r *accessControlResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *accessControlResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *accessControlResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *accessControlResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *accessControlResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *accessControlResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != accessControlID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_access_control must be %q, got %q.", accessControlID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), accessControlID)...)
}

// set changes the mode with black_white_list set_config and sends the current list of the new mode,
// as the access control dialog of the web UI does, so that the write does not change the entries of the list.
func (r *accessControlResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model accessControlResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}
	mode := accessControlModes[model.Mode.ValueString()]

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Access Control Configuration", err.Error())
		return
	}

	if config.Mode != mode {
		macs := accessControlList(config, mode)
		if mode == glinet.AccessControlAllowList && len(macs) == 0 {
			diags.AddAttributeError(
				path.Root("mode"),
				"Allow List Is Empty",
				"The allow list of the router is empty. In allow-list mode, the router blocks each client that is not in the allow list, "+
					"so an empty allow list can block all clients, also the computer that runs Terraform. "+
					"Add the MAC address of each client that must keep its access to the allow list in the web UI first.",
			)
			return
		}

		if err := r.client.AccessControl.SetConfig(ctx, mode, macs); err != nil {
			diags.AddError("Unable to Set Access Control Mode", err.Error())
			return
		}
	}

	r.read(ctx, state, diags)
}

func (r *accessControlResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Access Control Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &accessControlResourceModel{
		ID:   types.StringValue(accessControlID),
		Mode: types.StringValue(accessControlModeName(config.Mode)),
	})...)
}
