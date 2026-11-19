package provider

import (
	"context"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const maxSpeedLimit = 1073741824

var (
	_ resource.Resource                   = &clientSpeedLimitResource{}
	_ resource.ResourceWithConfigure      = &clientSpeedLimitResource{}
	_ resource.ResourceWithImportState    = &clientSpeedLimitResource{}
	_ resource.ResourceWithValidateConfig = &clientSpeedLimitResource{}
)

func NewClientSpeedLimitResource() resource.Resource {
	return &clientSpeedLimitResource{}
}

type clientSpeedLimitResource struct {
	client *glinet.Client
}

type clientSpeedLimitResourceModel struct {
	MAC      caseInsensitiveString `tfsdk:"mac"`
	Download types.Float64         `tfsdk:"download"`
	Upload   types.Float64         `tfsdk:"upload"`
}

func (r *clientSpeedLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client_speed_limit"
}

func (r *clientSpeedLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	speedDescription := "in KiB/s (the web UI shows KB/s with a base of 1024): 0 for no limit, or 1 to 1073741824 in steps of 0.125 " +
		"(the Kbps mode of the web UI sends the Kbps value divided by 8). " +
		"It is not known whether the router keeps a fraction or rounds it: " +
		"when the configured value has a fraction and the router has that value rounded down or up to a whole number, the state keeps the configured value. " +
		"Default 0."

	resp.Schema = schema.Schema{
		Description: "Manages the speed limit of a client device, as the clients page of the web UI does. " +
			"The write was not tested on a router; the parameters come from the router's web UI. " +
			"The web UI shows the speed limit only when the router is in router mode or passthrough mode. " +
			"The web UI does not let you set a limit while network acceleration is on, and says that network acceleration can stop the limits. " +
			"The resource does not change network acceleration and does not check it. " +
			"Create fails when the client list of the router has no client with the MAC address, " +
			"because it is not known whether the router accepts a limit for a MAC address that is not in the list. " +
			"Create also fails when the client already has a limit. " +
			"At least one of `download` and `upload` must be more than 0, because it is not known what the router does with two 0 values. " +
			"When the client goes out of the client list, or has no limit, refresh removes the resource from the state. " +
			"The provider finds the client without letter case and writes the MAC address in the letter case of the client list. " +
			"When the client list has the MAC address in more than one letter case, update fails when `mac` would select another client. " +
			"Import sets `mac` in the letter case of the client list. " +
			"Destroy removes the speed limit from the router.",
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
			"download": schema.Float64Attribute{
				Description: "Download limit of the client (`limit_rx` of the client list), " + speedDescription,
				Optional:    true,
				Computed:    true,
				Default:     float64default.StaticFloat64(0),
				Validators:  []validator.Float64{speedLimitValidator{}},
			},
			"upload": schema.Float64Attribute{
				Description: "Upload limit of the client (`limit_tx` of the client list), " + speedDescription,
				Optional:    true,
				Computed:    true,
				Default:     float64default.StaticFloat64(0),
				Validators:  []validator.Float64{speedLimitValidator{}},
			},
		},
	}
}

func (r *clientSpeedLimitResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var download, upload types.Float64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("download"), &download)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("upload"), &upload)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if noSpeedLimit(download) && noSpeedLimit(upload) {
		resp.Diagnostics.AddAttributeError(
			path.Root("download"),
			"No Speed Limit",
			"Set download or upload to a value more than 0. "+
				"It is not known what the router does with a limit of 0 in both directions. To remove the limit, destroy the resource.",
		)
	}
}

func (r *clientSpeedLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *clientSpeedLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clientSpeedLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	c := findClientForWrite(ctx, r.client, plan.MAC.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if hasSpeedLimit(c) {
		resp.Diagnostics.AddAttributeError(
			path.Root("mac"),
			"Client Speed Limit Already Exists",
			fmt.Sprintf("The client with MAC address %s already has a speed limit (download %g KiB/s, upload %g KiB/s). "+
				"Import it with the ID %q, or remove it from the router.", c.MAC, c.LimitRx, c.LimitTx, c.MAC),
		)
		return
	}

	r.write(ctx, c.MAC, plan, &resp.State, &resp.Diagnostics)
}

func (r *clientSpeedLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clientSpeedLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := readClient(ctx, r.client, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Speed Limit", err.Error())
		return
	}
	if c == nil || !hasSpeedLimit(c) {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, speedLimitModel(c, state))...)
}

