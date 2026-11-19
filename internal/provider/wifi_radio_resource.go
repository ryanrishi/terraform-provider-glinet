package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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

var (
	_ resource.Resource                = &wifiRadioResource{}
	_ resource.ResourceWithConfigure   = &wifiRadioResource{}
	_ resource.ResourceWithImportState = &wifiRadioResource{}
)

var (
	wifiTxPowers      = []string{"max", "high", "medium", "low"}
	wifiChannelWidths = []string{"20", "40", "80", "160", "320", "auto"}
)

// routerTxPowers maps each transmit power to its value in the router API.
var routerTxPowers = map[string]string{"max": "Max", "high": "High", "medium": "Medium", "low": "Low"}

// routerTxPower returns the value of the router API for the transmit power p. Another value, which providerTxPower
// gave unchanged from the router, goes back unchanged.
func routerTxPower(p string) string {
	if v, ok := routerTxPowers[p]; ok {
		return v
	}
	return p
}

// providerTxPower returns the transmit power of the router value v, or v when it is not a value of routerTxPowers.
func providerTxPower(v string) string {
	for p, router := range routerTxPowers {
		if router == v {
			return p
		}
	}
	return v
}

func NewWifiRadioResource() resource.Resource {
	return &wifiRadioResource{}
}

type wifiRadioResource struct {
	client *glinet.Client
}

type wifiRadioResourceModel struct {
	Name            types.String `tfsdk:"name"`
	Band            types.String `tfsdk:"band"`
	Channel         types.Int64  `tfsdk:"channel"`
	ChannelWidth    types.String `tfsdk:"channel_width"`
	Mode            types.String `tfsdk:"mode"`
	TxPower         types.String `tfsdk:"tx_power"`
	RandomBSSID     types.Bool   `tfsdk:"random_bssid_enabled"`
	PSCChannelsOnly types.Bool   `tfsdk:"psc_channels_only"`
}

func (r *wifiRadioResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_radio"
}

