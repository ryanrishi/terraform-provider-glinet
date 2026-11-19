package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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

var (
	// multiWANTrackModes has the track mode names at the index of their router value.
	multiWANTrackModes = []string{"low_data", "normal", "strict"}
	// multiWANTrackProtocols has the track protocol names at the index of their router value.
	multiWANTrackProtocols = []string{"ipv4", "ipv6", "both"}
)

var (
	_ resource.Resource                     = &multiWANInterfaceResource{}
	_ resource.ResourceWithConfigure        = &multiWANInterfaceResource{}
	_ resource.ResourceWithImportState      = &multiWANInterfaceResource{}
	_ resource.ResourceWithConfigValidators = &multiWANInterfaceResource{}
)

func NewMultiWANInterfaceResource() resource.Resource {
	return &multiWANInterfaceResource{}
}

type multiWANInterfaceResource struct {
	client *glinet.Client
}

type multiWANInterfaceResourceModel struct {
	Interface     types.String `tfsdk:"interface"`
	CheckEnabled  types.Bool   `tfsdk:"check_enabled"`
	TrackMode     types.String `tfsdk:"track_mode"`
	TrackProtocol types.String `tfsdk:"track_protocol"`
	TrackIPv4     types.List   `tfsdk:"track_ipv4"`
	TrackIPv6     types.List   `tfsdk:"track_ipv6"`
}

// trackSettings returns the track attributes of m by name.
func (m multiWANInterfaceResourceModel) trackSettings() map[string]attr.Value {
	return map[string]attr.Value{
		"track_mode":     m.TrackMode,
		"track_protocol": m.TrackProtocol,
		"track_ipv4":     m.TrackIPv4,
		"track_ipv6":     m.TrackIPv6,
	}
}

func (r *multiWANInterfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multiwan_interface"
}

func (r *multiWANInterfaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the tracking of a multi-WAN interface of the router (`kmwan.set_interface` in the router API). " +
			"The router finds out whether an interface is online: it sends requests to the track hosts of the interface.\n\n" +
			multiWANWarning +
			"When the track hosts do not answer, the router thinks that the interface is offline and can stop using it. " +
			"Use one instance of this resource for each interface. The interface always exists on the router, so create takes over the interface. " +
			"Each attribute that is not set keeps the value of the router: the provider sends back the value that it reads from the router just before the write. " +
			"The provider writes only when a value differs from the router, so a create whose values are equal to those of the router sends no write. " +
			"It sends the fields as the web UI does: always `interface` and `enable_check`; when `check_enabled` is `true`, also `track_proto`, `track_method` and `track_mode`, " +
			"`track_ipv4` when `track_protocol` is `ipv4` or `both`, and `track_ipv6` when `track_protocol` is `ipv6` or `both`. " +
			"It sends back the `track_method` of the router (the web UI lists only ping, `0`) and, for `track_method` `1`, the `enable_ssl` of the router. " +
			"It is not known whether the router keeps the fields that a write does not send. " +
			"The failover order and the weights are in `glinet_multiwan`. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"interface": schema.StringAttribute{
				Description: "Name of the interface on the router, for example `wan`, `secondwan`, `wwan` (repeater) or `tethering` (`interface` in the router API). " +
					"Changing it replaces the resource.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"check_enabled": schema.BoolAttribute{
				Description: "Whether the router tracks the interface with the track hosts (`enable_check` in the router API). " +
					"When it is `false`, the router uses only the physical state of the interface, and the other attributes cannot be set. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
			"track_mode": schema.StringAttribute{
				Description: "Track mode: `normal`, `low_data` (badge \"passive\" in the web UI) or `strict` (`track_mode` `1`, `0` or `2` in the router API). " +
					"How the modes differ is not known. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf(multiWANTrackModes...),
				},
			},
			"track_protocol": schema.StringAttribute{
				Description: "IP version of the tracking: `ipv4`, `ipv6` or `both` (`track_proto` `0`, `1` or `2` in the router API). " +
					"The web UI lets the user select it only when IPv6 is enabled on the router; the provider does not check this. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf(multiWANTrackProtocols...),
				},
			},
			"track_ipv4": schema.ListAttribute{
				Description: "Two to four IPv4 addresses of the track hosts (`track_ipv4` in the router API), as the web UI requires. " +
					"The router uses them when `track_protocol` is `ipv4` or `both`; they cannot be set when it is `ipv6`. " +
					"Create and update fail without a write when the router would get fewer than two hosts for a protocol that it tracks. " +
					"When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeBetween(2, 4),
					listvalidator.ValueStringsAre(stringCheck{description: "must be an IPv4 address", valid: validIPv4}),
				},
			},
			"track_ipv6": schema.ListAttribute{
				Description: "Two to four IPv6 addresses of the track hosts (`track_ipv6` in the router API), as the web UI requires. " +
					"The router uses them when `track_protocol` is `ipv6` or `both`; they cannot be set when it is `ipv4`. " +
					"When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeBetween(2, 4),
					listvalidator.ValueStringsAre(stringCheck{description: "must be an IPv6 address", valid: validIPv6}),
				},
			},
		},
	}
}

