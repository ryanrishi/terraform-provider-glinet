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
make vet32      # go vet for GOOS=linux with GOARCH=386 and GOARCH=arm (32-bit)
make generate   # terraform fmt on examples/ and tfplugindocs into docs/ (runs in tools/)
make test       # tests on the fake router, no TF_ACC needed; 20-minute limit
make testacc    # TF_ACC=1; with GLINET_HOST set, the tests that call realRouter() or guardedRealRouter use the real router
```

Go version: `.tool-versions` and `go.mod`. `make generate` and the tests need `terraform` (or `tofu`) on `PATH`.

Add a dependency with `go get github.com/author/dependency` and `go mod tidy`. Documentation tools are in the separate module `tools/`.

## Architecture

- `main.go` serves the provider with `providerserver.Serve`.
- `internal/provider/provider.go`: provider schema and `Configure`.
  - `host` (default `http://192.168.8.1`), `username` (default `root`), `password` (sensitive). Each one can come from `GLINET_HOST`, `GLINET_USERNAME`, `GLINET_PASSWORD`.
  - `Configure` calls `glinet.NewClientWithHost`, which logs in. A login error becomes a diagnostic.
  - Data sources and resources get the `*glinet.Client` from `ProviderData`.
- Client: `github.com/ryanrishi/glinet-client-go`. Service methods take a `context.Context`. `Client.Call(ctx, module, method, params, result)` calls methods that have no typed method. Router errors are `*glinet.RPCError`; an HTTP status other than 200 is `*glinet.HTTPError` (`StatusCode`, `Module`, `Method`). The client logs in again one time on `-32000`. Each call has a time limit of 60 seconds (`glinet.WithHTTPClient` replaces the HTTP client). Router IDs and metrics of WireGuard groups, peers and routes are `int64`. `*bool` fields (`DMZ.Priority`, `LocalAccessConfig.SSHEnabled`, `WireGuardServerConfig.IPv6Enabled`) are nil when the router does not return the key, and the client then does not send it.
- Data sources (`<name>_data_source.go`):
  - `glinet_system_timezone`, `glinet_system_info` (no serial numbers and no DDNS device ID), `glinet_system_status` (no Wi-Fi keys).
  - `glinet_clients`: all fields of `clients.get_list`, also `last_rx` and `last_tx`, whose meaning is not known; `download_limit` and `upload_limit` (`limit_rx` and `limit_tx` of the router) are Float64 (KiB/s); `icon` is `class` of the router.
  - `glinet_firewall_zones`, `glinet_arp_entries`, `glinet_wifi_status`.
  - `glinet_wan_status`: combines `cable.get_status`, `network.check_wan_cable` and `lan.get_wan_info`; `second_wan` comes from `cable.get_status` only, with null `mode` and `status` while the router gives an error code; no `network.check_wan_cable` call for the second port.
  - `glinet_multiwan_status`: `kmwan.get_status`.
  - `glinet_repeater_status` (`repeater.get_status`; no Wi-Fi key, EAP identity and `wpa_status` values), `glinet_repeater_saved_networks` (`repeater.get_saved_ap_list`; no Wi-Fi keys and EAP identities), `glinet_tethering_status` (`tethering.get_status`; read on a router only without a phone on USB).
  - `glinet_dns_secure_servers`: `dns.get_info`, the names for `servers` of `glinet_dns` in mode `secure`.
  - `glinet_fan_status`: `fan.get_status`.
  - `glinet_parental_control_apps`: `parental-control.get_app_list`, the IDs for `apps` of `glinet_parental_control_ruleset`.