func (r *wifiRadioResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the settings of a Wi-Fi radio of the router, for example `radio0`: channel, channel width, mode, transmit power, random BSSID and, for a 6 GHz radio, the PSC option. " +
			"Use one instance of this resource for each radio. The radio always exists on the router, so create takes over the radio.\n\n" +
			"~> **Warning:** a write, for example a change of the channel or the channel width, can restart the Wi-Fi of the radio, which disconnects its clients; it is not known whether it also restarts the other radio. " +
			"When the uplink of the router is a Wi-Fi repeater on the same radio, a write can cut the uplink, and a change of the channel or the channel width can make the repeater lose its access point. " +
			"A write to the LAN configuration (`glinet_lan`) restarted the Wi-Fi of a GL-AXT1800 with firmware 4.7.0 and cut its repeater uplink until a reboot; " +
			"a Wi-Fi write can probably do the same.\n\n" +
			"Each attribute that is not set keeps the value of the router: the provider sends back the value that it reads from the router just before the write. " +
			"The router carries the radio settings in the write of the main interface of the radio (`wifi.set_config`), as the web UI does. " +
			"So the provider reads the SSID, the encryption, the hidden flag and the Wi-Fi password of the main interface from the router just before the write and sends them back unchanged; " +
			"it does not manage them (see `glinet_wifi_interface`). " +
			"When only `tx_power` changes, the provider sends `wifi.set_txpower` instead, as the web UI does. " +
			"The provider sends a call only when one of its values differs: on create from the values of the router, on update from the state. " +
			"A create whose values are equal to those of the router sends no write. " +
			"The provider sends `channel` and `channel_width` only when one of them changes: the web UI does not send them while the Wi-Fi repeater uses the radio, and what the router does with them then is not known. " +
			"The provider checks `mode`, `channel_width` and `channel` against the lists of the radio on the router before the write. The web UI also permits only the channels that fit the channel width; the provider does not check this, and what the router does with such a channel is not known. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "Device name of the radio, for example `radio0` or `radio1` (`device` in the router API). Changing it replaces the resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"band": schema.StringAttribute{
				Description: "Band of the radio, for example `5G`, `2G` or `6G` (`band` in the router API).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"channel": schema.Int64Attribute{
				Description: "Channel of the radio, or `0` for automatic (`channel` in the router API). " +
					"The value must be one of the channels that the router lists for the radio. " +
					"A DFS channel is permitted only when the router supports DFS (`dfs_support`), and a 6 GHz radio with `psc_channels_only` permits only PSC channels. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"channel_width": schema.StringAttribute{
				Description: "Channel width in MHz, `20`, `40`, `80`, `160` or `320`, or `auto` (`htmode` in the router API). " +
					"The router lists the largest width for each mode of the radio, and whether the radio supports `auto`; create and update fail when the value is not available for `mode`. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf(wifiChannelWidths...),
				},
			},
			"mode": schema.StringAttribute{
				Description: "Wi-Fi mode of the radio, for example `11a/n/ac/ax` (`hwmode` in the router API). " +
					"The router lists the modes of each radio; create and update fail when the radio does not support the value. " +
					"When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"tx_power": schema.StringAttribute{
				Description: "Transmit power: `max`, `high`, `medium` or `low` (`txpower` in the router API, which has the values `Max`, `High`, `Medium` and `Low`). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(wifiTxPowers...),
				},
			},
			"random_bssid_enabled": schema.BoolAttribute{
				Description: "Whether the radio uses a random BSSID (`random_bssid` in the router API). When it is not set, the router value does not change.",
				Optional:    true,
				Computed:    true,
			},
			"psc_channels_only": schema.BoolAttribute{
				Description: "Whether a 6 GHz radio uses only the preferred scanning channels (PSC) (`chan_6g_only_psc` in the router API). " +
					"Only for a radio with the band `6G`: create and update fail when it is set for another radio, and it is always null there. " +
					"When it is `true`, `channel` must be `0` or a PSC channel; the web UI then sets a channel that is not a PSC channel to `0`, but the provider fails instead. " +
					"The option is in the web UI code of firmware 4.8.3 and was not seen on a router. When it is not set, the router value does not change.",
				Optional: true,
				Computed: true,
			},
		},
	}
}

func (r *wifiRadioResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *wifiRadioResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wifiRadioResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, nil, &resp.State, &resp.Diagnostics)
}

func (r *wifiRadioResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wifiRadioResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.Wifi.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio := findRadio(config, state.Name.ValueString())
	if radio == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, wifiRadioModel(radio))...)
}

func (r *wifiRadioResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wifiRadioResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.write(ctx, plan, &state, &resp.State, &resp.Diagnostics)
}