func (r *multiWANInterfaceResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "the track attributes go with check_enabled true, track_ipv4 does not go with track_protocol ipv6, and track_ipv6 does not go with track_protocol ipv4",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config multiWANInterfaceResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if !config.CheckEnabled.IsNull() && !config.CheckEnabled.IsUnknown() && !config.CheckEnabled.ValueBool() {
				for name, value := range config.trackSettings() {
					forbid(diags, name, value, "when check_enabled is false")
				}
				return
			}
			switch config.TrackProtocol.ValueString() {
			case "ipv4":
				forbid(diags, "track_ipv6", config.TrackIPv6, "when track_protocol is ipv4")
			case "ipv6":
				forbid(diags, "track_ipv4", config.TrackIPv4, "when track_protocol is ipv6")
			}
		},
	}}
}

func (r *multiWANInterfaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *multiWANInterfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANInterfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state multiWANInterfaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.MultiWAN.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Multi-WAN Configuration", err.Error())
		return
	}
	iface := findMultiWANInterface(config, state.Interface.ValueString())
	if iface == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, multiWANInterfaceModel(ctx, iface, &resp.Diagnostics))...)
}

func (r *multiWANInterfaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANInterfaceResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *multiWANInterfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("interface"), req, resp)
}

// write sends the tracking of plan when it differs from the router. An unknown attribute, which is not in the
// configuration, gets the router value. It holds routerWrites from the read to the write, so that the write sends
// back the router values.
func (r *multiWANInterfaceResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model multiWANInterfaceResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}
	name := model.Interface.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.MultiWAN.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Multi-WAN Configuration", err.Error())
		return
	}
	iface := findMultiWANInterface(config, name)
	if iface == nil {
		diags.AddAttributeError(path.Root("interface"), "Multi-WAN Interface Not Found",
			fmt.Sprintf("The router has no multi-WAN interface %q. The interfaces of the router are: %s.", name, strings.Join(multiWANInterfaceNames(config), ", ")))
		return
	}

	tracking := plannedTracking(ctx, model, iface.MultiWANTracking, diags)
	if diags.HasError() {
		return
	}
	if !sameTracking(tracking, iface.MultiWANTracking) {
		checkTracking(model, tracking, diags)
		if diags.HasError() {
			return
		}
		if err := r.client.MultiWAN.SetInterface(ctx, tracking); err != nil {
			diags.AddError("Unable to Set Multi-WAN Interface", err.Error())
			return
		}
	}

	config, err = r.client.MultiWAN.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Multi-WAN Configuration", err.Error())
		return
	}
	iface = findMultiWANInterface(config, name)
	if iface == nil {
		diags.AddError("Multi-WAN Interface Not Found", fmt.Sprintf("The router has no multi-WAN interface %q after the write.", name))
		return
	}

	diags.Append(state.Set(ctx, multiWANInterfaceModel(ctx, iface, diags))...)
}

// plannedTracking returns current with the known values of model.
func plannedTracking(ctx context.Context, model multiWANInterfaceResourceModel, current glinet.MultiWANTracking, diags *diag.Diagnostics) glinet.MultiWANTracking {
	tracking := current
	if !model.CheckEnabled.IsUnknown() {
		tracking.EnableCheck = model.CheckEnabled.ValueBool()
	}
	if !model.TrackMode.IsUnknown() {
		tracking.TrackMode = slices.Index(multiWANTrackModes, model.TrackMode.ValueString())
	}
	if !model.TrackProtocol.IsUnknown() {
		tracking.TrackProto = slices.Index(multiWANTrackProtocols, model.TrackProtocol.ValueString())
	}
	if !model.TrackIPv4.IsUnknown() {
		tracking.TrackIPv4 = listStrings(ctx, model.TrackIPv4, diags)
	}
	if !model.TrackIPv6.IsUnknown() {
		tracking.TrackIPv6 = listStrings(ctx, model.TrackIPv6, diags)
	}
	return tracking
}

