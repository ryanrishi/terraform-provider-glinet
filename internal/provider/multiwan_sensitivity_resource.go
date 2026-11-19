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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const multiWANSensitivityID = "multiwan_sensitivity"

// multiWANSensitivityDefault is the sensitivity that the web UI shows when the router result has no sensitivity.
var multiWANSensitivityDefault = glinet.MultiWANSensitivity{Level: glinet.MultiWANSensitivityMedium, Interval: 5}

var multiWANSensitivityLevels = []string{
	glinet.MultiWANSensitivityLow,
	glinet.MultiWANSensitivityMedium,
	glinet.MultiWANSensitivityHigh,
	glinet.MultiWANSensitivityCustom,
}

var (
	_ resource.Resource                     = &multiWANSensitivityResource{}
	_ resource.ResourceWithConfigure        = &multiWANSensitivityResource{}
	_ resource.ResourceWithImportState      = &multiWANSensitivityResource{}
	_ resource.ResourceWithConfigValidators = &multiWANSensitivityResource{}
)

func NewMultiWANSensitivityResource() resource.Resource {
	return &multiWANSensitivityResource{}
}

type multiWANSensitivityResource struct {
	client *glinet.Client
}

type multiWANSensitivityResourceModel struct {
	ID       types.String  `tfsdk:"id"`
	Level    types.String  `tfsdk:"level"`
	Interval types.Float64 `tfsdk:"interval"`
}

// validTrackInterval accepts 0.5 or a whole number from 1 to 90, as the web UI does.
func validTrackInterval(v float64) bool {
	return v == 0.5 || (v >= 1 && v <= 90 && v == math.Trunc(v))
}

type trackIntervalValidator struct{}

func (trackIntervalValidator) Description(context.Context) string {
	return "must be 0.5 or a whole number from 1 to 90"
}

func (v trackIntervalValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v trackIntervalValidator) ValidateFloat64(ctx context.Context, req validator.Float64Request, resp *validator.Float64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || validTrackInterval(req.ConfigValue.ValueFloat64()) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value",
		fmt.Sprintf("Attribute %s %s, got: %v", req.Path, v.Description(ctx), req.ConfigValue.ValueFloat64()))
}

func (r *multiWANSensitivityResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multiwan_sensitivity"
}

func (r *multiWANSensitivityResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the sensitivity of the multi-WAN tracking for all interfaces (`kmwan.set_sensitivity` in the router API). " +
			"The router has one sensitivity, so use one instance of this resource.\n\n" +
			multiWANWarning +
			"The sensitivity sets how fast the router finds out that an interface is offline or online again. " +
			"The provider writes only when the router values differ from the configuration, so a create whose values are equal to those of the router sends no write. " +
			"When the router result has no sensitivity, the provider uses the values that the web UI shows then: level `medium` and interval 5 seconds. " +
			"The format of the read was seen on a router. " +
			untestedWriteNote + destroyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + multiWANSensitivityID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"level": schema.StringAttribute{
				Description: "Sensitivity level: `low`, `medium`, `high` or `custom` (`level` in the router API). " +
					"Only `custom` lets you set `interval`.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf(multiWANSensitivityLevels...),
				},
			},
			"interval": schema.Float64Attribute{
				Description: "Track interval in seconds: `0.5` or a whole number from `1` to `90` (`val` in the router API). " +
					"Can be set only when `level` is `custom`. When it is not set, the provider sends the router value, as the web UI does for the other levels. " +
					"What the router does with the interval of a level other than `custom` is not known.",
				Optional: true,
				Computed: true,
				Validators: []validator.Float64{
					trackIntervalValidator{},
				},
			},
		},
	}
}

func (r *multiWANSensitivityResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "interval goes with level custom",
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config multiWANSensitivityResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() || config.Level.IsNull() || config.Level.IsUnknown() {
				return
			}

			if config.Level.ValueString() != glinet.MultiWANSensitivityCustom {
				forbid(diags, "interval", config.Interval, "when level is not custom")
			}
		},
	}}
}

func (r *multiWANSensitivityResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *multiWANSensitivityResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANSensitivityResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *multiWANSensitivityResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *multiWANSensitivityResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *multiWANSensitivityResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != multiWANSensitivityID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_multiwan_sensitivity must be %q, got %q.", multiWANSensitivityID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), multiWANSensitivityID)...)
}

// write sends the sensitivity of plan when it differs from the router. An unknown interval, which is not in the
// configuration, gets the router value.
func (r *multiWANSensitivityResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model multiWANSensitivityResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	routerWrites.Lock()
	defer routerWrites.Unlock()

	current := r.routerSensitivity(ctx, diags)
	if diags.HasError() {
		return
	}

	sensitivity := glinet.MultiWANSensitivity{Level: model.Level.ValueString(), Interval: current.Interval}
	if !model.Interval.IsUnknown() {
		sensitivity.Interval = model.Interval.ValueFloat64()
	}
	if sensitivity != *current {
		if err := r.client.MultiWAN.SetSensitivity(ctx, sensitivity); err != nil {
			diags.AddError("Unable to Set Multi-WAN Sensitivity", err.Error())
			return
		}
	}

	r.read(ctx, state, diags)
}

func (r *multiWANSensitivityResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	sensitivity := r.routerSensitivity(ctx, diags)
	if diags.HasError() {
		return
	}

	diags.Append(state.Set(ctx, &multiWANSensitivityResourceModel{
		ID:       types.StringValue(multiWANSensitivityID),
		Level:    types.StringValue(sensitivity.Level),
		Interval: types.Float64Value(sensitivity.Interval),
	})...)
}

// routerSensitivity returns the sensitivity of the router. When the router result has no sensitivity, the client
// gives the zero value, and routerSensitivity returns the values that the web UI shows then.
func (r *multiWANSensitivityResource) routerSensitivity(ctx context.Context, diags *diag.Diagnostics) *glinet.MultiWANSensitivity {
	sensitivity, err := r.client.MultiWAN.GetSensitivity(ctx)
	if err != nil {
		diags.AddError("Unable to Read Multi-WAN Sensitivity", err.Error())
		return nil
	}
	if *sensitivity == (glinet.MultiWANSensitivity{}) {
		d := multiWANSensitivityDefault
		return &d
	}
	return sensitivity
}
