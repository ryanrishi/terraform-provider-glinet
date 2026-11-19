package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// refreshPriorState gives the provider the state rawState of a resource of the type typeName, as Terraform does
// for a state that an earlier release wrote with the same schema version: UpgradeResourceState, then ReadResource
// on the fake router srv. It returns the attributes of the refreshed state, or nil when Read removed the resource.
func refreshPriorState(t *testing.T, srv *glinettest.Server, typeName, rawState string) map[string]tftypes.Value {
	t.Helper()
	ctx := context.Background()
	server, err := testProtoV6ProviderFactories["glinet"]()
	if err != nil {
		t.Fatal(err)
	}

	providerType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"host": tftypes.String, "username": tftypes.String, "password": tftypes.String,
	}}
	config, err := tfprotov6.NewDynamicValue(providerType, tftypes.NewValue(providerType, map[string]tftypes.Value{
		"host":     tftypes.NewValue(tftypes.String, srv.URL),
		"username": tftypes.NewValue(tftypes.String, srv.Username),
		"password": tftypes.NewValue(tftypes.String, srv.Password),
	}))
	if err != nil {
		t.Fatal(err)
	}
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	expectNoDiagnostics(t, "ConfigureProvider", configured.Diagnostics)

	upgraded, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: typeName,
		RawState: &tfprotov6.RawState{JSON: []byte(rawState)},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectNoDiagnostics(t, "UpgradeResourceState", upgraded.Diagnostics)

	read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: upgraded.UpgradedState})
	if err != nil {
		t.Fatal(err)
	}
	expectNoDiagnostics(t, "ReadResource", read.Diagnostics)

	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := read.NewState.Unmarshal(schemas.ResourceSchemas[typeName].ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if state.IsNull() {
		return nil
	}
	var attributes map[string]tftypes.Value
	if err := state.As(&attributes); err != nil {
		t.Fatal(err)
	}
	return attributes
}

func expectNoDiagnostics(t *testing.T, call string, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		t.Errorf("%s: %s: %s", call, d.Summary, d.Detail)
	}
}

// expectStringAttribute checks that the attribute name of attributes is the string want.
func expectStringAttribute(t *testing.T, attributes map[string]tftypes.Value, name, want string) {
	t.Helper()
	var got string
	if err := attributes[name].As(&got); err != nil || got != want {
		t.Errorf("%s is %v, want %q", name, attributes[name], want)
	}
}
