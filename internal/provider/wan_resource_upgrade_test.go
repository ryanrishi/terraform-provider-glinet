package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestWANResourceUpgradeStateV0 upgrades a state of schema version 0, which has no port, and plans the same
// configuration without refresh, which must not replace or change the resource.
//
// resource.UnitTest cannot start from a state of schema version 0 without a provider binary of 0.1.x, so the
// test calls the provider server directly.
func TestWANResourceUpgradeStateV0(t *testing.T) {
	parallel(t)
	ctx := context.Background()
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}

	var schemaResp resource.SchemaResponse
	NewWANResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	objectType := schemaResp.Schema.Type().TerraformType(ctx)

	upgraded, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: "glinet_wan",
		Version:  0,
		RawState: &tfprotov6.RawState{JSON: []byte(`{"id":"wan","protocol":"pppoe","ipv4":null,"ipv6":null,` +
			`"username":"user@isp","password":"secret","vlan_id":null,"ttl":null,"ttl_ipv6":null,"mtu":1492}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range upgraded.Diagnostics {
		t.Fatalf("upgrade diagnostic: %s: %s", d.Summary, d.Detail)
	}

	state, err := upgraded.UpgradedState.Unmarshal(objectType)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]tftypes.Value
	if err := state.As(&values); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"id": "wan", "port": "wan", "username": "user@isp", "password": "secret"} {
		var got string
		if err := values[name].As(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("upgraded %s is %q, want %q", name, got, want)
		}
	}

	configValues := map[string]tftypes.Value{}
	for name, value := range values {
		configValues[name] = tftypes.NewValue(value.Type(), nil)
	}
	for _, name := range []string{"protocol", "username", "password", "mtu"} {
		configValues[name] = values[name]
	}
	config, err := tfprotov6.NewDynamicValue(objectType, tftypes.NewValue(objectType, configValues))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "glinet_wan",
		PriorState:       upgraded.UpgradedState,
		ProposedNewState: upgraded.UpgradedState,
		Config:           &config,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range plan.Diagnostics {
		t.Errorf("plan diagnostic: %s: %s", d.Summary, d.Detail)
	}
	if len(plan.RequiresReplace) != 0 {
		t.Errorf("plan replaces the resource because of %v", plan.RequiresReplace)
	}
	planned, err := plan.PlannedState.Unmarshal(objectType)
	if err != nil {
		t.Fatal(err)
	}
	if !planned.Equal(state) {
		t.Errorf("planned state is %v, want the upgraded state %v", planned, state)
	}
}