func (r *wifiRadioResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *wifiRadioResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// write sends the radio settings of plan only when one of them differs from prior, or on create (prior is nil)
// from the router values. An unknown attribute, which is not in the configuration, gets the router value. It holds
// routerWrites from the read to the last write, so that the write sends back the main interface settings that the
// router has.
func (r *wifiRadioResource) write(ctx context.Context, plan wifiRadioResourceModel, prior *wifiRadioResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	name := plan.Name.ValueString()

	routerWrites.Lock()
	defer routerWrites.Unlock()

	config, err := r.client.Wifi.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio := findRadio(config, name)
	if radio == nil {
		diags.AddAttributeError(path.Root("name"), "Wi-Fi Radio Not Found",
			fmt.Sprintf("The router has no Wi-Fi radio %q. The radios of the router are: %s.", name, strings.Join(radioNames(config), ", ")))
		return
	}

	current := wifiRadioModel(radio)
	if plan.Channel.IsUnknown() {
		plan.Channel = current.Channel
	}
	if plan.ChannelWidth.IsUnknown() {
		plan.ChannelWidth = current.ChannelWidth
	}
	if plan.Mode.IsUnknown() {
		plan.Mode = current.Mode
	}
	if plan.TxPower.IsUnknown() {
		plan.TxPower = current.TxPower
	}
	if plan.RandomBSSID.IsUnknown() {
		plan.RandomBSSID = current.RandomBSSID
	}
	if plan.PSCChannelsOnly.IsUnknown() {
		plan.PSCChannelsOnly = current.PSCChannelsOnly
	}

	compared := prior
	if compared == nil {
		compared = current
	}
	channel := !plan.Channel.Equal(compared.Channel) || !plan.ChannelWidth.Equal(compared.ChannelWidth)
	settings := channel || !plan.Mode.Equal(compared.Mode) || !plan.RandomBSSID.Equal(compared.RandomBSSID) || !plan.PSCChannelsOnly.Equal(compared.PSCChannelsOnly)
	txPower := !plan.TxPower.Equal(compared.TxPower)

	checkWifiRadio(plan, *compared, config.DFSSupport, radio, diags)
	if diags.HasError() {
		return
	}

	switch {
	case settings:
		iface := mainIface(radio)
		if iface == nil {
			diags.AddError("Wi-Fi Interface Not Found", fmt.Sprintf("The Wi-Fi radio %q has no main interface, which carries the radio settings.", name))
			return
		}
		request := glinet.SetWifiConfigRequest{
			IfaceName:     iface.Name,
			SSID:          iface.SSID,
			Encryption:    iface.Encryption,
			Hidden:        &iface.Hidden,
			Device:        radio.Device,
			HWMode:        plan.Mode.ValueString(),
			TxPower:       routerTxPower(plan.TxPower.ValueString()),
			RandomBSSID:   plan.RandomBSSID.ValueBoolPointer(),
			Chan6GOnlyPSC: plan.PSCChannelsOnly.ValueBoolPointer(),
		}
		if iface.Encryption != "none" {
			request.Key = iface.Key
		}
		if channel {
			c := int(plan.Channel.ValueInt64())
			request.Channel = &c
			request.HTMode = plan.ChannelWidth.ValueString()
		}
		if err := r.client.Wifi.SetConfig(ctx, request); err != nil {
			diags.AddError("Unable to Set Wi-Fi Radio", err.Error())
			return
		}
	case txPower:
		if err := r.client.Wifi.SetTxPower(ctx, radio.Device, routerTxPower(plan.TxPower.ValueString())); err != nil {
			diags.AddError("Unable to Set Wi-Fi Transmit Power", err.Error())
			return
		}
	}

	plan.Band = types.StringValue(radio.Band)
	diags.Append(state.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}

	config, err = r.client.Wifi.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Wi-Fi Configuration", err.Error())
		return
	}
	radio = findRadio(config, name)
	if radio == nil {
		diags.AddError("Wi-Fi Radio Not Found", fmt.Sprintf("The router has no Wi-Fi radio %q after the write.", name))
		return
	}

	diags.Append(state.Set(ctx, wifiRadioModel(radio))...)
}

