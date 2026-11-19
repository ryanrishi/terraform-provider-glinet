package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	fanStatusAddress          = "data.glinet_fan_status.test"
	fanStatusDataSourceConfig = `
data "glinet_fan_status" "test" {}
`
)

func TestFanStatusDataSource(t *testing.T) {
	parallel(t)
	tests := []struct {
		name    string
		result  string
		running bool
		speed   int64
	}{
		{"running", `{"speed": 3120, "status": true}`, true, 3120},
		{"stopped", `{"speed": 0, "status": false}`, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := glinettest.NewServer(t)
			srv.Fan.SetStatus(json.RawMessage(tt.result))

			readDataSource(t, fakeRouterConfig(srv)+fanStatusDataSourceConfig, expectAttributes(fanStatusAddress, map[string]knownvalue.Check{
				"running": knownvalue.Bool(tt.running),
				"speed":   knownvalue.Int64Exact(tt.speed),
			}))
		})
	}
}

func TestFanStatusDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), fanStatusDataSourceConfig, "fan", "get_status", "Unable to Read Fan Status")
}
