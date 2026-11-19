package provider

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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

const (
	hoursPerDay           = 24
	defaultMACPeriodHours = 7 * hoursPerDay
	maxMACPeriodHours     = 180 * hoursPerDay
)

var (
	_ resource.Resource                     = &wanPortResource{}
	_ resource.ResourceWithConfigure        = &wanPortResource{}
	_ resource.ResourceWithImportState      = &wanPortResource{}
	_ resource.ResourceWithConfigValidators = &wanPortResource{}
)

func NewWANPortResource() resource.Resource {
	return &wanPortResource{}
}

type wanPortResource struct {
	client *glinet.Client
}

type wanPortResourceModel struct {
	ID                types.String          `tfsdk:"id"`
	Name              types.String          `tfsdk:"name"`
	Mode              types.String          `tfsdk:"mode"`
	MACMode           types.String          `tfsdk:"mac_mode"`
	MACAddress        caseInsensitiveString `tfsdk:"mac_address"`
	MACUpdate         types.String          `tfsdk:"mac_update"`
	MACPeriodHours    types.Int64           `tfsdk:"mac_period_hours"`
	DefaultMACAddress types.String          `tfsdk:"default_mac_address"`
	Label             types.String          `tfsdk:"label"`
}

// validUnicastMAC accepts the MAC addresses that the web UI accepts for MAC clone: not 00:00:00:00:00:00, and not
// a multicast address.
func validUnicastMAC(s string) bool {
	return macAddress.MatchString(s) && s != "00:00:00:00:00:00" && strings.ContainsRune("02468aAcCeE", rune(s[1]))
}

func (r *wanPortResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wan_port"
}

func (r *wanPortResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the role and the MAC address of a wired WAN port of the router (`cable.set_port_config` in the router API).\n\n" +
			"~>**Warning:** `mode = \"lan\"` removes the wired uplink of the port, and a change of the MAC address can make the upstream network give the router a new address. " +
			"Then Terraform can lose the access to the router.\n\n" +
			"Use one instance of this resource for each wired WAN port. The port always exists on the router, so create takes over the port. " +
			"Only a router with a second wired WAN port (`software_feature.secondwan` of `system.get_info`) accepts `name = \"secondwan\"`: " +
			"for that port, the provider reads `system.get_info` before each call and stops with an error when the router has no second wired WAN port. " +
			"The protocol settings of the same port (DHCP, static address, PPPoE) are in `glinet_wan`, which uses another router method; the two resources have no attribute in common. " +
			"Each write sends `mode` and, for `mode = \"wan\"`, the MAC settings, as the web UI does. " +
			"Each MAC attribute that is not set keeps the value of the router: the provider sends back the value that it reads from the router just before the write. " +
			"Before a change of the port from `wan` to `lan`, the provider reads `network.check_wan_cable` and stops with an error while the router reports `cable_enabled`, as the web UI does: remove the cable from the port first. " +
			"The web UI waits 3 seconds after the write, or 15 seconds after a change of `mode`, before it reads the ports again; the provider does not wait. " +
			"The read of the ports was seen on a router, but the values of `mac_update` and `mac_period_hours` were not. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Same as `name`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The wired WAN port: `wan` for the first port or `secondwan` for the second port (`name` in the router API). Changing it replaces the resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("wan", "secondwan"),
				},
			},
			"mode": schema.StringAttribute{
				Description: "How the router uses the port: `wan` for an uplink or `lan` for a LAN port (`mode` in the router API). " +
					"`lan` removes the wired uplink of the port.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf("wan", "lan"),
				},
			},
			"mac_mode": schema.StringAttribute{
				Description: "MAC address of the port (`macaddr.mode` in the router API): `default` for the MAC address of the router (`default_mac_address`), " +
					"`clone` for the MAC address in `mac_address`, for example the MAC address of a client, or `random` for a random MAC address. " +
					"Can be set only when `mode` is `wan`. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf("default", "clone", "random"),
				},
			},
			"mac_address": schema.StringAttribute{
				Description: "MAC address of the port, in lower case or upper case (`macaddr.macaddr` in the router API). " +
					"Must not be `00:00:00:00:00:00` or a multicast address (the second hex digit must be even). " +
					"Can be set only when `mac_mode` is `clone` or `random`, and is required for `clone`. " +
					"For `random`, when it is not set, the provider sends back the MAC address of the router when the router already uses a random MAC address, " +
					"and else makes a random locally administered MAC address, as the web UI does. " +
					"With `mac_update` `time` or `reboot`, the router changes the MAC address itself: do not set this attribute then, or each change shows as a difference. " +
					"With `mac_mode` `default`, the provider sends `default_mac_address`, as the web UI does.",
				Optional:   true,
				Computed:   true,
				CustomType: caseInsensitiveStringType{},
				Validators: []validator.String{
					stringCheck{description: "must be a unicast MAC address that is not 00:00:00:00:00:00, for example 02:00:00:00:00:01", valid: validUnicastMAC},
				},
			},
			"mac_update": schema.StringAttribute{
				Description: "When the router changes a random MAC address (`macaddr.update` in the router API): `none` never, `time` each `mac_period_hours`, `reboot` at each reboot. " +
					"Can be set only when `mac_mode` is `random`. When it is not set, the router value does not change, or the provider sends `none` when the router has no value, as the web UI does.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf("none", "time", "reboot"),
				},
			},
			"mac_period_hours": schema.Int64Attribute{
				Description: "Period in hours after which the router changes a random MAC address, for `mac_update = \"time\"` (`macaddr.period` in the router API). " +
					"The web UI offers 1 to 180 days and sends the period in hours, so the value must be a multiple of 24 from 24 to 4320; the attribute uses the unit of the router so that the provider can send back each router value unchanged. " +
					"Can be set only when `mac_mode` is `random`, and not with `mac_update = \"none\"`. The web UI also sends the period with `mac_update = \"reboot\"`; what the router does with it then is not known. " +
					"When it is not set, the router value does not change, or the provider sends 168 (7 days) when the router has no value, as the web UI does.",
				Optional: true,
				Computed: true,
				Validators: []validator.Int64{
					int64validator.Between(hoursPerDay, maxMACPeriodHours),
				},
			},
			"default_mac_address": schema.StringAttribute{
				Description: "MAC address of the router for the port (`macaddr.defmacaddr` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"label": schema.StringAttribute{
				Description: "Label of the port on the case of the router (`silk` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *wanPortResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "the MAC attributes are allowed only for mode wan, mac_address only for mac_mode clone or random and required for clone, " +
			"mac_update and mac_period_hours only for mac_mode random, mac_period_hours not with mac_update none and only a multiple of 24",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config wanPortResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}

			if !config.MACPeriodHours.IsNull() && !config.MACPeriodHours.IsUnknown() && config.MACPeriodHours.ValueInt64()%hoursPerDay != 0 {
				diags.AddAttributeError(path.Root("mac_period_hours"), invalidCombination, "mac_period_hours must be a multiple of 24: the web UI offers whole days.")
			}
			if config.MACUpdate.ValueString() == "none" {
				forbid(diags, "mac_period_hours", config.MACPeriodHours, "when mac_update is none")
			}

			if config.Mode.ValueString() == "lan" {
				forbid(diags, "mac_mode", config.MACMode, "when mode is lan")
				forbid(diags, "mac_address", config.MACAddress, "when mode is lan")
				forbid(diags, "mac_update", config.MACUpdate, "when mode is lan")
				forbid(diags, "mac_period_hours", config.MACPeriodHours, "when mode is lan")
				return
			}
			if config.MACMode.IsUnknown() {
				return
			}

			macMode := config.MACMode.ValueString()
			reason := "when mac_mode is " + macMode
			if config.MACMode.IsNull() {
				reason = "when mac_mode is not set"
			}
			switch macMode {
			case "clone":
				require(diags, "mac_address", config.MACAddress, reason)
			case "random":
			default:
				forbid(diags, "mac_address", config.MACAddress, reason)
			}
			if macMode != "random" {
				forbid(diags, "mac_update", config.MACUpdate, reason)
				forbid(diags, "mac_period_hours", config.MACPeriodHours, reason)
			}
		},
	}}
}

