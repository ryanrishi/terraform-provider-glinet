package provider

import (
	"context"
	"fmt"
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

const (
	switchButtonID   = "switch_button"
	switchButtonNone = "none"
)

var (
	_ resource.Resource                     = &switchButtonResource{}
	_ resource.ResourceWithConfigure        = &switchButtonResource{}
	_ resource.ResourceWithImportState      = &switchButtonResource{}
	_ resource.ResourceWithConfigValidators = &switchButtonResource{}
)

func NewSwitchButtonResource() resource.Resource {
	return &switchButtonResource{}
}

type switchButtonResource struct {
	client *glinet.Client
}

type switchButtonResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Function    types.String `tfsdk:"function"`
	SubFunction types.String `tfsdk:"sub_function"`
}

func (r *switchButtonResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_switch_button"
}

func (r *switchButtonResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets the function of the side switch of the router (`switch-button.get_config` and `switch-button.set_config`). " +
			"The switch turns the function on in one position and off in the other. " +
			"**Warning:** after a change, the position of the switch can turn a VPN tunnel, a Wi-Fi network, AdGuard Home or another function on or off. " +
			"The provider always sends `sync` `false`, as the web UI does when the user does not accept to sync the function with the current switch position. " +
			"What the router does with the function at the time of the write is not known. " +
			"The functions depend on the router model, the firmware and the configuration: before each write, the provider reads them with `switch-button.get_funcs`, " +
			"and when `function` or `sub_function` is not available, create and update fail and do not write to the router. " +
			"Only a router with a switch (`hardware_feature.switch_button` of `system.get_info` is not empty) has this setting: " +
			"on other routers, create and update fail and do not write to the router. " +
			"The router has one switch configuration, so use one instance of this resource. " +
			"The write is from the web UI code of firmware 4.8.3 and was not tested on a router. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + switchButtonID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"function": schema.StringAttribute{
				Description: "Function of the switch (`func` in the router API): `" + switchButtonNone + "` or a function of `switch-button.get_funcs`. " +
					"Firmware 4.8.3 has functions such as `adguardhome`, `led`, `repeater`, `tor`, `wifi` and `vpn`, and firmware 4.7.0 has `openvpn` and `wireguard` in place of `vpn`. " +
					"The web UI does not let the user select a function that is not installed, such as AdGuard Home or Tor; the router can list it.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"sub_function": schema.StringAttribute{
				Description: "Sub-function of `function` (`sub_func` in the router API), for example `main_wifi` or `guest_wifi` for `wifi`, " +
					"or the ID of a VPN tunnel as a string for `vpn`. Required when the function has sub-functions in `switch-button.get_funcs`, " +
					"and cannot be set when it has none or when `function` is `" + switchButtonNone + "`. " +
					"A VPN tunnel ID can change when the VPN tunnels change; then the next plan shows a difference or the apply fails.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
	}
}

func (r *switchButtonResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{configRules{
		description: "sub_function cannot be set when function is " + switchButtonNone,
		validate: func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics) {
			var config switchButtonResourceModel
			diags.Append(req.Config.Get(ctx, &config)...)
			if diags.HasError() {
				return
			}
			if config.Function.ValueString() == switchButtonNone {
				forbid(diags, "sub_function", config.SubFunction, "when function is "+switchButtonNone)
			}
		},
	}}
}

func (r *switchButtonResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *switchButtonResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *switchButtonResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *switchButtonResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *switchButtonResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *switchButtonResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != switchButtonID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_switch_button must be %q, got %q.", switchButtonID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), switchButtonID)...)
}

func (r *switchButtonResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model switchButtonResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if !requireHardware(ctx, r.client, "switch_button", "switch", func(h glinet.HardwareFeature) bool { return h.SwitchButton != "" }, diags) {
		return
	}

	config := glinet.SwitchButtonConfig{Func: model.Function.ValueString(), SubFunc: model.SubFunction.ValueString()}
	if config.Func != switchButtonNone {
		funcs, err := r.client.SwitchButton.ListFuncs(ctx)
		if err != nil {
			diags.AddError("Unable to Read Switch Functions", err.Error())
			return
		}
		if problem := checkSwitchFunction(funcs, config); problem != "" {
			diags.AddError("Switch Function Not Available", problem+" The provider did not change the router.")
			return
		}
	}

	if err := r.client.SwitchButton.SetConfig(ctx, config, false); err != nil {
		diags.AddError("Unable to Set Switch Function", err.Error())
		return
	}

	model.ID = types.StringValue(switchButtonID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

// checkSwitchFunction returns why config is not a function of funcs with one of its sub-functions, or "" when it is.
func checkSwitchFunction(funcs []glinet.SwitchButtonFunc, config glinet.SwitchButtonConfig) string {
	names := []string{switchButtonNone}
	for _, f := range funcs {
		names = append(names, f.Func)
		if f.Func != config.Func {
			continue
		}

		subNames := make([]string, len(f.SubFuncs))
		for j, sub := range f.SubFuncs {
			subNames[j] = sub.Func
			if sub.Func == config.SubFunc {
				return ""
			}
		}
		switch {
		case len(subNames) == 0 && config.SubFunc == "":
			return ""
		case len(subNames) == 0:
			return fmt.Sprintf("Function %q has no sub-functions in switch-button.get_funcs, so sub_function cannot be set.", config.Func)
		case config.SubFunc == "":
			return fmt.Sprintf("Function %q needs sub_function. The sub-functions of %q in switch-button.get_funcs are: %s.", config.Func, config.Func, quoted(subNames))
		default:
			return fmt.Sprintf("Sub-function %q is not available for function %q. The sub-functions of %q in switch-button.get_funcs are: %s.",
				config.SubFunc, config.Func, config.Func, quoted(subNames))
		}
	}
	return fmt.Sprintf("Function %q is not available. The available functions are %s (%q and the functions of switch-button.get_funcs).", config.Func, quoted(names), switchButtonNone)
}

func quoted(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(q, ", ")
}

func (r *switchButtonResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	config, err := r.client.SwitchButton.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Read Switch Function", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &switchButtonResourceModel{
		ID:          types.StringValue(switchButtonID),
		Function:    types.StringValue(config.Func),
		SubFunction: stringOrNull(config.SubFunc),
	})...)
}
