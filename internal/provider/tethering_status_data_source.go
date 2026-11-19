package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

// tetheringStatuses has the values of status. The index is the router code (status 0 to 2).
var tetheringStatuses = []string{"not_connected", "connected", "connecting"}

// tetheringDeviceTypes has the values of the device type. The index is the router code (type 0 to 1).
var tetheringDeviceTypes = []string{"android", "ios"}

var (
	_ datasource.DataSource              = &tetheringStatusDataSource{}
	_ datasource.DataSourceWithConfigure = &tetheringStatusDataSource{}
)

func NewTetheringStatusDataSource() datasource.DataSource {
	return &tetheringStatusDataSource{}
}

type tetheringStatusDataSource struct {
	client *glinet.Client
}

type tetheringStatusModel struct {
	Status     types.String           `tfsdk:"status"`
	StatusCode int                    `tfsdk:"status_code"`
	Devices    []tetheringDeviceModel `tfsdk:"devices"`
	IPv4       *uplinkIPv4Model       `tfsdk:"ipv4"`
}

type tetheringDeviceModel struct {
	Device string       `tfsdk:"device"`
	Type   types.String `tfsdk:"type"`
	Use    bool         `tfsdk:"use"`
}

func (d *tetheringStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tethering_status"
}

func (d *tetheringStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the state of the USB tethering uplink of the router: a phone on USB (`tethering.get_status`). " +
			"The read was tested on a router only without a phone on USB. The values with a phone come from the web UI code. " +
			"The values change while the router runs.",
		Attributes: map[string]schema.Attribute{
			"status": computedString("Status of the uplink: `not_connected` (`0`), `connected` (`1`) or `connecting` (`2`). " +
				"A router without a phone on USB gave `connecting`. Null when the router gives a code that is not in this list."),
			"status_code": computedInt64("Status of the uplink as a number (`status`)."),
			"devices": computedList("Phones on USB (`devices`).", map[string]schema.Attribute{
				"device": computedString("Name of the network device of the phone (`device`)."),
				"type":   computedString("Type of the phone (`type`): `android` (`0`) or `ios` (`1`). Null when the router gives no type or a code that is not in this list."),
				"use":    computedBool("Whether the uplink uses this phone (`use`)."),
			}),
			"ipv4": computedObject("IPv4 address of the uplink (`ipv4`). This value comes from the web UI code and was not seen on a router. "+
				"Null when the router gives no IPv4 address.", uplinkIPv4Attributes()),
		},
	}
}

func (d *tetheringStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = dataSourceClient(req, resp)
}

func (d *tetheringStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.Tethering.GetStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Tethering Status", err.Error())
		return
	}

	state := tetheringStatusModel{
		Status:     codeName(tetheringStatuses, status.Status),
		StatusCode: status.Status,
		Devices:    []tetheringDeviceModel{},
		IPv4:       uplinkIPv4(ctx, status.IPv4, &resp.Diagnostics),
	}
	for _, device := range status.Devices {
		model := tetheringDeviceModel{Device: device.Device, Type: types.StringNull(), Use: device.Use}
		if device.Type != nil {
			model.Type = codeName(tetheringDeviceTypes, *device.Type)
		}
		state.Devices = append(state.Devices, model)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