// checkTracking adds an error when the write of tracking would not send a configured value of model, or would send
// fewer than two hosts for a tracked protocol.
func checkTracking(model multiWANInterfaceResourceModel, tracking glinet.MultiWANTracking, diags *diag.Diagnostics) {
	if !tracking.EnableCheck {
		for name, value := range model.trackSettings() {
			if !value.IsUnknown() {
				diags.AddAttributeError(path.Root(name), invalidCombination,
					fmt.Sprintf("%s cannot be set while check_enabled is false on the router: the write sends the track settings only when check_enabled is true.", name))
			}
		}
		return
	}

	ipv4 := tracking.TrackProto == glinet.MultiWANTrackIPv4 || tracking.TrackProto == glinet.MultiWANTrackBoth
	ipv6 := tracking.TrackProto == glinet.MultiWANTrackIPv6 || tracking.TrackProto == glinet.MultiWANTrackBoth
	checkTrackHosts(diags, "track_ipv4", model.TrackIPv4, tracking.TrackIPv4, ipv4, tracking.TrackProto)
	checkTrackHosts(diags, "track_ipv6", model.TrackIPv6, tracking.TrackIPv6, ipv6, tracking.TrackProto)
}

// checkTrackHosts adds an error when the hosts of attribute are configured but not sent, or are sent with fewer
// than two hosts.
func checkTrackHosts(diags *diag.Diagnostics, attribute string, configured types.List, hosts []string, sent bool, proto int) {
	protocol, _ := enumName(multiWANTrackProtocols, proto)
	switch {
	case !sent && !configured.IsUnknown():
		diags.AddAttributeError(path.Root(attribute), invalidCombination,
			fmt.Sprintf("%s cannot be set while track_protocol is %s on the router: the write does not send it.", attribute, protocol))
	case sent && len(hosts) < 2:
		diags.AddAttributeError(path.Root(attribute), invalidCombination,
			fmt.Sprintf("track_protocol %s needs two to four hosts in %s, and the router has %d. Set %s.", protocol, attribute, len(hosts), attribute))
	}
}

// sameTracking reports whether a and b have the same values in all fields.
func sameTracking(a, b glinet.MultiWANTracking) bool {
	return a.Interface == b.Interface && a.EnableCheck == b.EnableCheck && a.TrackMode == b.TrackMode && a.TrackProto == b.TrackProto &&
		a.TrackMethod == b.TrackMethod && a.EnableSSL == b.EnableSSL && slices.Equal(a.TrackIPv4, b.TrackIPv4) && slices.Equal(a.TrackIPv6, b.TrackIPv6)
}

func findMultiWANInterface(config *glinet.MultiWANConfig, name string) *glinet.MultiWANInterface {
	for i := range config.Interfaces {
		if config.Interfaces[i].Interface == name {
			return &config.Interfaces[i]
		}
	}
	return nil
}

func multiWANInterfaceModel(ctx context.Context, iface *glinet.MultiWANInterface, diags *diag.Diagnostics) *multiWANInterfaceResourceModel {
	mode, ok := enumName(multiWANTrackModes, iface.TrackMode)
	if !ok {
		diags.AddError("Unknown Track Mode", fmt.Sprintf("The multi-WAN interface %q has the track mode %d, which the provider does not know.", iface.Interface, iface.TrackMode))
	}
	protocol, ok := enumName(multiWANTrackProtocols, iface.TrackProto)
	if !ok {
		diags.AddError("Unknown Track Protocol", fmt.Sprintf("The multi-WAN interface %q has the track protocol %d, which the provider does not know.", iface.Interface, iface.TrackProto))
	}
	return &multiWANInterfaceResourceModel{
		Interface:     types.StringValue(iface.Interface),
		CheckEnabled:  types.BoolValue(iface.EnableCheck),
		TrackMode:     types.StringValue(mode),
		TrackProtocol: types.StringValue(protocol),
		TrackIPv4:     stringList(ctx, iface.TrackIPv4, diags),
		TrackIPv6:     stringList(ctx, iface.TrackIPv6, diags),
	}
}
