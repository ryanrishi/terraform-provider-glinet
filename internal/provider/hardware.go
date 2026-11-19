package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/ryanrishi/glinet-client-go"
)

// requireHardware adds an error and returns false when the router does not have the hardware that
// key names in hardware_feature of system.get_info. has tells from the hardware features if the
// router has it. The web UI shows the settings of this hardware only when the router has it.
func requireHardware(ctx context.Context, client *glinet.Client, key, hardware string, has func(glinet.HardwareFeature) bool, diags *diag.Diagnostics) bool {
	info, err := client.System.GetInfo(ctx)
	if err != nil {
		diags.AddError("Unable to Read System Information", err.Error())
		return false
	}
	if !has(info.HardwareFeature) {
		diags.AddError(
			"Hardware Not Available",
			fmt.Sprintf("The router has no %s (hardware_feature.%s of system.get_info is false or empty). The provider did not change the router.", hardware, key),
		)
		return false
	}
	return true
}
