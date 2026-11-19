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

var (
	_ resource.Resource                = &clientListResource{}
	_ resource.ResourceWithConfigure   = &clientListResource{}
	_ resource.ResourceWithImportState = &clientListResource{}
)

// clientList is one MAC address list of the black_white_list module.
type clientList struct {
	// action is the word for an entry in the type name and in the messages, for example "block".
	action string
	// mode is the access control mode that uses the list.
	mode        string
	description string
	add         func(*glinet.AccessControlService, context.Context, string) error
	remove      func(*glinet.AccessControlService, context.Context, string) error
	// deleteOutsideModeAdvice tells the user what to do when destroy fails because the router is not in mode.
	deleteOutsideModeAdvice string
	// keepLastEntry makes destroy fail when it would empty the list while the router uses it.
	keepLastEntry bool
}

func (l clientList) title() string {
	return strings.ToUpper(l.action[:1]) + l.action[1:]
}

func (l clientList) name() string {
	return l.action + " list"
}

func (l clientList) modeTitle() string {
	return l.title() + "-List Mode"
}

// clientListResource manages one MAC address in a list of the black_white_list module.
type clientListResource struct {
	list   clientList
	client *glinet.Client
}

type clientListResourceModel struct {
	MAC types.String `tfsdk:"mac"`
}

func (r *clientListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client_" + r.list.action
}

func (r *clientListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: r.list.description,
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Description: "MAC address of the client, in lower case or upper case, for example `02:00:00:00:00:01`. " +
					"A change of only the letter case changes only the state. Another change replaces the resource.",
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

func (r *clientListResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *clientListResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clientListResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mac := plan.MAC.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	_, entries := r.listEntries(ctx, mac,
		fmt.Sprintf("Set the access control mode to %q first, for example with glinet_access_control.", accessControlModeName(r.list.mode)),
		&resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(entries) > 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"Client "+r.list.title()+" Already Exists",
			fmt.Sprintf("The %s of the router already has MAC address %s (%s). "+
				"Import it with the ID %q, or remove it from the router.", r.list.name(), mac, strings.Join(entries, ", "), mac),
		)
		return
	}

	if err := r.list.add(r.client.AccessControl, ctx, strings.ToUpper(mac)); err != nil {
		resp.Diagnostics.AddError("Unable to Create Client "+r.list.title(), err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)

	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client "+r.list.title(), err.Error())
		return
	}
	if len(matchingMACs(accessControlList(config, r.list.mode), mac)) == 0 {
		resp.Diagnostics.AddError("Client "+r.list.title()+" Not Found", "The "+r.list.name()+" of the router has no MAC address "+mac+" after the write.")
	}
}

func (r *clientListResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clientListResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client "+r.list.title(), err.Error())
		return
	}
	if config.Mode != r.list.mode {
		resp.Diagnostics.AddWarning(
			"Router Is Not in "+r.list.modeTitle(),
			fmt.Sprintf("The access control mode of the router is %q, not %q, so the %s is not used. "+
				"The %s of MAC address %s stays in the state without a check.",
				accessControlModeName(config.Mode), accessControlModeName(r.list.mode), r.list.name(), r.list.action, state.MAC.ValueString()),
		)
		return
	}

	if len(matchingMACs(accessControlList(config, r.list.mode), state.MAC.ValueString())) == 0 {
		resp.State.RemoveResource(ctx)
	}
}

// Update changes only the letter case of mac. The router finds the entries without letter case, so it needs no write.
func (r *clientListResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan clientListResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *clientListResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clientListResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mac := state.MAC.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	list, entries := r.listEntries(ctx, mac, r.list.deleteOutsideModeAdvice, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.list.keepLastEntry && len(entries) > 0 && len(entries) == len(list) {
		resp.Diagnostics.AddError(
			"Last "+r.list.title()+"-List Entry",
			fmt.Sprintf("MAC address %s is the last entry of the %s, and the router is in %q mode. "+
				"An empty %s can block all clients, also the computer that runs Terraform, so the provider does not remove it. "+
				"Keep another entry in the %s, or remove the resource from the configuration and then from the state with terraform state rm: "+
				"the entry then stays on the router and is not managed. "+
				"A change of the access control mode does not help, because this resource removes entries only in %q mode.",
				mac, r.list.name(), accessControlModeName(r.list.mode), r.list.name(), r.list.name(), accessControlModeName(r.list.mode)),
		)
		return
	}
	for _, entry := range entries {
		if err := r.list.remove(r.client.AccessControl, ctx, entry); err != nil {
			resp.Diagnostics.AddError("Unable to Delete Client "+r.list.title(), err.Error())
			return
		}
	}
}

// ImportState sets mac in lower case, because the router can have the MAC address in more than one letter case.
func (r *clientListResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	mac := strings.ToLower(req.ID)
	if !macAddress.MatchString(mac) {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("The import ID must be a MAC address, for example 02:00:00:00:00:01, got %q.", req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), mac)...)
}

// listEntries reads the access control configuration and returns the list of the resource and its entries for mac.
// It adds an error when the router is not in the mode of the list: the web UI writes a single MAC address only
// to the list of the current mode, and it is not known what such a write does in the other mode.
func (r *clientListResource) listEntries(ctx context.Context, mac, advice string, diags *diag.Diagnostics) (list, entries []string) {
	config, err := r.client.AccessControl.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read "+r.list.title()+" List", err.Error())
		return nil, nil
	}
	if config.Mode != r.list.mode {
		diags.AddError(
			"Router Is Not in "+r.list.modeTitle(),
			fmt.Sprintf("The access control mode of the router is %q, not %q. "+
				"This resource writes to the %s only in %q mode, because it is not known what the write does in another mode. %s",
				accessControlModeName(config.Mode), accessControlModeName(r.list.mode), r.list.name(),
				accessControlModeName(r.list.mode), advice),
		)
		return nil, nil
	}

	list = accessControlList(config, r.list.mode)
	return list, matchingMACs(list, mac)
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