// checkWifiRadio checks each value of plan that differs from compared against the lists of the radio, as the web
// UI does. A change of mode or channel width checks the pair, and a change of channel or PSC option checks the
// channel.
func checkWifiRadio(plan, compared wifiRadioResourceModel, dfsSupport bool, radio *glinet.WifiRadio, diags *diag.Diagnostics) {
	is6G := strings.EqualFold(radio.Band, "6G")
	if !is6G && !plan.PSCChannelsOnly.IsNull() {
		diags.AddAttributeError(path.Root("psc_channels_only"), "PSC Option Not Supported",
			fmt.Sprintf("The Wi-Fi radio %s has the band %s; psc_channels_only is only for a radio with the band 6G.", radio.Device, radio.Band))
		return
	}

	mode := plan.Mode.ValueString()
	if !plan.Mode.Equal(compared.Mode) && !slices.Contains(radio.HWModes, mode) {
		diags.AddAttributeError(path.Root("mode"), "Mode Not Supported",
			fmt.Sprintf("The Wi-Fi radio %s does not support the mode %q. It supports: %s.", radio.Device, mode, strings.Join(radio.HWModes, ", ")))
		return
	}

	if !plan.Mode.Equal(compared.Mode) || !plan.ChannelWidth.Equal(compared.ChannelWidth) {
		width := plan.ChannelWidth.ValueString()
		if widths := channelWidths(radio, mode); !slices.Contains(widths, width) {
			diags.AddAttributeError(path.Root("channel_width"), "Channel Width Not Supported",
				fmt.Sprintf("The Wi-Fi radio %s does not support the channel width %q with the mode %q. It supports: %s.",
					radio.Device, width, mode, strings.Join(widths, ", ")))
			return
		}
	}

	if plan.Channel.Equal(compared.Channel) && plan.PSCChannelsOnly.Equal(compared.PSCChannelsOnly) {
		return
	}
	number := int(plan.Channel.ValueInt64())
	if number == 0 {
		return
	}
	i := slices.IndexFunc(radio.Channels, func(c glinet.WifiChannel) bool { return c.Channel == number })
	switch {
	case i < 0:
		diags.AddAttributeError(path.Root("channel"), "Channel Not Supported",
			fmt.Sprintf("The Wi-Fi radio %s does not support the channel %d. It supports 0 (automatic) and: %s.", radio.Device, number, channelNumbers(radio.Channels)))
	case radio.Channels[i].DFS && !dfsSupport:
		diags.AddAttributeError(path.Root("channel"), "Channel Not Supported",
			fmt.Sprintf("The channel %d of the Wi-Fi radio %s is a DFS channel, and the router does not support DFS.", number, radio.Device))
	case is6G && plan.PSCChannelsOnly.ValueBool() && !radio.Channels[i].PSC:
		diags.AddAttributeError(path.Root("channel"), "Channel Not Supported",
			fmt.Sprintf("The channel %d of the Wi-Fi radio %s is not a PSC channel. Set channel to 0 or to a PSC channel, or set psc_channels_only to false.", number, radio.Device))
	}
}

// channelWidths returns the channel widths of mode, as the web UI gives them: 20, 40, ... up to the largest width of
// mode, and auto when the radio supports it.
func channelWidths(radio *glinet.WifiRadio, mode string) []string {
	var widths []string
	for w := 20; w <= radio.HTModes.MaxWidth[mode]; w *= 2 {
		widths = append(widths, strconv.Itoa(w))
	}
	if radio.HTModes.Auto {
		widths = append(widths, "auto")
	}
	return widths
}

func channelNumbers(channels []glinet.WifiChannel) string {
	numbers := make([]string, len(channels))
	for i, c := range channels {
		numbers[i] = strconv.Itoa(c.Channel)
	}
	return strings.Join(numbers, ", ")
}

func findRadio(config *glinet.WifiConfig, device string) *glinet.WifiRadio {
	for i := range config.Radios {
		if config.Radios[i].Device == device {
			return &config.Radios[i]
		}
	}
	return nil
}

func radioNames(config *glinet.WifiConfig) []string {
	var names []string
	for _, radio := range config.Radios {
		names = append(names, radio.Device)
	}
	return names
}

// mainIface returns the interface of radio that is not a guest network.
func mainIface(radio *glinet.WifiRadio) *glinet.WifiIface {
	for i := range radio.Ifaces {
		if !radio.Ifaces[i].Guest {
			return &radio.Ifaces[i]
		}
	}
	return nil
}

func wifiRadioModel(radio *glinet.WifiRadio) *wifiRadioResourceModel {
	return &wifiRadioResourceModel{
		Name:            types.StringValue(radio.Device),
		Band:            types.StringValue(radio.Band),
		Channel:         types.Int64Value(int64(radio.Channel)),
		ChannelWidth:    types.StringValue(radio.HTMode),
		Mode:            types.StringValue(radio.HWMode),
		TxPower:         types.StringValue(providerTxPower(radio.TxPower)),
		RandomBSSID:     types.BoolValue(radio.RandomBSSID),
		PSCChannelsOnly: types.BoolPointerValue(radio.Chan6GOnlyPSC),
	}
}