func (r *wanPortResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wanPortResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanPortResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !checkWANPort(ctx, r.client, name.ValueString(), &resp.Diagnostics) {
		return
	}

	ports, err := r.client.Cable.ListPorts(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Ethernet Ports", err.Error())
		return
	}
	port := findCablePort(ports, name.ValueString())
	if port == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wanPortModel(port))...)
}

func (r *wanPortResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wanPortResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *wanPortResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, ok := wanPorts[req.ID]; !ok {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_wan_port must be \"wan\" or \"secondwan\", got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

// set sends the mode of the port and, for mode wan, the MAC settings, as the web UI does. Each MAC attribute that
// is not configured gets the router value, or the default of the web UI when the router has none.
func (r *wanPortResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model wanPortResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}
	name := model.Name.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	if !checkWANPort(ctx, r.client, name, diags) {
		return
	}
	ports, err := r.client.Cable.ListPorts(ctx)
	if err != nil {
		diags.AddError("Unable to Read Ethernet Ports", err.Error())
		return
	}
	port := findCablePort(ports, name)
	if port == nil {
		diags.AddAttributeError(path.Root("name"), "Ethernet Port Not Found",
			fmt.Sprintf("The router has no Ethernet port %q. The ports of the router are: %s.", name, cablePortNames(ports)))
		return
	}

	mode := model.Mode.ValueString()
	if port.Mode == "wan" && mode == "lan" && !r.checkCableRemoved(ctx, name, diags) {
		return
	}

	var mac glinet.CablePortMAC
	if mode == "wan" {
		mac, err = wanPortMAC(model, port)
		if err != nil {
			diags.AddError("Unable to Make Random MAC Address", err.Error())
			return
		}
	}
	if err := r.client.Cable.SetPort(ctx, name, mode, mac); err != nil {
		diags.AddError("Unable to Set WAN Port", err.Error())
		return
	}

	written := *port
	written.Mode = mode
	if mode == "wan" {
		mac.DefaultMACAddr = defaultMAC(port)
		written.MAC = &mac
	}
	diags.Append(state.Set(ctx, wanPortModel(&written))...)
	if diags.HasError() {
		return
	}

	ports, err = r.client.Cable.ListPorts(ctx)
	if err != nil {
		diags.AddError("Unable to Read Ethernet Ports", err.Error())
		return
	}
	port = findCablePort(ports, name)
	if port == nil {
		diags.AddError("Ethernet Port Not Found", fmt.Sprintf("The router has no Ethernet port %q after the write.", name))
		return
	}
	diags.Append(state.Set(ctx, wanPortModel(port))...)
}

// checkCableRemoved adds an error while the router reports the cable of the WAN port as enabled. The web UI does
// not change a WAN port to lan then.
func (r *wanPortResource) checkCableRemoved(ctx context.Context, name string, diags *diag.Diagnostics) bool {
	status, err := r.client.Network.CheckWANCable(ctx, wanPorts[name])
	if err != nil {
		diags.AddError("Unable to Read WAN Cable State", err.Error())
		return false
	}
	if status.CableEnabled {
		diags.AddAttributeError(path.Root("mode"), "Cable Not Removed",
			fmt.Sprintf("The router reports the cable of the WAN port %q as enabled (cable_enabled of network.check_wan_cable). "+
				"The web UI does not change the port to lan then and tells the user to remove the cable. Remove the cable from the port and apply again.", name))
		return false
	}
	return true
}

// wanPortMAC returns the MAC settings to send for mode wan: the values of model, and for an unknown value the
// router value of port, as the web UI does.
func wanPortMAC(model wanPortResourceModel, port *glinet.CablePort) (glinet.CablePortMAC, error) {
	var current glinet.CablePortMAC
	if port.MAC != nil {
		current = *port.MAC
	}
	if current.Mode == "" {
		current.Mode = "default"
	}

	mac := glinet.CablePortMAC{Mode: model.MACMode.ValueString()}
	if model.MACMode.IsUnknown() {
		mac.Mode = current.Mode
	}

	switch mac.Mode {
	case "default":
		mac.MACAddr = defaultMAC(port)
	case "clone":
		mac.MACAddr = model.MACAddress.ValueString()
		if model.MACAddress.IsUnknown() {
			mac.MACAddr = current.MACAddr
		}
	case "random":
		mac.MACAddr = model.MACAddress.ValueString()
		if model.MACAddress.IsUnknown() {
			mac.MACAddr = current.MACAddr
			if current.Mode != "random" {
				random, err := randomMAC()
				if err != nil {
					return mac, err
				}
				mac.MACAddr = random
			}
		}

		mac.Update = model.MACUpdate.ValueString()
		if model.MACUpdate.IsUnknown() {
			mac.Update = current.Update
			if mac.Update == "" {
				mac.Update = "none"
			}
		}
		mac.Period = int(model.MACPeriodHours.ValueInt64())
		if model.MACPeriodHours.IsUnknown() {
			mac.Period = current.Period
			if mac.Period == 0 {
				mac.Period = defaultMACPeriodHours
			}
		}
	}
	return mac, nil
}

// randomMAC returns a random locally administered unicast MAC address in upper case, as the web UI makes it.
func randomMAC() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[0] = b[0]&^0x01 | 0x02
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[0], b[1], b[2], b[3], b[4], b[5]), nil
}

