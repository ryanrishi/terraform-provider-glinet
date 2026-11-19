# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

This is a Terraform provider for GL.iNet routers that run v4 firmware. It is published at `registry.terraform.io/ryanrishi/glinet`.

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
- Data sources: `glinet_system_timezone` (`system_timezone_data_source.go`), `glinet_system_info` (`system_info_data_source.go`; no serial numbers and no DDNS device ID), `glinet_system_status` (`system_status_data_source.go`; no Wi-Fi keys), `glinet_clients` (`clients_data_source.go`; all fields of `clients.get_list`, also `last_rx` and `last_tx`, whose meaning is not known), `glinet_firewall_zones` (`firewall_zones_data_source.go`), `glinet_wan_status` (`wan_status_data_source.go`; combines `cable.get_status`, `network.check_wan_cable` and `lan.get_wan_info`; `second_wan` comes from `cable.get_status` only, with null `mode` and `status` while the router gives an error code; no `network.check_wan_cable` call for the second port), `glinet_arp_entries` (`arp_entries_data_source.go`), `glinet_wifi_status` (`wifi_status_data_source.go`), `glinet_dns_secure_servers` (`dns_secure_servers_data_source.go`; `dns.get_info`, the names for `servers` of `glinet_dns` in mode `secure`).
- `dataSourceClient` in `provider.go` gives a data source the client in `Configure`. `data_source_attributes.go` has the schema helpers of the data sources.
- Resources: `glinet_system_timezone` (`system_timezone_resource.go`, singleton), `glinet_lan_static_lease` (`lan_static_lease_resource.go`, ID is the MAC; `mac` in any letter case with case-insensitive semantic equality; finds the binding exactly, else without letter case, and writes with the router's MAC; update writes to the router binding of the state and fails before a write when the planned `mac` selects another binding; import sets the router's letter case), `glinet_firewall_port_forward` (`firewall_port_forward_resource.go`, identity is `external_zone` + `protocol` + `external_port`; the router ID is looked up before each call and is not in the state), `glinet_firewall_dmz` (`firewall_dmz_resource.go`, singleton; destroy disables the DMZ on the router; `higher_priority` is `priority`, sent only in an enabled write; when it is not set, the write sends back the router value; the state keeps it while the router reports the DMZ as disabled), `glinet_firewall_port_forward_order` (`firewall_port_forward_order_resource.go`, singleton; `port_forwards` lists port forward identities, not router IDs; the write sends the IDs of all port forwards, listed ones first, and sends nothing when the order is already correct; the read compares only the first N port forwards of the router; import reads all port forwards; `-32601` (firmware 4.7.0) gives a clear error), `glinet_firewall_rule` (`firewall_rule_resource.go`, identity is the eight match fields; the router ID is looked up before each call and is not in the state; import by name), `glinet_led` (`led_resource.go`, singleton), `glinet_led_schedule` (`led_schedule_resource.go`, singleton; `schedule.go` has the time and day helpers of the schedules), `glinet_reboot_schedule` (`reboot_schedule_resource.go`, singleton; keeps `time` and `days` of the state while the router reports the schedule as disabled; destroy disables the schedule with the write `enable: false`), `glinet_dns` (`dns_resource.go`, singleton; modes `auto`, `manual`, `proxy`, `secure`, and only `auto` writes were tested on a router; without a configured `mode` it writes only the three flags; `ConfigValidators` sets which attributes each mode, `protocol` and `dot_provider` require or forbid; `servers` is the user's value in `manual`, `proxy` and `secure` with `DoH`, `DNSCrypt`, `oDoH`, and router data without a diff in `auto`, `DoT` and without `mode`; `ModifyPlan` plans null for `protocol`, `dot_provider`, `dot_resolver_id` that the configured mode does not have; `dot_provider` maps `cloudflare`, `nextdns`, `controld`, `controld_free` to `"0"`-`"3"`; `dot_resolver_id` is written to and read from only the ID field of that provider), `glinet_adguardhome` (`adguardhome_resource.go`, singleton), `glinet_client_block` (`client_block_resource.go`, `mac` in any letter case, import sets lower case; writes upper case and matches block list entries without letter case; a change of only the letter case is an update without a write; create and delete fail when the router is not in block-list mode), `glinet_client_allow` (`client_allow_resource.go`, like `glinet_client_block` for the allow list; destroy fails for the last allow-list entry in allow-list mode; `client_list_resource.go` has the shared code of the two), `glinet_access_control` (`access_control_resource.go`, singleton; `mode` `block_list` or `allow_list` maps to `black` or `white`; a mode change sends `set_config` with the router's current list of the new mode; refuses a change to `allow_list` with an empty allow list), `glinet_ddns` (`ddns_resource.go`, singleton; destroy disables DDNS), `glinet_tailscale` (`tailscale_resource.go`, singleton; destroy disables Tailscale; the login is not managed), `glinet_zerotier` (`zerotier_resource.go`, singleton; `network_id` in any letter case with case-insensitive semantic equality; the router ignores the network ID and the flags in a disabled write; destroy disables ZeroTier and cannot remove the network ID), `glinet_wan` (`wan_resource.go`, one per wired WAN port, ID is `port` (`wan` or `secondwan`); schema version 1, the upgrader sets `port` from the ID; for `secondwan` it reads `system.get_info` before each call and fails when `software_feature.secondwan` is false; full replace of `cable.set_config`; `network_config.go` has the helpers of the network configuration resources), `glinet_ipv6` (`ipv6_resource.go`, singleton; a disabled write sends only `enable: false`, and the state keeps the other attributes while the router reports IPv6 as disabled; a `lan_dns` list with one server sends only `lan_dns1`, and the read then ignores the second DNS server of the router), `glinet_lan` (`lan_resource.go`, one per network `lan` or `guest`, ID is `name`; create takes over the network; sends the address group and the DHCP group only when they differ from the state, or on create from the router values, because each write restarts the router's Wi-Fi; compares lease times by duration; `wan_isolation` (guest only, null for `lan`) and `dhcp_lpr_servers` are Optional and Computed: an unknown plan value gets the router value before the write; `dhcp_lpr_servers = []` removes all LPR servers), `glinet_wifi_interface` (`wifi_interface_resource.go`, one per Wi-Fi interface, ID is `name`; create takes over the interface; sends the settings call and the switch call only when they differ from the state, or on create from the router values; a main interface write sends back the radio values read under `routerWrites`, also `chan_6g_only_psc` of a 6G radio), `glinet_wifi_radio` (`wifi_radio_resource.go`, one per radio, ID is `name`, the device such as `radio0`; create takes over the radio; an attribute that is not set gets the router value before the write; a change of only `tx_power` sends `wifi.set_txpower`, every other change sends `wifi.set_config` on the main interface with the SSID, encryption, hidden flag and key read from the router under `routerWrites`; sends `channel` and `htmode` only when one of them changes; checks mode, width and channel against the lists of the radio at apply time; `channel` 0 is automatic), `glinet_wireguard_client_group` (`wireguard_client_group_resource.go`, a custom group (type 2); `id` is the router's `group_id` and is in the state; destroy fails while the group has configurations), `glinet_wireguard_client_config` (`wireguard_client_config_resource.go`, a peer of `wg-client` in a custom group; each read, import and delete also rejects a group that is not custom; `id` is the router's `peer_id`; import `<group_id>/<id>`; `ipv6_enable` and `presharedkey_enable` come from `address_v6` and `preshared_key`), `glinet_wireguard_server_peer` (`wireguard_server_peer_resource.go`, `id` is the router's `peer_id`; create fails when the server is not initialized; `client_config` (computed, `private_key` and `preshared_key` sensitive) comes from `generate_peer` after create and update, and in a read only when the state has no `client_config` (import), because it is not known whether `generate_peer` makes new keys). `wireguard.go` has the helpers of the WireGuard resources: `addCreateError` adds to a create error of a group or a configuration that the router may have created the object, because the client error does not tell the add call from the list read after it; the optional `dns`, `listen_port`, `persistent_keepalive`, `mtu` (and `allowed_ips` of the server peer) are Optional and Computed: an update sends back the state value of an attribute that is not set, so removing it from the configuration keeps the router value; the client sends `""` for a number of 0 and does not send an empty `dns` or `allowed_ips`, and what the router does with `""` is not known; the resources do not start or stop a tunnel.
- `resourceClient` in `provider.go` gives a resource the client in `Configure`. `case_insensitive_string.go` has `caseInsensitiveString` (semantic equality without letter case) and `requiresReplaceIfNotLetterCase`.
- Option coverage: a resource supports each option that the router web UI can send, not only the options of the test router. The options that come only from the web UI code of firmware 4.8.3 are tested only on the fake router, and each resource description says so.
- Destroy rule: destroy disables a feature that exposes the router or acts on its own (DMZ, DDNS, Tailscale, ZeroTier, reboot schedule), removes a list item (static lease, client block, client allow, port forward, firewall rule), and leaves a plain setting unchanged (the other resources). Each resource description states its destroy behaviour.

