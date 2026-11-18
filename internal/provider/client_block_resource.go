package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const blockListMode = "black"

var (
	_ resource.Resource                = &clientBlockResource{}
	_ resource.ResourceWithConfigure   = &clientBlockResource{}
	_ resource.ResourceWithImportState = &clientBlockResource{}
)

func NewClientBlockResource() resource.Resource {
	return &clientBlockResource{}
}

type clientBlockResource struct {
	client *glinet.Client
}

type clientBlockResourceModel struct {
	MAC types.String `tfsdk:"mac"`
}

func (r *clientBlockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client_block"
}

func (r *clientBlockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Blocks a client by its MAC address: the MAC address is an entry in the block list of the router (`black_white_list`). " +
			"The router has one list for all clients. " +
			"The resource does not manage the access control mode: it works only when the router is in block-list mode, " +
			"and create and destroy fail when the router is in allow-list mode. " +
			"While the router is not in block-list mode, refresh keeps the resource in the state and shows a warning. " +
			"The provider writes the MAC address in upper case, as the web UI does, and finds entries without letter case. " +
			"The state keeps `mac` in the letter case of the configuration. Import sets `mac` in lower case. " +
			"Destroy removes the MAC address in each letter case. " +
			"It was not verified that the router blocks the traffic of a client in the list.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Description: "MAC address of the client, in lower case or upper case, for example `02:00:00:00:00:01`. " +
					"A change of only the letter case changes only the state. Another change replaces the block.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					requiresReplaceIfNotLetterCase(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(macAddress, "must be a MAC address, for example 02:00:00:00:00:01"),
				},
			},
		},
	}
}

func (r *clientBlockResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *clientBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clientBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mac := plan.MAC.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	entries := r.blockListEntries(ctx, mac, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(entries) > 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"Client Block Already Exists",
			fmt.Sprintf("The block list of the router already has MAC address %s (%s). "+
				"Import it with the ID %q, or remove it from the router.", mac, strings.Join(entries, ", "), mac),
		)
		return
	}

	if err := r.client.AccessControl.AddBlockedMAC(ctx, strings.ToUpper(mac)); err != nil {
		resp.Diagnostics.AddError("Unable to Create Client Block", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)

	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Block", err.Error())
		return
	}
	if len(matchingMACs(config.BlackMAC, mac)) == 0 {
		resp.Diagnostics.AddError("Client Block Not Found", "The block list of the router has no MAC address "+mac+" after the write.")
	}
}

func (r *clientBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clientBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Block", err.Error())
		return
	}
	if config.Mode != blockListMode {
		resp.Diagnostics.AddWarning(
			"Router Is Not in Block-List Mode",
			fmt.Sprintf("The access control mode of the router is %q, not block-list mode, so the block list is not used. "+
				"The block of MAC address %s stays in the state without a check.", config.Mode, state.MAC.ValueString()),
		)
		return
	}

	if len(matchingMACs(config.BlackMAC, state.MAC.ValueString())) == 0 {
		resp.State.RemoveResource(ctx)
	}
}

// Update changes only the letter case of mac. The router finds the entries without letter case, so it needs no write.
func (r *clientBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan clientBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *clientBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clientBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	entries := r.blockListEntries(ctx, state.MAC.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, entry := range entries {
		if err := r.client.AccessControl.RemoveBlockedMAC(ctx, entry); err != nil {
			resp.Diagnostics.AddError("Unable to Delete Client Block", err.Error())
			return
		}
	}
}

// ImportState sets mac in lower case, because the router can have the MAC address in more than one letter case.
func (r *clientBlockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	mac := strings.ToLower(req.ID)
	if !macAddress.MatchString(mac) {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("The import ID must be a MAC address, for example 02:00:00:00:00:01, got %q.", req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), mac)...)
}

// blockListEntries reads the access control configuration and returns the entries of the block list for mac.
// It adds an error when the router is not in block-list mode, because the writes of the client send mode "black"
// and it is not known what they do in another mode.
func (r *clientBlockResource) blockListEntries(ctx context.Context, mac string, diags *diag.Diagnostics) []string {
	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Block List", err.Error())
		return nil
	}
	if config.Mode != blockListMode {
		diags.AddError(
			"Router Is Not in Block-List Mode",
			fmt.Sprintf("The access control mode of the router is %q, not block-list mode (%q). "+
				"This resource manages only the block list and does not write in allow-list mode, "+
				"because a write could change the mode. Set the router to block-list mode, "+
				"or remove the resource from the state with terraform state rm.", config.Mode, blockListMode),
		)
		return nil
	}

	return matchingMACs(config.BlackMAC, mac)
}

// matchingMACs returns the entries of list that are mac in any letter case.
// The router compares with letter case, but the web UI and the MAC addresses of clients do not.
func matchingMACs(list []string, mac string) []string {
	var entries []string
	for _, entry := range list {
		if strings.EqualFold(entry, mac) {
			entries = append(entries, entry)
		}
	}
	return entries
}
