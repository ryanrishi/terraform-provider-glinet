package provider

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// setHardwareFeature sets hardware_feature.<key> of system.get_info on the fake router.
func setHardwareFeature(t *testing.T, srv *glinettest.Server, key string, value any) {
	var info map[string]any
	if err := json.Unmarshal(srv.System.Info(), &info); err != nil {
		t.Fatal(err)
	}
	features, ok := info["hardware_feature"].(map[string]any)
	if !ok {
		t.Fatal("system.get_info has no hardware_feature")
	}
	features[key] = value
	srv.System.SetInfo(json.RawMessage(mustJSON(t, info)))
}

// expectNoHardware applies config on a router without the hardware of key and expects the error and no call of
// module.method.
func expectNoHardware(t *testing.T, srv *glinettest.Server, key string, value any, config, module, method string) {
	setHardwareFeature(t, srv, key, value)
	writes := recordCalls(t, srv, module, method)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + config,
				ExpectError: regexp.MustCompile(`(?s)Hardware Not Available.*` + wrapped("hardware_feature."+key+" of system.get_info")),
			},
		},
	})

	if err := writes.expect()(nil); err != nil {
		t.Error(err)
	}
}

// expectSystemInfoError makes system.get_info fail and expects config to fail with no call of module.method.
func expectSystemInfoError(t *testing.T, srv *glinettest.Server, config, module, method string) {
	failRouterMethod(srv, "system", "get_info")()
	writes := recordCalls(t, srv, module, method)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeRouterConfig(srv) + config,
				ExpectError: regexp.MustCompile(`(?s)Unable to Read System Information.*Method not found`),
			},
		},
	})

	if err := writes.expect()(nil); err != nil {
		t.Error(err)
	}
}
