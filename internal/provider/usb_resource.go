package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

const usbID = "usb"

var (
	_ resource.Resource                = &usbResource{}
	_ resource.ResourceWithConfigure   = &usbResource{}
	_ resource.ResourceWithImportState = &usbResource{}
)

func NewUSBResource() resource.Resource {
	return &usbResource{}
}

type usbResource struct {
	client *glinet.Client
}

type usbResourceModel struct {
	ID          types.String `tfsdk:"id"`
	USB3Enabled types.Bool   `tfsdk:"usb3_enabled"`
}

func (r *usbResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_usb"
}

func (r *usbResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets the USB port of the router to USB 3.0 or USB 2.0 (`system.get_usb3_disable` and `system.set_usb3_disable`). " +
			"**Warning:** a change of `usb3_enabled` can disconnect the USB devices. The web UI shows a warning before the change, " +
			"but the text of the warning is not in the web UI code, so the effect on the USB devices is not known. " +
			"The router has one USB configuration, so use one instance of this resource. " +
			"Only a router with a USB 3.0 port (`hardware_feature.usb3` of `system.get_info` is not empty) has this setting: " +
			"on other routers, create and update fail and do not write to the router. " +
			"The write is from the web UI code of firmware 4.8.3 and was not tested on a router. " +
			"Destroy removes the resource from the Terraform state and does not change the router.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Always `" + usbID + "`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"usb3_enabled": schema.BoolAttribute{
				Description: "Whether the USB port works as USB 3.0. When `false`, it works as USB 2.0. " +
					"The router API has the opposite value `usb3_disable`. When the router gives no `usb3_disable`, the provider reads `false`, as the web UI does.",
				Required: true,
			},
		},
	}
}

func (r *usbResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = resourceClient(req, resp)
}

func (r *usbResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *usbResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	r.read(ctx, &resp.State, &resp.Diagnostics)
}

func (r *usbResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.set(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *usbResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *usbResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != usbID {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("The import ID of glinet_usb must be %q, got %q.", usbID, req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), usbID)...)
}

func (r *usbResource) set(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var model usbResourceModel
	diags.Append(plan.Get(ctx, &model)...)
	if diags.HasError() {
		return
	}

	if !requireHardware(ctx, r.client, "usb3", "USB 3.0 port", func(h glinet.HardwareFeature) bool { return h.USB3 != "" }, diags) {
		return
	}

	if err := r.client.System.SetUSB3Disabled(ctx, !model.USB3Enabled.ValueBool()); err != nil {
		diags.AddError("Unable to Set USB Configuration", err.Error())
		return
	}

	model.ID = types.StringValue(usbID)
	diags.Append(state.Set(ctx, &model)...)
	if diags.HasError() {
		return
	}

	r.read(ctx, state, diags)
}

func (r *usbResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	disabled, err := r.client.System.GetUSB3Disabled(ctx)
	if err != nil {
		diags.AddError("Unable to Read USB Configuration", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &usbResourceModel{
		ID:          types.StringValue(usbID),
		USB3Enabled: types.BoolValue(!disabled),
	})...)
}