// Update writes to the client of the state. A change of only the letter case of mac needs no write.
func (r *clientSpeedLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state clientSpeedLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	c := findClientForUpdate(ctx, r.client, state.MAC.ValueString(), plan.MAC.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Download.Equal(state.Download) && plan.Upload.Equal(state.Upload) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}
	r.write(ctx, c.MAC, plan, &resp.State, &resp.Diagnostics)
}

func (r *clientSpeedLimitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clientSpeedLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	c, err := readClient(ctx, r.client, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Client Speed Limit", err.Error())
		return
	}
	if c == nil || !hasSpeedLimit(c) {
		return
	}

	if err := r.client.QoS.RemoveSpeedLimit(ctx, c.MAC); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Client Speed Limit", err.Error())
	}
}

// ImportState sets mac in the letter case of the client list, so that a configuration in that letter case has no difference.
func (r *clientSpeedLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importClientMAC(ctx, r.client, req, resp)
}

// write sends the limits of plan to the client with the MAC address mac of the client list, and sets state from the client list.
// It sets state from plan before the read, so that a failed read does not leave a limit on the router that the state does not have.
func (r *clientSpeedLimitResource) write(ctx context.Context, mac string, plan clientSpeedLimitResourceModel, state *tfsdk.State, diags *diag.Diagnostics) {
	limit := glinet.SpeedLimit{MAC: mac, Download: plan.Download.ValueFloat64(), Upload: plan.Upload.ValueFloat64()}
	if err := r.client.QoS.SetSpeedLimit(ctx, limit); err != nil {
		diags.AddError("Unable to Write Client Speed Limit", err.Error())
		return
	}

	diags.Append(state.Set(ctx, plan)...)
	if diags.HasError() {
		return
	}

	c, err := readClient(ctx, r.client, mac)
	if err != nil {
		diags.AddError("Unable to Read Client Speed Limit", err.Error())
		return
	}
	if c == nil || !hasSpeedLimit(c) {
		diags.AddError("Client Speed Limit Not Found",
			"The client list of the router has no speed limit for MAC address "+mac+" after the write. "+
				"Make sure that the router is in router mode or passthrough mode and that network acceleration is off.")
		return
	}
	diags.Append(state.Set(ctx, speedLimitModel(c, plan))...)
}

func hasSpeedLimit(c *glinet.ClientDevice) bool {
	return c.LimitRx != 0 || c.LimitTx != 0
}

func noSpeedLimit(v types.Float64) bool {
	return v.IsNull() || (!v.IsUnknown() && v.ValueFloat64() == 0)
}

// speedLimitModel gives the limits of the client. When the limit of prior has a fraction and the router has it rounded
// down or up to a whole number, the limit keeps the value of prior, because it is not known whether the router rounds a fraction.
func speedLimitModel(c *glinet.ClientDevice, prior clientSpeedLimitResourceModel) clientSpeedLimitResourceModel {
	return clientSpeedLimitResourceModel{
		MAC:      caseInsensitiveStringValue(c.MAC),
		Download: speedLimitValue(c.LimitRx, prior.Download),
		Upload:   speedLimitValue(c.LimitTx, prior.Upload),
	}
}

func speedLimitValue(router float64, prior types.Float64) types.Float64 {
	if prior.IsNull() || prior.IsUnknown() {
		return types.Float64Value(router)
	}
	p := prior.ValueFloat64()
	if p != math.Trunc(p) && (router == math.Floor(p) || router == math.Ceil(p)) {
		return prior
	}
	return types.Float64Value(router)
}

// speedLimitValidator accepts the values that the web UI can send: 0, or 1 to 1073741824 in steps of 0.125.
type speedLimitValidator struct{}

func (v speedLimitValidator) Description(context.Context) string {
	return "must be 0, or from 1 to 1073741824 in steps of 0.125"
}

func (v speedLimitValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v speedLimitValidator) ValidateFloat64(ctx context.Context, req validator.Float64Request, resp *validator.Float64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueFloat64()
	if value == 0 || (value >= 1 && value <= maxSpeedLimit && value*8 == math.Trunc(value*8)) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value", fmt.Sprintf("Attribute %s %s, got: %g", req.Path, v.Description(ctx), value))
}
