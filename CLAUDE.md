# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

This is a Terraform provider for GL.iNet routers that run v4 firmware. It is published at `registry.terraform.io/ryanrishi/glinet`. `PLAN.md` has the work plan.

## Development Commands

```bash
make build      # go build
make install    # go install
make fmt        # gofmt
make lint       # golangci-lint v2 (.golangci.yml)
make generate   # terraform fmt on examples/ and tfplugindocs into docs/ (runs in tools/)
make test       # tests on the fake router, no TF_ACC needed
make testacc    # TF_ACC=1; with GLINET_HOST set, the main data source and resource tests use the real router
```

Go version: `.tool-versions` and `go.mod`. `make generate` and the tests need `terraform` (or `tofu`) on `PATH`.

Add a dependency with `go get github.com/author/dependency` and `go mod tidy`. Documentation tools are in the separate module `tools/`.

## Architecture

- `main.go` serves the provider with `providerserver.Serve`.
- `internal/provider/provider.go`: provider schema and `Configure`.
  - `host` (default `http://192.168.8.1`), `username` (default `root`), `password` (sensitive). Each one can come from `GLINET_HOST`, `GLINET_USERNAME`, `GLINET_PASSWORD`.
  - `Configure` calls `glinet.NewClientWithHost`, which logs in. A login error becomes a diagnostic.
  - Data sources and resources get the `*glinet.Client` from `ProviderData`.
- Client: `github.com/ryanrishi/glinet-client-go`. Service methods take a `context.Context`. `Client.Call(ctx, module, method, params, result)` calls methods that have no typed method. Router errors are `*glinet.RPCError`. The client logs in again one time on `-32000`.
- Data sources: `glinet_system_timezone_config` (`system_timezone_config_data_source.go`), `glinet_system_info` (`system_info_data_source.go`; no serial numbers and no DDNS device ID), `glinet_system_status` (`system_status_data_source.go`; no Wi-Fi keys), `glinet_clients` (`clients_data_source.go`), `glinet_firewall_zones` (`firewall_zones_data_source.go`), `glinet_wan_status` (`wan_status_data_source.go`; combines `cable.get_status`, `network.check_wan_cable` and `lan.get_wan_info`), `glinet_arp_entries` (`arp_entries_data_source.go`), `glinet_wifi_status` (`wifi_status_data_source.go`).
- `dataSourceClient` in `provider.go` gives a data source the client in `Configure`. `data_source_attributes.go` has the schema helpers of the data sources.
- Resources: `glinet_system_timezone` (`system_timezone_resource.go`, singleton), `glinet_lan_static_lease` (`lan_static_lease_resource.go`, ID is the MAC), `glinet_firewall_port_forward` (`firewall_port_forward_resource.go`, identity is `external_zone` + `protocol` + `external_port`; the router ID is looked up before each call and is not in the state), `glinet_firewall_dmz` (`firewall_dmz_resource.go`, singleton; destroy disables the DMZ on the router), `glinet_firewall_rule` (`firewall_rule_resource.go`, identity is the eight match fields; the router ID is looked up before each call and is not in the state; import by name), `glinet_led` (`led_resource.go`, singleton), `glinet_led_schedule` (`led_schedule_resource.go`, singleton; `schedule.go` has the time and day helpers of the schedules), `glinet_reboot_schedule` (`reboot_schedule_resource.go`, singleton; keeps `time` and `days` of the state while the router reports the schedule as disabled), `glinet_dns` (`dns_resource.go`, singleton), `glinet_adguardhome` (`adguardhome_resource.go`, singleton), `glinet_client_block` (`client_block_resource.go`, ID is the MAC in lower case; writes upper case and matches block list entries without letter case; create and delete fail when the router is not in block-list mode), `glinet_ddns` (`ddns_resource.go`, singleton; destroy disables DDNS), `glinet_tailscale` (`tailscale_resource.go`, singleton; destroy disables Tailscale; the login is not managed), `glinet_zerotier` (`zerotier_resource.go`, singleton; the router ignores the network ID and the flags in a disabled write; destroy disables ZeroTier and cannot remove the network ID), `glinet_wan` (`wan_resource.go`, singleton; full replace of `cable.set_config`; `network_config.go` has the helpers of the network configuration resources).
- `resourceClient` in `provider.go` gives a resource the client in `Configure`.

## Adding Data Sources and Resources

1. Create `internal/provider/<name>_data_source.go` or `<name>_resource.go`.
2. Register it in `DataSources()` or `Resources()` in `provider.go`.
3. Add an example in `examples/data-sources/glinet_<name>/data-source.tf` or `examples/resources/glinet_<name>/resource.tf`.
4. Add tests (see below) and run `make generate`.

## Testing Architecture

Tests run Terraform with the provider in the test process:

- `resource.UnitTest` with `ProtoV6ProviderFactories` (`testProtoV6ProviderFactories` in `provider_test.go`). No `TF_ACC` needed.
- The provider connects to a fake router from `github.com/ryanrishi/glinet-client-go/glinettest`: `srv := glinettest.NewServer(t)`, then `fakeRouterConfig(srv)` gives the provider block.
  - `srv.System`, `srv.AdGuardHome`, `srv.LAN`: module state in memory, which tests can set.
  - `srv.Handle(module, method, m)`: replace a method, for example to return a `glinettest.Error`.
  - `srv.ExpireSessions()`: the next call gets `-32000`.
- Assertions use `ConfigStateChecks` with `statecheck` and `knownvalue`. Error paths use `ExpectError`.
- Data source tests use `readDataSource`, `expectAttributes` and `expectReadError` (`provider_test.go`). The real-router checks use `anyString`, `anyBool`, `anyInt64`, `nonEmptyString`, `positiveInt64`, and `orNull(check)` for a value that can be null.
- `realRouter()` is true when `TF_ACC=1` and `GLINET_HOST` are set. Then the main test of each data source and resource uses `provider "glinet" {}` (credentials from the environment). Data source tests check only types and empty values. Resource tests restore the initial router values in `t.Cleanup`. Other tests (errors, drift) always use the fake router.
- `guardedRealRouter(t, allow)` puts a proxy in front of the real router that sends a call only when `allow` accepts its module, method and params. The real-router tests of `glinet_reboot_schedule` use it so that only `enable: false` can be written, those of `glinet_dns` so that only `rebind_protection` can change, and those of `glinet_adguardhome` so that AdGuard Home stays disabled.
- `network_config_test.go`: `recordCalls(t, srv, module, method)` records the params of each call of a fake router method, and `expect` / `expectUnordered` check them after a step; `expectRouterJSON` and `saveRouterJSON` compare a fake router result. The tests of the network configuration resources (`glinet_wan`, `glinet_ipv6`, `glinet_lan_config`, `glinet_wifi_iface`) always use the fake router: their writes were not tested on a router.
- With OpenTofu: set `TF_ACC_TERRAFORM_PATH`, `TF_ACC_PROVIDER_HOST=registry.opentofu.org`, `TF_ACC_PROVIDER_NAMESPACE=hashicorp`.

CI (`.github/workflows/test.yml`): build and lint; `make generate` with a no-diff check; `make test` on Terraform 1.16, 1.17 and the latest OpenTofu. Release (`release.yml`): GoReleaser with GPG signing on `v*` tags.
