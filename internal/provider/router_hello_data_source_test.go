package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func TestAccRouterHelloDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Read testing
			{
				Config: testAccRouterHelloDataSourceConfig(0),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.glinet_router_hello.hello", "code", "0"),
				),
			},
		},
	})
}

func testAccRouterHelloDataSourceConfig(code int) string {
	return `
data "glinet_router_hello" "hello" {
}
`
}
