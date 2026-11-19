package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

const wanAccessID = "wan_access"

var (
	_ resource.Resource                = &wanAccessResource{}
	_ resource.ResourceWithConfigure   = &wanAccessResource{}
	_ resource.ResourceWithImportState = &wanAccessResource{}
)

func NewWANAccessResource() resource.Resource {
	return &wanAccessResource{}
}

type wanAccessResource struct {
	client *glinet.Client
}

type wanAccessResourceModel struct {
	ID               types.String `tfsdk:"id"`
	HTTPS            types.Bool   `tfsdk:"https"`
	SSH              types.Bool   `tfsdk:"ssh"`
	Ping             types.Bool   `tfsdk:"ping"`
	WhitelistEnabled types.Bool   `tfsdk:"whitelist_enabled"`
	Whitelist        types.Map    `tfsdk:"whitelist"`
}

func (r *wanAccessResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wan_access"
}

func (r *wanAccessResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the access to the router from the WAN (remote access): the web UI over HTTPS, SSH and ping (`firewall.set_wan_access` in the router API). " +
			"The router has one WAN access configuration, so use one instance of this resource.\n\n" +
			"~> **Warning:** this resource can cut the management of the router over the WAN: " +
			"when `https` or `ssh` is `false`, or when `whitelist_enabled` is `true` and the whitelist does not have your address, " +
			"you cannot get access to the web UI, the API or SSH from the WAN. The access from the LAN does not change. " +
			"When `https` or `ssh` is `true`, the router is open to the WAN on that service; use the whitelist to limit the sources.\n\n" +
			"Each write sends all attributes, as the web UI does. When `whitelist_enabled` or `whitelist` is not set, the provider sends back the router value that it reads just before the write. " +
			"After the write, the web UI reads the firewall rules again: it is not known whether the write adds or changes firewall rules, " +
			"and so changes the positional IDs of the firewall rules and port forwards on the router. " +
			"`glinet_firewall_rule` and `glinet_firewall_port_forward` look up the router ID before each call, so they do not keep an ID in the state. " +
			untestedWriteNote +
			"Destroy disables the access from the WAN: the provider sends `false` for `https`, `ssh`, `ping` and `whitelist_enabled`, and an empty whitelist.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + wanAccessID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"https": schema.BoolAttribute{
				Description: "Whether the web UI and the API of the router accept connections over HTTPS from the WAN (`enable_https` in the router API).",
				Required:    true,
			},
			"ssh": schema.BoolAttribute{
				Description: "Whether the SSH server of the router accepts connections from the WAN (`enable_ssh` in the router API). " +
					"The web UI shows this switch only while SSH is enabled on the LAN (`ssh_enabled` of `glinet_local_access`). The provider does not check this.",
				Required: true,
			},
			"ping": schema.BoolAttribute{
				Description: "Whether the router answers ping from the WAN (`enable_ping` in the router API).",
				Required:    true,
			},
			"whitelist_enabled": schema.BoolAttribute{
				Description: "Whether only the addresses in `whitelist` have the access from the WAN (`enable_whitelist` in the router API). " +
					"The web UI shows this switch only while `https`, `ssh` or `ping` is `true`. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
			"whitelist": schema.MapAttribute{
				Description: "Addresses that have the access from the WAN when `whitelist_enabled` is `true` (`whitelist` in the router API). " +
					"Each key is an IPv4 or IPv6 address without a prefix length (`ipaddr`), and each value is a description of at most 64 characters, or `\"\"` (`name`). " +
					"It is not known whether the router accepts an address with a prefix length. " +
					"The provider sends the addresses in the order of the keys. When the router has an address two times, the provider keeps only one of them in a write. " +
					"When it is not set, the router value does not change. Set it to `{}` to remove all addresses.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.Map{
					mapvalidator.KeysAre(stringCheck{description: "must be an IPv4 or IPv6 address without a prefix length", valid: validIP}),
					mapvalidator.ValueStringsAre(stringvalidator.LengthAtMost(64)),
				},
			},
		},
	}
}

func (r *wanAccessResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wanAccessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanAccessResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *wanAccessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanAccessResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	routerWrites.Lock()
	defer routerWrites.Unlock()

	if err := r.client.Firewall.SetWANAccess(ctx, glinet.WANAccess{}); err != nil {
		resp.Diagnostics.AddError("Unable to Disable WAN Access", err.Error())
	}
}

func (r *wanAccessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != wanAccessID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_wan_access must be %q, got %q.", wanAccessID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), wanAccessID)...)
}

// set writes all fields. An unknown attribute, which is not in the configuration, gets the router value.
// It holds routerWrites, because the write can possibly change the firewall rules of the router.
func (r *wanAccessResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model wanAccessResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	if model.WhitelistEnabled.IsUnknown() || model.Whitelist.IsUnknown() {
		access, err := r.client.Firewall.GetWANAccess(ctx)
		if err != nil {
			diags.AddError("Unable to Read WAN Access", err.Error())
			return
		}
		current := wanAccessModel(access, diags)
		if model.WhitelistEnabled.IsUnknown() {
			model.WhitelistEnabled = current.WhitelistEnabled
		}
		if model.Whitelist.IsUnknown() {
			model.Whitelist = current.Whitelist
		}
	}

	var whitelist map[string]string
	diags.Append(model.Whitelist.ElementsAs(ctx, &whitelist, false)...)
	if diags.HasError() {
		return
	}
	request := glinet.WANAccess{
		EnableHTTPS:     model.HTTPS.ValueBool(),
		EnableSSH:       model.SSH.ValueBool(),
		EnablePing:      model.Ping.ValueBool(),
		EnableWhitelist: model.WhitelistEnabled.ValueBool(),
	}
	for _, ip := range slices.Sorted(maps.Keys(whitelist)) {
		request.Whitelist = append(request.Whitelist, glinet.WANAccessSource{IPAddr: ip, Name: whitelist[ip]})
	}

	if err := r.client.Firewall.SetWANAccess(ctx, request); err != nil {
		diags.AddError("Unable to Set WAN Access", err.Error())
		return
	}

	model.ID = types.StringValue(wanAccessID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *wanAccessResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	access, err := r.client.Firewall.GetWANAccess(ctx)
	if err != nil {
		diags.AddError("Unable to Read WAN Access", err.Error())
		return
	}

	model := wanAccessModel(access, diags)
	if diags.HasError() {
		return
	}
	diags.Append(state.Set(ctx, model)...)
}

func wanAccessModel(access *glinet.WANAccess, diags *diag.Diagnostics) *wanAccessResourceModel {
	sources := make(map[string]attr.Value, len(access.Whitelist))
	for _, s := range access.Whitelist {
		sources[s.IPAddr] = types.StringValue(s.Name)
	}
	whitelist, d := types.MapValue(types.StringType, sources)
	diags.Append(d...)

	return &wanAccessResourceModel{
		ID:               types.StringValue(wanAccessID),
		HTTPS:            types.BoolValue(access.EnableHTTPS),
		SSH:              types.BoolValue(access.EnableSSH),
		Ping:             types.BoolValue(access.EnablePing),
		WhitelistEnabled: types.BoolValue(access.EnableWhitelist),
		Whitelist:        whitelist,
	}
}