## Router API References

- `REPORT.md` in `testdata/fixtures/axt1800_4.8.3/` of `glinet-client-go`: the read results of firmware 4.8.3, and the write formats that come from its web UI code and were not tested on a router.
- `REPORT.md` in `testdata/fixtures/axt1800_4.7.0/` of `glinet-client-go`: the verified behaviour of a GL-AXT1800 on firmware 4.7.0, and for each write method whether it was tested on a router. Highest authority.
- [Archived official reference, 2023-09](https://web.archive.org/web/20230909030901if_/https://dev.gl-inet.com/router-4.x-api/docs/api_docs_api/?version=4): parameters and examples of 55 modules. Authority for a method until it is verified on a router.
- [HarvsG/gli4py](https://github.com/HarvsG/gli4py) and [angolo40/GLiNet_HomeAssistant](https://github.com/angolo40/GLiNet_HomeAssistant): other clients, for login variants, firewall calls and VPN calls.
- [shauneccles/glinet-registry](https://github.com/shauneccles/glinet-registry): method probe of an MT6000 on firmware 4.9.0. GPL-3.0: read only, do not copy content.

## Resource Conventions

- Each resource has import.
- Secrets (Wi-Fi key, private key, preshared key, password) are `Sensitive`.
- Router API numbers that mean true or false (0 or 1) are `Bool` in the schema.
- A resource does not start, stop or restart a service unless the router does it as part of the write.
- A new router method is client work first (typed method, `glinettest` handler, fixtures), then a client release, then provider work.

Not built:

- A DNS hosts resource (`dns.get_host`, `dns.set_host`): in the write test, the router did not use a new hosts file within 5 minutes.
- VPN policy, the WireGuard server configuration, tunnel start and stop: the client has the methods, and their writes were not tested on a router.
- OpenVPN client and server, the Wi-Fi schedule, and `vpn-client`, which replaces `wg-client` on firmware 4.9.0.
- MLO networks (`wifi.get_mlo_config`, `wifi.set_mlo_config`) and the WireGuard client groups of VPN service providers: the web UI code does not settle their parameters (see the 4.8.3 `REPORT.md` of the client).
- Router features with no resource: remote access over the WAN, local access (ports, HTTPS, SSH), client name and speed limit, multi-WAN, repeater, tethering and modem, network mode, NAT and SIP ALG, IGMP snooping, fan, USB 3, screen schedule, switch button, parental control, WireGuard server settings and routes.

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
- `guardedRealRouter(t, allow)` puts a proxy in front of the real router that sends a call only when `allow` accepts its module, method and params. The real-router tests of `glinet_reboot_schedule` use it so that only `enable: false` can be written, those of `glinet_dns` so that only `rebind_protection` can change (the tests of the other DNS modes always use the fake router), and those of `glinet_adguardhome` so that AdGuard Home stays disabled.
- `network_config_test.go`: `recordCalls(t, srv, module, method)` records the params of each call of a fake router method, and `expect` / `expectUnordered` check them after a step; `expectRouterJSON` and `saveRouterJSON` compare a fake router result. The tests of the network configuration resources (`glinet_wan`, `glinet_ipv6`, `glinet_lan`, `glinet_wifi_interface`, `glinet_wifi_radio`), of `glinet_access_control`, `glinet_client_allow` and `glinet_firewall_port_forward_order`, and of the WireGuard resources always use the fake router: their writes were not tested on a router.
- Each test starts with `parallel(t)` (`provider_test.go`). On the fake router the tests then run in parallel. On the real router `parallel(t)` does nothing, so each test runs alone.
  - A test that uses `t.Setenv` does not call `parallel(t)`: Go does not permit `t.Setenv` in a parallel test.
  - Tests do not call `t.Parallel()` directly, and subtests are not parallel.
  - A test keeps its state in its own `glinettest.Server` and in local variables. Package-level test variables are read-only.
- With OpenTofu: set `TF_ACC_TERRAFORM_PATH`, `TF_ACC_PROVIDER_HOST=registry.opentofu.org`, `TF_ACC_PROVIDER_NAMESPACE=hashicorp`.

CI (`.github/workflows/test.yml`): build and lint; `make generate` with a no-diff check; `make test` on Terraform 1.16, 1.17 and the latest OpenTofu. Release (`release.yml`): GoReleaser with GPG signing on `v*` tags.
