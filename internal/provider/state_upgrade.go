package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// renameAttributes returns a state upgrader that moves the value of each attribute old of names to the attribute
// names[old], for a state that release 0.2.0 wrote before the attribute renames.
func renameAttributes(names map[string]string) resource.StateUpgrader {
	return resource.StateUpgrader{StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		var state map[string]any
		if err := json.Unmarshal(req.RawState.JSON, &state); err != nil {
			resp.Diagnostics.AddError("Unable to Upgrade State", err.Error())
			return
		}

		for old, name := range names {
			state[name] = state[old]
			delete(state, old)
		}
		upgraded, err := json.Marshal(state)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Upgrade State", err.Error())
			return
		}
		resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgraded}
	}}
}