- `dataSourceClient` in `provider.go` gives a data source the client in `Configure`. `data_source_attributes.go` has the schema helpers of the data sources, and `codeName`, which maps a router code to a name of a list (null for a code out of the list; also used by `glinet_repeater`).
- Resources (`<name>_resource.go`; "singleton" means one instance for each router, with a fixed ID):
  - System and hardware:
    - `glinet_system_timezone` (singleton), `glinet_led` (singleton).
    - `glinet_led_schedule` (singleton; the router keeps times and days only from an enabled write, so a disabled write first writes the schedule enabled). `schedule.go` has the time and day helpers of the schedules.
    - `glinet_reboot_schedule` (singleton; keeps `time` and `days` of the state while the router reports the schedule as disabled; destroy disables the schedule with the write `enable: false`).
    - `glinet_fan`, `glinet_usb`, `glinet_screen_schedule`, `glinet_switch_button` (singletons): `hardware.go` `requireHardware` reads `hardware_feature` of `system.get_info` before each write, and create and update fail without a write on a router without the hardware. Only the fan write was tested on a router. `glinet_screen_schedule` works like `glinet_led_schedule`. `glinet_switch_button` checks `function` and `sub_function` against `switch-button.get_funcs` before each write and always sends `sync: false`.
  - Clients and access control:
    - `glinet_client_block` and `glinet_client_allow` (`client_block_resource.go`, `client_allow_resource.go`; the shared code is in `client_list_resource.go`): one MAC in the block list or the allow list of `black_white_list`; ID is the MAC; `mac` in any letter case, import sets lower case; writes upper case and matches entries without letter case; a change of only the letter case is an update without a write. `glinet_client_block` create and delete fail when the router is not in block-list mode (a block-list write in allow-list mode was not tested). `glinet_client_allow` also writes in block-list mode (tested on a router), and its destroy fails for the last allow-list entry in allow-list mode.
    - `glinet_access_control` (singleton; `mode` `block_list` or `allow_list` maps to `black` or `white`; a mode change sends `set_config` with the router's current list of the new mode; refuses a change to `allow_list` with an empty allow list).
    - `glinet_client_info`, `glinet_client_speed_limit`: ID is the MAC; find the client in `clients.get_list` without letter case and write the MAC in the letter case of the list; create fails before a write when the client is not in the list; refresh removes the resource when the client leaves the list. `glinet_client_speed_limit` create also fails when the client has a limit; destroy removes the limit; `glinet_client_info` destroy leaves the name and the icon.
    - `glinet_lan_static_lease`: ID is the MAC; `mac` in any letter case with case-insensitive semantic equality; finds the binding exactly, else without letter case, and writes with the router's MAC; update writes to the router binding of the state and fails before a write when the planned `mac` selects another binding; import sets the router's letter case.
  - Firewall and router access:
    - `glinet_firewall_port_forward`: identity is `external_zone` + `protocol` + `external_port`; the router ID is looked up before each call and is not in the state.
    - `glinet_firewall_port_forward_order` (singleton; `port_forwards` lists port forward identities, not router IDs; the write sends the IDs of all port forwards, listed ones first, and sends nothing when the order is already correct; the read compares only the first N port forwards of the router; import reads all port forwards; `-32601` (firmware 4.7.0) gives a clear error).
    - `glinet_firewall_dmz` (singleton; destroy disables the DMZ on the router; `higher_priority` is `priority`, sent only in an enabled write; when it is not set, the write sends back the router value, and no `priority` when the router reports none (before the first write); the read back is compared only when the provider sent a priority; the state keeps it while the router reports the DMZ as disabled).
    - `glinet_firewall_rule`: identity is the eight match fields; the router ID is looked up before each call and is not in the state; import by name. `source_ip` and `destination_ip` take IPv4 and IPv6 addresses and CIDR blocks; `protocol` takes a lower-case protocol name or number (`protocolName`); `source_mac` is a `caseInsensitiveString` and matches without letter case. Firmware 4.8.3 returns HTTP status 500 for `set_rule` and `remove_rule` of a rule with no destination port: `ModifyPlan` warns when a plan creates, changes or destroys such a rule, and `noDestinationPortFault` adds the `uci` removal over SSH to a failed write when `isNoDestinationPortFault` finds that fault (a `*glinet.HTTPError` with status 500 from `firewall.set_rule` or `firewall.remove_rule`). For firmware 4.7.0, Delete first writes the destination port `65535` to such a rule, then removes it.
    - `glinet_wan_access` (singleton; sends all attributes; destroy disables the access from the WAN with `false` values and an empty `allow_list`).
    - `glinet_local_access` (singleton; sends all attributes, also the LuCI ones; checks that the five ports differ, also against router values; a change of `http_port`, `https_port` or `redirect_https_enabled` moves the API, so the provider saves the state before the read back, warns, and does not fail when the read back fails or times out after 5 seconds; `ssh_enabled` is null when the router does not return it (firmware 4.7.0), and a configured `ssh_enabled` then fails before the write).
  - Parental control (`parental_control.go` has the shared helpers):
    - `glinet_parental_control` (singleton; destroy disables parental control and keeps `drop_anonymous_enabled`, `auto_update_enabled`, the groups and the rule sets).
    - `glinet_parental_control_group`, `glinet_parental_control_ruleset`: `id` is the router ID and is in the state; `findParentalControlItem` falls back to the only item with the name of the state; import by name. The group resource refuses a name that another group has and writes the schedules only when they differ; the router IDs of the schedules are not in the state. The rule set resource has schema version 1 (the upgrader moves `blacklist` and `url` of a 0.2.0 state to `block_list` and `block_list_url`, because Read keeps the URL of the state when the router returns none) and does not manage the presets `drop` and `accept`; destroy fails while a group uses the rule set. The temporary rule set of a group is not managed.
  - DNS and VPN services:
    - `glinet_dns` (singleton; modes `auto`, `manual`, `proxy`, `secure`, and only `auto` writes were tested on a router; without a configured `mode` it writes only the three flags; `ConfigValidators` sets which attributes each mode, `protocol` and `dot_provider` require or forbid; `servers` is the user's value in `manual`, `proxy` and `secure` with `DoH`, `DNSCrypt`, `oDoH`, and router data without a diff in `auto`, `DoT` and without `mode`; `ModifyPlan` plans null for `protocol`, `dot_provider`, `dot_resolver_id` that the configured mode does not have; `dot_provider` maps `cloudflare`, `nextdns`, `controld`, `controld_free` to `"0"`-`"3"`; `dot_resolver_id` is written to and read from only the ID field of that provider).
    - `glinet_adguardhome` (singleton).
    - `glinet_ddns` (singleton; destroy disables DDNS).
    - `glinet_tailscale` (singleton; destroy disables Tailscale; the login is not managed; `exit_node_ip` is IPv4 or IPv6).
    - `glinet_zerotier` (singleton; `network_id` in any letter case with case-insensitive semantic equality; the router ignores the network ID and the flags in a disabled write; destroy disables ZeroTier and cannot remove the network ID).
  - WAN, LAN and Wi-Fi (`network_config.go` has the helpers of the network configuration resources, also `untestedWriteNote` and `destroyNote`):
    - `glinet_wan`: one per wired WAN port, ID is `port` (`wan` or `secondwan`); schema version 1, the upgrader sets `port` from the ID; for `secondwan` it reads `system.get_info` before each call and fails when `software_feature.secondwan` is false; full replace of `cable.set_config`.
    - `glinet_wan_port`: one per wired WAN port, ID is `port` (as `glinet_wan`), and Read finds the port by the ID; `cable.set_port_config` (role and MAC address); create takes over the port; an unset MAC attribute gets the router value before the write; refuses a change from `wan` to `lan` while `network.check_wan_cable` reports `cable_enabled`; the same `secondwan` check as `glinet_wan`.
    - `glinet_multiwan` (singleton; writes only on a difference; a failover write sends only metrics, a load balance write only weights, so `priority` is read only in failover mode and `weights` only in load balance mode), `glinet_multiwan_interface` (one per interface, ID is `interface`; create takes over the interface; sends the tracking fields as the web UI does), `glinet_multiwan_sensitivity` (singleton; level `medium` and 5 seconds when the router gives no sensitivity).
    - `glinet_repeater` (singleton; switching mode and band lock only; does not connect and does not change saved networks; fails when the read back differs from the write).
    - `glinet_network_advanced` (singleton; Full Cone NAT and SIP ALG, both sent in each write), `glinet_igmp_snooping` (singleton; `enabled` and `version` sent in each write).
    - `glinet_ipv6` (singleton; a disabled write sends only `enable: false`, and the state keeps the other attributes while the router reports IPv6 as disabled; a `lan_dns` list with one server sends only `lan_dns1`, and the read then ignores the second DNS server of the router).
    - `glinet_lan`: one per network `lan` or `guest`, ID is `name`; create takes over the network; sends the address group and the DHCP group only when they differ from the state, or on create from the router values, because each write restarts the router's Wi-Fi; compares lease times by duration; `wan_isolation_enabled` (guest only, null for `lan`) and `dhcp_lpr_servers` are Optional and Computed: an unknown plan value gets the router value before the write; `dhcp_lpr_servers = []` removes all LPR servers.
    - `glinet_wifi_interface`: one per Wi-Fi interface, ID is `name`; create takes over the interface; sends the settings call and the switch call only when they differ from the state, or on create from the router values; a main interface write sends back the radio values read under `routerWrites`, also `chan_6g_only_psc` of a 6G radio; `encryption` is checked at apply time against `encryptions` of the radio, not in the schema.
    - `glinet_wifi_radio`: one per radio, ID is `name`, the device such as `radio0`; create takes over the radio; an attribute that is not set gets the router value before the write; `tx_power` is lower case and `routerTxPower` maps it to the router value (`Max`, `High`, `Medium`, `Low`); a change of only `tx_power` sends `wifi.set_txpower`, every other change sends `wifi.set_config` on the main interface with the SSID, encryption, hidden flag and key read from the router under `routerWrites`; sends `channel` and `htmode` only when one of them changes; checks mode, width and channel against the lists of the radio at apply time; `channel` 0 is automatic.
  - WireGuard (`wireguard.go` has the shared helpers):
    - `glinet_wireguard_client_group`: a custom group (type 2); `id` is the router's `group_id` and is in the state; destroy fails while the group has configurations.
    - `glinet_wireguard_client_config`: a peer of `wg-client` in a custom group; each read, import and delete also rejects a group that is not custom; `id` is the router's `peer_id`; import `<group_id>/<id>`; `ipv6_enable` and `presharedkey_enable` come from `address_v6` and `preshared_key`.
    - `glinet_wireguard_server` (singleton, ID `wireguard_server`; create takes over the server and, when it is not initialized, calls `wg-server.generate_key`; the resource does not exist while the router reports the server as not initialized; sends the configuration call and the options call only on a difference; sends `ipv6_enable` only when the router returned it and `address_v6` is not empty; a configuration write fails while the server runs; `private_key` is in the state).
    - `glinet_wireguard_server_peer`: `id` is the router's `peer_id`; create fails when the server is not initialized; `client_config` (computed, `private_key` and `preshared_key` sensitive) comes from `generate_peer` after create and update, and in a read only when the state has no `client_config` (import), because it is not known whether `generate_peer` makes new keys.
    - `glinet_wireguard_server_route`: identity is `destination` (`dest` in the router API); schema version 1, the upgrader moves `dest` of a 0.2.0 state to `destination` (`renameAttributes` in `state_upgrade.go`); the router `rule_id` is looked up before each call and is not in the state; create fails when a route rule for `destination` exists or the server is not initialized; sends `gateway`, `metric` and `mtu` with `""` and `0` when not set; `metric` is 1 to 4294967295; import by `destination`.
    - `addCreateError` adds to a create error of a group or a configuration that the router may have created the object, because the client error does not tell the add call from the list read after it. The optional `dns`, `listen_port`, `persistent_keepalive`, `mtu` (and `allowed_ips` of the server peer) are Optional and Computed: an update sends back the state value of an attribute that is not set, so removing it from the configuration keeps the router value; the client sends `""` for a number of 0 and does not send an empty `dns` or `allowed_ips`, and what the router does with `""` is not known. The resources do not start or stop a tunnel or the server.
- `resourceClient` in `provider.go` gives a resource the client in `Configure`. `routerWrites` in `provider.go` serializes each read-check-write sequence. `case_insensitive_string.go` has `caseInsensitiveString` (semantic equality without letter case) and `requiresReplaceIfNotLetterCase`.
- Option coverage: a resource supports each option that the router web UI can send, not only the options of the test router. The options that come only from the web UI code of firmware 4.8.3 are tested only on the fake router, and each resource description says so (`untestedWriteNote`). Writes of the web UI options tested on a GL-AXT1800 with firmware 4.8.3: `fan.set_config`, an allow-list write in block-list mode (client `REPORT.md`), and `glinet_firewall_rule` with IPv6 addresses, CIDR blocks, `esp`, `icmpv6` and an upper-case MAC (the nftables rule set had the rules).
- Access warning: the provider description in `provider.go` and `README.md` list each resource whose description warns that it can cut the access to the router or its Internet access. Keep the two lists the same.
- Destroy rule: destroy disables a feature that exposes the router or acts on its own (`glinet_firewall_dmz`, `glinet_wan_access`, `glinet_ddns`, `glinet_tailscale`, `glinet_zerotier`, `glinet_reboot_schedule`, `glinet_parental_control`), removes a list item (`glinet_lan_static_lease`, `glinet_client_block`, `glinet_client_allow`, `glinet_client_speed_limit`, `glinet_firewall_port_forward`, `glinet_firewall_rule`, `glinet_parental_control_group`, `glinet_parental_control_ruleset`, `glinet_wireguard_client_group`, `glinet_wireguard_client_config`, `glinet_wireguard_server_peer`, `glinet_wireguard_server_route`), and leaves a plain setting unchanged (the other resources). Each resource description states its destroy behaviour, and the provider description in `provider.go` has the same lists.

## Router API References

- `REPORT.md` in `testdata/fixtures/axt1800_4.8.3/` of `glinet-client-go`: the read results of firmware 4.8.3, the write formats that come from its web UI code (sections "(web UI code)"), the read shapes and the writes that were tested on a router ("Read shapes confirmed on the router", "Writes tested on the router"), and the cellular modem ("Cellular modem (not built)").
- `REPORT.md` in `testdata/fixtures/axt1800_4.7.0/` of `glinet-client-go`: the verified behaviour of a GL-AXT1800 on firmware 4.7.0, and for each write method whether it was tested on a router. Highest authority.
- [Archived official reference, 2023-09](https://web.archive.org/web/20230909030901if_/https://dev.gl-inet.com/router-4.x-api/docs/api_docs_api/?version=4): parameters and examples of 55 modules. Authority for a method until it is verified on a router.
- [HarvsG/gli4py](https://github.com/HarvsG/gli4py) and [angolo40/GLiNet_HomeAssistant](https://github.com/angolo40/GLiNet_HomeAssistant): other clients, for login variants, firewall calls and VPN calls.
- [shauneccles/glinet-registry](https://github.com/shauneccles/glinet-registry): method probe of an MT6000 on firmware 4.9.0. GPL-3.0: read only, do not copy content.

## Resource Conventions

- Each resource has import.
- Attribute names: an on/off switch ends in `_enabled`; a MAC address is `mac` or `*_mac`; lists of permitted and blocked items are `allow_list` and `block_list` (not whitelist and blacklist); full words, not abbreviations of the router (`destination`, not `dest`); a readable name when the router key is not one, with the router key in the description (`` (`key` in the router API) ``); enumeration values in lower case, except standard names such as `DoH` and the firewall targets `ACCEPT`, `DROP`, `REJECT`.
- Secrets (Wi-Fi key, private key, preshared key, password) are `Sensitive`.
- Router API numbers that mean true or false (0 or 1) are `Bool` in the schema.
- A resource does not start, stop or restart a service unless the router does it as part of the write.
- A new router method is client work first (typed method, `glinettest` handler, fixtures), then a client release, then provider work.
- A resource that can cut the access to the router or its Internet access has a warning in its description, preferably a `~> **Warning:**` block after the first paragraph, and is in the access warning lists of `provider.go` and `README.md`.
- A hardware setting calls `requireHardware` before each write.
- `README.md` has one table row for each registered resource and data source, in alphabetical order.

Not built:

- Network mode (`netmode.set_mode`): the router changes its address.
- A resource that connects the repeater or USB tethering (`repeater.connect`, `tethering.set_connect`): the router has no call that saves a network without a connect, so a write would switch the uplink at once.
- Bark, and the temporary rule set of parental control.
- MLO networks of Wi-Fi 7 routers (`wifi.get_mlo_config`, `wifi.set_mlo_config`; issue 127) and the cellular modem (`modem`, `mvas`; issue 128): they need API results of routers that the maintainer does not have.
- The WireGuard client groups of VPN service providers: the calls send account data to a third-party service, and the web UI code does not settle their parameters.
- VPN policy, and tunnel start and stop.
- OpenVPN client and server, and `vpn-client`, which replaces `wg-client` on firmware 4.9.0.
- The QoS configuration: no view of the web UI writes it.
- The admin password.
- A DNS hosts resource (`dns.get_host`, `dns.set_host`): in the write test, the router did not use a new hosts file within 5 minutes.
- The Wi-Fi schedule.

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
- `realRouter()` is true when `TF_ACC=1` and `GLINET_HOST` are set. Then the main test of a data source or resource that calls `realRouter()` or `guardedRealRouter` uses `provider "glinet" {}` (credentials from the environment). Data source tests check only types and empty values. Resource tests restore the initial router values in `t.Cleanup`. Other tests (errors, drift) always use the fake router.
  - Data sources with a real-router test: `glinet_arp_entries`, `glinet_clients`, `glinet_dns_secure_servers`, `glinet_firewall_zones`, `glinet_system_info`, `glinet_system_status`, `glinet_system_timezone`, `glinet_wan_status`, `glinet_wifi_status`.
  - Resources with a real-router test: `glinet_adguardhome`, `glinet_client_block`, `glinet_ddns`, `glinet_dns`, `glinet_firewall_dmz`, `glinet_firewall_port_forward`, `glinet_firewall_rule`, `glinet_lan_static_lease`, `glinet_led`, `glinet_led_schedule`, `glinet_reboot_schedule`, `glinet_system_timezone`, `glinet_tailscale`, `glinet_zerotier`.
  - The tests of all other data sources and resources always use the fake router: the network configuration resources (`glinet_wan`, `glinet_wan_port`, `glinet_ipv6`, `glinet_lan`, `glinet_wifi_interface`, `glinet_wifi_radio`), multi-WAN, repeater and tethering, access control (`glinet_access_control`, `glinet_client_allow`), clients (`glinet_client_info`, `glinet_client_speed_limit`), remote and local access, `glinet_firewall_port_forward_order`, `glinet_network_advanced`, `glinet_igmp_snooping`, the hardware settings, parental control, and WireGuard. Most of their writes were not tested on a router.
- `guardedRealRouter(t, allow)` puts a proxy in front of the real router that sends a call only when `allow` accepts its module, method and params. The real-router tests of `glinet_reboot_schedule` use it so that only `enable: false` can be written, those of `glinet_dns` so that only `rebind_protection` (`rebind_protection_enabled`) can change (the tests of the other DNS modes always use the fake router), those of `glinet_adguardhome` so that AdGuard Home stays disabled, and those of `glinet_ddns`, `glinet_tailscale`, `glinet_zerotier` and `glinet_client_block` so that only the writes of the test can reach the router.
- `network_config_test.go`: `recordCalls(t, srv, module, method)` records the params of each call of a fake router method, and `expect` / `expectUnordered` check them after a step; `expectRouterJSON` and `saveRouterJSON` compare a fake router result.
- `make test` runs the tests with `-parallel=8` and a time limit of 20 minutes.
- The release builds 32-bit platforms (`386`, `arm`), where `int` has 32 bits. `make vet32` runs `go vet` for them; CI runs it too.
- Each test starts with `parallel(t)` (`provider_test.go`). On the fake router the tests then run in parallel. On the real router `parallel(t)` does nothing, so each test runs alone.
  - A test that uses `t.Setenv` does not call `parallel(t)`: Go does not permit `t.Setenv` in a parallel test.
  - Tests do not call `t.Parallel()` directly, and subtests are not parallel.
  - A test keeps its state in its own `glinettest.Server` and in local variables. Package-level test variables are read-only.
- With OpenTofu: set `TF_ACC_TERRAFORM_PATH`, `TF_ACC_PROVIDER_HOST=registry.opentofu.org`, `TF_ACC_PROVIDER_NAMESPACE=hashicorp`.

CI (`.github/workflows/test.yml`): build, `make vet32` and lint; `make generate` with a no-diff check; `make test` on Terraform 1.16, 1.17 and the latest OpenTofu, each job with a limit of 30 minutes. Release (`release.yml`): GoReleaser with GPG signing on `v*` tags; the job fails when the tagged commit is not in `main`.
