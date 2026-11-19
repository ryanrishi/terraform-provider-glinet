package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

var (
	_ resource.Resource                     = &wifiInterfaceResource{}
	_ resource.ResourceWithConfigure        = &wifiInterfaceResource{}
	_ resource.ResourceWithImportState      = &wifiInterfaceResource{}
	_ resource.ResourceWithConfigValidators = &wifiInterfaceResource{}
)

var (
	wifiEncryptions = []string{"none", "psk2", "psk-mixed", "sae", "sae-mixed"}

	// The Wi-Fi key rules of the web UI: 8-63 printable ASCII characters or 64 hex digits.
	asciiWifiKey = regexp.MustCompile(`^[\x20-\x7E]{8,63}$`)
	hexWifiKey   = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)
)

func NewWifiInterfaceResource() resource.Resource {
	return &wifiInterfaceResource{}
}

type wifiInterfaceResource struct {
	client *glinet.Client
}

type wifiInterfaceResourceModel struct {
	Name       types.String `tfsdk:"name"`
	SSID       types.String `tfsdk:"ssid"`
	Key        types.String `tfsdk:"key"`
	Encryption types.String `tfsdk:"encryption"`
	Hidden     types.Bool   `tfsdk:"hidden"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	Band       types.String `tfsdk:"band"`
	Guest      types.Bool   `tfsdk:"guest"`
	Radio      types.String `tfsdk:"radio"`
}

func validWifiKey(s string) bool {
	return asciiWifiKey.MatchString(s) || hexWifiKey.MatchString(s)
}

func (r *wifiInterfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_interface"
}

func (r *wifiInterfaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Wi-Fi interface (a Wi-Fi network) of the router, for example the main 5 GHz network `default_radio0` or the guest network `guest2g` (`wifi.set_config` in the router API). " +
			"Use one instance of this resource for each interface. The interface always exists on the router, so create takes over the interface. " +
			"The web UI writes the switch (`enabled`) and the other attributes in two calls. " +
			"The provider sends a call only when one of its values differs: on create from the values of the router, on update from the state. " +
			"A create whose values are equal to those of the router sends no write. " +
			"When it disables an interface, the provider sends the switch first; else it sends the switch last. " +
			"This resource does not manage the radio (channel, channel width, mode, transmit power); `glinet_wifi_radio` does. " +
			"For a main interface, the web UI also sends `device`, `hwmode`, `txpower`, `random_bssid` and, for a 6 GHz radio, `chan_6g_only_psc` of the radio, so the provider reads them from the router just before the write and sends them back unchanged. " +
			"It does not send `channel` and `htmode`, as the web UI does while the repeater uses the radio. " +
			"**Danger:** a write can restart the Wi-Fi of the radio, which disconnects its clients; it is not known whether it also restarts the other radio. " +
			"When the uplink of the router is a Wi-Fi repeater on the same radio, a write can cut the uplink. " +
			"A write to the LAN configuration (`glinet_lan`) restarted the Wi-Fi of a GL-AXT1800 with firmware 4.7.0 and cut its repeater uplink until a reboot; " +
			"a Wi-Fi write can probably do the same. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "Name of the interface on the router, for example `default_radio0`, `guest5g`, `default_radio1` or `guest2g` (`iface_name` in the router API). " +
					"Changing it replaces the resource.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"ssid": schema.StringAttribute{
				Description: "Name of the Wi-Fi network, 1 to 32 bytes (`ssid` in the router API).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 32),
				},
			},
			"key": schema.StringAttribute{
				Description: "Wi-Fi password (`key` in the router API): 8 to 63 printable ASCII characters, or 64 hex digits; at most 63 characters for `sae` and `sae-mixed`. " +
					"Required unless `encryption` is `none`, and cannot be set then.",
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					stringCheck{description: "must be 8 to 63 printable ASCII characters or 64 hex digits", valid: validWifiKey, sensitive: true},
				},
			},
			"encryption": schema.StringAttribute{
				Description: "Encryption (`encryption` in the router API): `none` (open), `psk2` (WPA2-PSK), `psk-mixed` (WPA/WPA2-PSK), `sae` (WPA3-SAE) or `sae-mixed` (WPA2-PSK/WPA3-SAE). " +
					"The router lists the encryptions that each radio supports; create and update fail when the radio does not support the value.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf(wifiEncryptions...),
				},
			},
			"hidden": schema.BoolAttribute{
				Description: "Whether the network name is hidden (`hidden` in the router API). Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the interface is enabled (`enabled` in the router API).",
				Required:    true,
			},
			"band": schema.StringAttribute{
				Description: "Band of the radio, for example `5G` or `2G` (`band` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"guest": schema.BoolAttribute{
				Description: "Whether the interface is a guest network (`guest` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"radio": schema.StringAttribute{
				Description: "Device name of the radio, for example `radio0` (`device` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *wifiInterfaceResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "key is required unless encryption is none, and has at most 63 characters for sae and sae-mixed",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config wifiInterfaceResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.Encryption.IsNull() || config.Encryption.IsUnknown() {
				return
			}

			encryption := config.Encryption.ValueString()
			if encryption == "none" {
				forbid(diags, "key", config.Key, "when encryption is none")
				return
			}
			require(diags, "key", config.Key, "when encryption is "+encryption)
			if strings.HasPrefix(encryption, "sae") && !config.Key.IsUnknown() && len(config.Key.ValueString()) > 63 {
				diags.AddAttributeError(path.Root("key"), invalidCombination, "key must have at most 63 characters when encryption is "+encryption+".")
			}
		},
	}}
}

func (r *wifiInterfaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wifiInterfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wifiInterfaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, nil, &resp.State, &resp.Diagnostics)
}

func (r *wifiInterfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wifiInterfaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.Wifi.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio, iface := config.FindIface(state.Name.ValueString())
	if iface == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wifiInterfaceModel(radio, iface))...)
}

func (r *wifiInterfaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wifiInterfaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, &state, &resp.State, &resp.Diagnostics)
}

func (r *wifiInterfaceResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *wifiInterfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// write sends the settings call and the switch call of plan, each one only when one of its attributes differs
// from prior, or on create (prior is nil) from the router values. It holds routerWrites from the read of the
// radio values to the last write, so that the write of a main interface sends back the radio values that the
// router has.
func (r *wifiInterfaceResource) write(ctx context.Context, plan wifiInterfaceResourceModel, prior *wifiInterfaceResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	name := plan.Name.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.Wifi.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio, iface := config.FindIface(name)
	if iface == nil {
		diags.AddAttributeError(path.Root("name"), "Wi-Fi Interface Not Found",
			fmt.Sprintf("The router has no Wi-Fi interface %q. The interfaces of the router are: %s.", name, strings.Join(ifaceNames(config), ", ")))
		return
	}
	if encryption := plan.Encryption.ValueString(); !slices.Contains(radio.Encryptions, encryption) {
		diags.AddAttributeError(path.Root("encryption"), "Encryption Not Supported",
			fmt.Sprintf("The radio %s of the Wi-Fi interface %q does not support the encryption %q. It supports: %s.",
				radio.Device, name, encryption, strings.Join(radio.Encryptions, ", ")))
		return
	}

	compared := prior
	if compared == nil {
		compared = wifiInterfaceModel(radio, iface)
	}
	settings := !plan.SSID.Equal(compared.SSID) || !plan.Key.Equal(compared.Key) || !plan.Encryption.Equal(compared.Encryption) || !plan.Hidden.Equal(compared.Hidden)
	enable := !plan.Enabled.Equal(compared.Enabled)

	request := glinet.SetWifiConfigRequest{
		IfaceName:  name,
		SSID:       plan.SSID.ValueString(),
		Encryption: plan.Encryption.ValueString(),
		Hidden:     plan.Hidden.ValueBoolPointer(),
		Key:        plan.Key.ValueString(),
	}
	if !iface.Guest {
		request.Device = radio.Device
		request.HWMode = radio.HWMode
		request.TxPower = radio.TxPower
		request.RandomBSSID = &radio.RandomBSSID
		request.Chan6GOnlyPSC = radio.Chan6GOnlyPSC
	}

	calls := []func() error{}
	if settings {
		calls = append(calls, func() error { return r.client.Wifi.SetConfig(ctx, request) })
	}
	if enable {
		switchCall := func() error {
			return r.client.Wifi.SetConfig(ctx, glinet.SetWifiConfigRequest{IfaceName: name, Enabled: plan.Enabled.ValueBoolPointer()})
		}
		if plan.Enabled.ValueBool() {
			calls = append(calls, switchCall)
		} else {
			calls = append([]func() error{switchCall}, calls...)
		}
	}
	for _, call := range calls {
		if err := call(); err != nil {
			diags.AddError("Unable to Set Wi-Fi Interface", err.Error())
			return
		}
	}

	plan.Band = types.StringValue(radio.Band)
	plan.Guest = types.BoolValue(iface.Guest)
	plan.Radio = types.StringValue(radio.Device)
	diags.Append(state.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}

	config, err = r.client.Wifi.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio, iface = config.FindIface(name)
	if iface == nil {
		diags.AddError("Wi-Fi Interface Not Found", fmt.Sprintf("The router has no Wi-Fi interface %q after the write.", name))
		return
	}

	diags.Append(state.Set(ctx, wifiInterfaceModel(radio, iface))...)
}

func ifaceNames(config *glinet.WifiConfig) []string {
	var names []string
	for _, radio := range config.Radios {
		for _, iface := range radio.Ifaces {
			names = append(names, iface.Name)
		}
	}
	return names
}

// wifiInterfaceModel returns the model of iface. The key is null for an open network, because the router can keep the
// previous key.
func wifiInterfaceModel(radio *glinet.WifiRadio, iface *glinet.WifiIface) *wifiInterfaceResourceModel {
	model := &wifiInterfaceResourceModel{
		Name:       types.StringValue(iface.Name),
		SSID:       types.StringValue(iface.SSID),
		Key:        stringOrNull(iface.Key),
		Encryption: types.StringValue(iface.Encryption),
		Hidden:     types.BoolValue(iface.Hidden),
		Enabled:    types.BoolValue(iface.Enabled),
		Band:       types.StringValue(radio.Band),
		Guest:      types.BoolValue(iface.Guest),
		Radio:      types.StringValue(radio.Device),
	}
	if iface.Encryption == "none" {
		model.Key = types.StringNull()
	}
	return model
}
