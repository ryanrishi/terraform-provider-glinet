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
				Config: providerConfig + testAccSystemTimezoneConfigDataSourceConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "zonename", "Asia/Shanghai"),
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "tzoffset", "+0800"),
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "autotimezone_enabled", "true"),
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "localtime", "1643200134"),
					resource.TestCheckResourceAttr("data.glinet_system_timezone_config.config", "timezone", "CST-8"),
				),
			},
		},
	})
}

func testAccSystemTimezoneConfigDataSourceConfig() string {
	return `
data "glinet_system_timezone_config" "config" {}
`
}
