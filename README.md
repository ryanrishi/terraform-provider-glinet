# terraform-provider-glinet
[![Tests](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml/badge.svg)](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ryanrishi/terraform-provider-glinet)](https://github.com/ryanrishi/terraform-provider-glinet/releases/latest)

This provider lets Terraform manage [GL.iNet](https://www.gl-inet.com/) routers that run [v4 firmware](https://docs.gl-inet.com/router/en/4/).
It uses the Go client [glinet-client-go](https://github.com/ryanrishi/glinet-client-go).

## Using the provider

The provider is in the [Terraform Registry](https://registry.terraform.io/providers/ryanrishi/glinet/latest/docs) and the [OpenTofu Registry](https://search.opentofu.org/provider/ryanrishi/glinet/latest) as `ryanrishi/glinet`.

```terraform
terraform {
  required_providers {
    glinet = {
      source = "ryanrishi/glinet"
    }
  }
}

provider "glinet" {
  host     = "http://192.168.8.1" # default
  username = "root"               # default
  password = var.glinet_password
}

variable "glinet_password" {
  type      = string
  sensitive = true
}
```

If an argument is not set, the provider reads it from `GLINET_HOST`, `GLINET_USERNAME` or `GLINET_PASSWORD`.

> [!WARNING]
> Some resources can cut the access to the router: [`glinet_wan`](docs/resources/wan.md), [`glinet_lan`](docs/resources/lan.md), [`glinet_wifi_interface`](docs/resources/wifi_interface.md) and [`glinet_wifi_radio`](docs/resources/wifi_radio.md).
> Read the warnings on their pages before you apply them.

| Type | Name | Documentation |
|---|---|---|
| Resource | `glinet_adguardhome` | [docs/resources/adguardhome.md](docs/resources/adguardhome.md) |
| Resource | `glinet_client_block` | [docs/resources/client_block.md](docs/resources/client_block.md) |
| Resource | `glinet_ddns` | [docs/resources/ddns.md](docs/resources/ddns.md) |
| Resource | `glinet_dns` | [docs/resources/dns.md](docs/resources/dns.md) |
| Resource | `glinet_firewall_dmz` | [docs/resources/firewall_dmz.md](docs/resources/firewall_dmz.md) |
| Resource | `glinet_firewall_port_forward` | [docs/resources/firewall_port_forward.md](docs/resources/firewall_port_forward.md) |
| Resource | `glinet_firewall_rule` | [docs/resources/firewall_rule.md](docs/resources/firewall_rule.md) |
| Resource | `glinet_ipv6` | [docs/resources/ipv6.md](docs/resources/ipv6.md) |
| Resource | `glinet_lan` | [docs/resources/lan.md](docs/resources/lan.md) |
| Resource | `glinet_lan_static_lease` | [docs/resources/lan_static_lease.md](docs/resources/lan_static_lease.md) |
| Resource | `glinet_led_schedule` | [docs/resources/led_schedule.md](docs/resources/led_schedule.md) |
| Resource | `glinet_led` | [docs/resources/led.md](docs/resources/led.md) |
| Resource | `glinet_reboot_schedule` | [docs/resources/reboot_schedule.md](docs/resources/reboot_schedule.md) |
| Resource | `glinet_system_timezone` | [docs/resources/system_timezone.md](docs/resources/system_timezone.md) |
| Resource | `glinet_tailscale` | [docs/resources/tailscale.md](docs/resources/tailscale.md) |
| Resource | `glinet_wan` | [docs/resources/wan.md](docs/resources/wan.md) |
| Resource | `glinet_wifi_interface` | [docs/resources/wifi_interface.md](docs/resources/wifi_interface.md) |
| Resource | `glinet_wifi_radio` | [docs/resources/wifi_radio.md](docs/resources/wifi_radio.md) |
| Resource | `glinet_wireguard_client_config` | [docs/resources/wireguard_client_config.md](docs/resources/wireguard_client_config.md) |
| Resource | `glinet_wireguard_client_group` | [docs/resources/wireguard_client_group.md](docs/resources/wireguard_client_group.md) |
| Resource | `glinet_wireguard_server_peer` | [docs/resources/wireguard_server_peer.md](docs/resources/wireguard_server_peer.md) |
| Resource | `glinet_zerotier` | [docs/resources/zerotier.md](docs/resources/zerotier.md) |
| Data source | `glinet_arp_entries` | [docs/data-sources/arp_entries.md](docs/data-sources/arp_entries.md) |
| Data source | `glinet_clients` | [docs/data-sources/clients.md](docs/data-sources/clients.md) |
| Data source | `glinet_dns_secure_servers` | [docs/data-sources/dns_secure_servers.md](docs/data-sources/dns_secure_servers.md) |
| Data source | `glinet_firewall_zones` | [docs/data-sources/firewall_zones.md](docs/data-sources/firewall_zones.md) |
| Data source | `glinet_system_info` | [docs/data-sources/system_info.md](docs/data-sources/system_info.md) |
| Data source | `glinet_system_status` | [docs/data-sources/system_status.md](docs/data-sources/system_status.md) |
| Data source | `glinet_system_timezone` | [docs/data-sources/system_timezone.md](docs/data-sources/system_timezone.md) |
| Data source | `glinet_wan_status` | [docs/data-sources/wan_status.md](docs/data-sources/wan_status.md) |
| Data source | `glinet_wifi_status` | [docs/data-sources/wifi_status.md](docs/data-sources/wifi_status.md) |

The provider is tested with a GL-AXT1800 on firmware 4.7.0 and 4.8.3. Other models and firmware versions can have different API methods.

## Requirements

- [Go](https://go.dev/doc/install) 1.27 (see `.tool-versions`)
- [Terraform](https://developer.hashicorp.com/terraform/install) 1.16 or later, or [OpenTofu](https://opentofu.org/docs/intro/install/). The tests and `make generate` need the CLI.
- [golangci-lint](https://golangci-lint.run/) v2 for `make lint`

## Developing the provider

| Command | Result |
|---|---|
| `make build` | Build the provider. |
| `make install` | Build the provider and install it in `$GOPATH/bin`. |
| `make fmt` | Format the Go code. |
| `make lint` | Run golangci-lint. |
| `make generate` | Format the examples and generate `docs/` with tfplugindocs. |
| `make test` | Run the tests on a fake router. |
| `make testacc` | Run the tests with `TF_ACC=1`. |

To add a dependency, run `go get github.com/author/dependency` and `go mod tidy`, then commit `go.mod` and `go.sum`.
Documentation tools are in the separate module `tools/`.

## Tests

The tests run Terraform with the provider in the test process (`resource.UnitTest` with `ProtoV6ProviderFactories`).
The provider connects to a fake router from the package [`glinettest`](https://pkg.go.dev/github.com/ryanrishi/glinet-client-go/glinettest), which runs in the same process.
The tests need no router and no `TF_ACC`.

To run the tests with OpenTofu, set these environment variables:

```shell
TF_ACC_TERRAFORM_PATH=$(which tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org TF_ACC_PROVIDER_NAMESPACE=hashicorp make test
```

### Tests on a real router

When `TF_ACC=1` and `GLINET_HOST` are set, the main test of each data source and resource uses the router at `GLINET_HOST` instead of the fake router.
The provider gets the credentials from `GLINET_USERNAME` and `GLINET_PASSWORD`.
Because the values on a router are not fixed, the data source test checks only the types and that the values are not empty.
The resource tests change the router and put back the initial values when they end.
The other tests, for example the tests for errors and drift, always use the fake router.

```shell
GLINET_HOST=http://192.168.8.1 GLINET_USERNAME=root GLINET_PASSWORD=... make testacc
```

Tests on a real router run locally only. A test that changes the router must put the router back into its initial state.
