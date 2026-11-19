package provider

import (
	"context"
	"fmt"
	"slices"

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

const repeaterID = "repeater"

// repeaterSwitchModes has the values of switching_mode. The index is the router code (auto 0 to 2).
var repeaterSwitchModes = []string{"no_switching", "auto_switching", "auto_switching_without_network"}

var (
	_ resource.Resource                = &repeaterResource{}
	_ resource.ResourceWithConfigure   = &repeaterResource{}
	_ resource.ResourceWithImportState = &repeaterResource{}
)

func NewRepeaterResource() resource.Resource {
	return &repeaterResource{}
}

type repeaterResource struct {
	client *glinet.Client
}

type repeaterResourceModel struct {
	ID            types.String `tfsdk:"id"`
	SwitchingMode types.String `tfsdk:"switching_mode"`
	LockBand      types.String `tfsdk:"lock_band"`
}

func (r *repeaterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repeater"
}

func (r *repeaterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the options of the repeater, the Wi-Fi uplink of the router (`repeater.set_config` in the router API). " +
			"The router has one repeater configuration, so use one instance of this resource. " +
			"The resource does not connect the repeater to a network and does not change the saved networks. " +
			"When the uplink of the router is the repeater, a band lock to a band without the uplink network can cut the Internet access of the router and its clients. " +
			"It is not known whether a change of `switching_mode` makes the repeater connect to another saved network at once. " +
			"After the write, the provider reads the configuration again and stops with an error when the router did not apply it. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + repeaterID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"switching_mode": schema.StringAttribute{
				Description: "What the repeater does with the other saved networks (`auto` in the router API): " +
					"`no_switching` (`0`), `auto_switching` (`1`) or `auto_switching_without_network` (`2`). " +
					"The names come from the label keys of the web UI (`repeater.no_switching`, `repeater.auto_switching`, `repeater.auto_switching_without_network`). " +
					"The web UI code does not tell the exact behaviour of each mode. " +
					"Firmware 4.7.0 gives a bool, which the provider reads as `auto_switching` (`true`) or `no_switching` (`false`); " +
					"it is not known whether firmware 4.7.0 accepts a number in the write. " +
					"When it is not set, the provider writes the router value. " +
					"The state has no value when the router gives a code that is not in this list.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf(repeaterSwitchModes...),
				},
			},
			"lock_band": schema.StringAttribute{
				Description: "Band that the repeater uses (`lock_band` in the router API), for example `2g` or `5g`, or `\"\"` for all bands. " +
					"The bands depend on the router model. The web UI shows this option only on a router with two or more bands. " +
					"For `\"\"`, the write has no `lock_band`, as in the web UI. It is not known whether such a write removes a band lock on the router. " +
					"When it is not set, the provider writes the router value.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *repeaterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *repeaterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Config, &resp.State, &resp.Diagnostics)
}

func (r *repeaterResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	config, ok := r.getConfig(ctx, &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, repeaterModel(config))...)
}

func (r *repeaterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Config, &resp.State, &resp.Diagnostics)
}

func (r *repeaterResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *repeaterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != repeaterID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_repeater must be %q, got %q.", repeaterID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), repeaterID)...)
}

// set writes the configured values and the router values of the attributes that are not set.
func (r *repeaterResource) set(ctx context.Context, cfg tfsdk.Config, state *tfsdk.State, diags *diag.Diagnostics) {
	var model repeaterResourceModel
	diags.Append(cfg.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	current, ok := r.getConfig(ctx, diags)
	if !ok {
		return
	}

	request := glinet.SetRepeaterConfigRequest{Auto: current.Auto, LockBand: current.LockBand}
	if !model.SwitchingMode.IsNull() {
		request.Auto = glinet.RepeaterSwitchMode(slices.Index(repeaterSwitchModes, model.SwitchingMode.ValueString()))
	}
	if !model.LockBand.IsNull() {
		request.LockBand = model.LockBand.ValueString()
	}

	if err := r.client.Repeater.SetConfig(ctx, request); err != nil {
		diags.AddError("Unable to Set Repeater Configuration", err.Error())
		return
	}

	written, ok := r.getConfig(ctx, diags)
	if !ok {
		return
	}
	if written.Auto != request.Auto || written.LockBand != request.LockBand {
		diags.AddError("Repeater Configuration Not Applied", fmt.Sprintf(
			"The provider wrote auto %d and lock_band %q, and the router gives auto %d and lock_band %q after the write.",
			request.Auto, request.LockBand, written.Auto, written.LockBand))
		return
	}

	diags.Append(state.Set(ctx, repeaterModel(written))...)
}

func (r *repeaterResource) getConfig(ctx context.Context, diags *diag.Diagnostics) (*glinet.RepeaterConfig, bool) {
	config, err := r.client.Repeater.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Repeater Configuration", err.Error())
		return nil, false
	}
	return config, true
}

func repeaterModel(config *glinet.RepeaterConfig) *repeaterResourceModel {
	return &repeaterResourceModel{
		ID:            types.StringValue(repeaterID),
		SwitchingMode: codeName(repeaterSwitchModes, int(config.Auto)),
		LockBand:      types.StringValue(config.LockBand),
	}
}