func defaultMAC(port *glinet.CablePort) string {
	if port.MAC == nil {
		return ""
	}
	return port.MAC.DefaultMACAddr
}

func findCablePort(ports []glinet.CablePort, name string) *glinet.CablePort {
	for i := range ports {
		if ports[i].Name == name {
			return &ports[i]
		}
	}
	return nil
}

func cablePortNames(ports []glinet.CablePort) string {
	names := make([]string, len(ports))
	for i, port := range ports {
		names[i] = port.Name
	}
	return strings.Join(names, ", ")
}

func wanPortModel(port *glinet.CablePort) *wanPortResourceModel {
	model := &wanPortResourceModel{
		ID:                types.StringValue(port.Name),
		Name:              types.StringValue(port.Name),
		Mode:              types.StringValue(port.Mode),
		MACMode:           types.StringNull(),
		MACAddress:        caseInsensitiveString{StringValue: types.StringNull()},
		MACUpdate:         types.StringNull(),
		MACPeriodHours:    types.Int64Null(),
		DefaultMACAddress: types.StringNull(),
		Label:             stringOrNull(port.Silk),
	}
	if mac := port.MAC; mac != nil {
		model.MACMode = types.StringValue(mac.Mode)
		if mac.Mode == "" {
			model.MACMode = types.StringValue("default")
		}
		model.MACAddress = caseInsensitiveString{StringValue: stringOrNull(mac.MACAddr)}
		model.MACUpdate = stringOrNull(mac.Update)
		model.MACPeriodHours = int64OrNull(mac.Period)
		model.DefaultMACAddress = stringOrNull(mac.DefaultMACAddr)
	}
	return model
}
