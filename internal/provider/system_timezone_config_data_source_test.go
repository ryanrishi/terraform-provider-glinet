package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSystemTimezoneConfigDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Read testing
			{
				Config: providerConfig + testAccSystemTimezoneConfingDataSourceConfig(0),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "code", "0"),
				),
			},
		},
	})
}

func testAccSystemTimezoneConfingDataSourceConfig(code int) string {
	return `
data "glinet_system_timezone_config" "config" {}
`
}
