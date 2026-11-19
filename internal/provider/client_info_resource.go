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

var noCommaOrQuote = regexp.MustCompile(`^[^,"]*$`)

var (
	_ resource.Resource                = &clientInfoResource{}
	_ resource.ResourceWithConfigure   = &clientInfoResource{}
	_ resource.ResourceWithImportState = &clientInfoResource{}
)

func NewClientInfoResource() resource.Resource {
	return &clientInfoResource{}
}

type clientInfoResource struct {
	client *glinet.Client
}

type clientInfoResourceModel struct {
	MAC   caseInsensitiveString `tfsdk:"mac"`
	Alias types.String          `tfsdk:"alias"`
	Class types.String          `tfsdk:"class"`
}

func (r *clientInfoResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client_info"
}

func (r *clientInfoResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the name and the icon of a client device in the client list of the router, as the edit dialog of the clients page of the web UI does. " +
			"The write was not tested on a router; the parameters come from the router's web UI. " +
			"Create fails when the client list of the router has no client with the MAC address, " +
			"because it is not known whether the router accepts a name or an icon for a MAC address that is not in the list. " +
			"When the client goes out of the client list, refresh removes the resource from the state. " +
			"The provider finds the client without letter case and writes the MAC address in the letter case of the client list. " +
			"Import sets `mac` in the letter case of the client list. " +
			"Destroy sets the name and the icon of the client back to automatic.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Description: "MAC address of the client, in lower case or upper case, for example `02:00:00:00:00:01`. " +
					"A change of only the letter case changes only the state. Another change replaces the resource.",
				Required:   true,
				CustomType: caseInsensitiveStringType{},
				PlanModifiers: []planmodifier.String{
					requiresReplaceIfNotLetterCase(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(macAddress, "must be a MAC address, for example 02:00:00:00:00:01"),
				},
			},
			"alias": schema.StringAttribute{
				Description: "Name of the client, at most 32 characters, without `,` and `\"`. " +
					"An empty string sets the name back to automatic: the router then shows the name that it found. " +
					"When not set, the provider sends the name that the router has.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtMost(32),
					stringvalidator.RegexMatches(noCommaOrQuote, "must not contain , or \""),
				},
			},
			"class": schema.StringAttribute{
				Description: "Icon of the client. The web UI of firmware 4.8.3 offers `computer`, `phone`, `pad`, `camera`, `watch`, `laptop`, `printer`, " +
					"`sound`, `television`, `smartappliances`, `games`, `gateway`, `nas`, `server` and `switch`. " +
					"The provider does not limit the value to this list, because other firmware versions can have other icons. " +
					"An empty string sets the icon back to automatic. " +
					"When not set, the provider sends the icon that the router has.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(noCommaOrQuote, "must not contain , or \""),
				},
			},
		},
	}
}

func (r *clientInfoResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *clientInfoResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clientInfoResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *clientInfoResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clientInfoResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := readClient(ctx, r.client, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Info", err.Error())
		return
	}
	if c == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, clientInfoModel(c))...)
}

func (r *clientInfoResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan clientInfoResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *clientInfoResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clientInfoResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	c, err := readClient(ctx, r.client, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Info", err.Error())
		return
	}
	if c == nil {
		return
	}

	if err := r.client.Clients.SetInfo(ctx, glinet.ClientInfo{MAC: c.MAC}); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Client Info", err.Error())
	}
}

// ImportState sets mac in the letter case of the client list, so that a configuration in that letter case has no difference.
func (r *clientInfoResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importClientMAC(ctx, r.client, req, resp)
}

// write sends the name and the icon of plan to the client with the MAC address of plan, and sets state from the client list.
// An unknown alias or class sends the value that the router has.
func (r *clientInfoResource) write(ctx context.Context, plan clientInfoResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	c := findClientForWrite(ctx, r.client, plan.MAC.ValueString(), diags)
	if diags.HasError() {
		return
	}

	info := glinet.ClientInfo{MAC: c.MAC, Alias: c.Alias, Class: c.Class}
	if !plan.Alias.IsUnknown() {
		info.Alias = plan.Alias.ValueString()
	}
	if !plan.Class.IsUnknown() {
		info.Class = plan.Class.ValueString()
	}
	if err := r.client.Clients.SetInfo(ctx, info); err != nil {
		diags.AddError("Unable to Write Client Info", err.Error())
		return
	}

	c, err := readClient(ctx, r.client, c.MAC)
	if err != nil {
		diags.AddError("Unable to Read Client Info", err.Error())
		return
	}
	if c == nil {
		diags.AddError("Client Not Found", "The client list of the router has no client with MAC address "+plan.MAC.ValueString()+" after the write.")
		return
	}
	diags.Append(state.Set(ctx, clientInfoModel(c))...)
}

// clientInfoModel gives an empty alias or class for a key that the router does not give, because the router gives
// alias and class only when they are set, and it is not known whether it keeps an empty value after a write of "".
func clientInfoModel(c *glinet.ClientDevice) clientInfoResourceModel {
	return clientInfoResourceModel{
		MAC:   caseInsensitiveStringValue(c.MAC),
		Alias: types.StringValue(c.Alias),
		Class: types.StringValue(c.Class),
	}
}

func readClient(ctx context.Context, client *glinet.Client, mac string) (*glinet.ClientDevice, error) {
	clients, err := client.Clients.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	return findClient(clients, mac)
}

// findClientForWrite returns the client with mac in the client list. It adds an error when the list has no such client,
// because it is not known whether the router accepts a write for a MAC address that is not in the list.
func findClientForWrite(ctx context.Context, client *glinet.Client, mac string, diags *diag.Diagnostics) *glinet.ClientDevice {
	c, err := readClient(ctx, client, mac)
	if err != nil {
		diags.AddAttributeError(path.Root("mac"), "Unable to Read Client List", err.Error())
		return nil
	}
	if c == nil {
		diags.AddAttributeError(
			path.Root("mac"),
			"Client Not Found",
			fmt.Sprintf("The client list of the router has no client with MAC address %s. "+
				"It is not known whether the router accepts a write for a MAC address that is not in the client list, so the provider does not send it. "+
				"Connect the client to the router first.", mac),
		)
	}
	return c
}

// findClient returns the client with exactly the MAC address mac, else the one client with mac in another letter case.
// When no client has exactly mac and more than one client has mac in another letter case, findClient returns an error.
func findClient(clients []glinet.ClientDevice, mac string) (*glinet.ClientDevice, error) {
	var matches []glinet.ClientDevice
	for _, c := range clients {
		if c.MAC == mac {
			return &c, nil
		}
		if strings.EqualFold(c.MAC, mac) {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	}
	var macs []string
	for _, c := range matches {
		macs = append(macs, c.MAC)
	}
	return nil, fmt.Errorf("the client list of the router has more than one client for MAC address %s in different letter case (%s); "+
		"set mac to exactly one of them", mac, strings.Join(macs, ", "))
}

// importClientMAC sets mac in the letter case of the client list. When the list has no such client, the read after the import removes the resource.
func importClientMAC(ctx context.Context, client *glinet.Client, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !macAddress.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("The import ID must be a MAC address, for example 02:00:00:00:00:01, got %q.", req.ID))
		return
	}

	mac := req.ID
	c, err := readClient(ctx, client, mac)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client List", err.Error())
		return
	}
	if c != nil {
		mac = c.MAC
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), mac)...)
}
