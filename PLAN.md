# Work plan: terraform-provider-glinet and glinet-client-go

Date: 2026-11-12

## Scope

Two repositories, one plan:

- `glinet-client-go` (`~/git/glinet-client-go`): Go client for the GL.iNet v4 JSON-RPC API.
- `terraform-provider-glinet` (this repository): Terraform provider that uses the client.

The dependency goes in one direction. Each item is client work first, then provider work.

## How the work is done

- One coordinator session plans, writes the briefs, and checks each diff.
- One Opus worker session for each repository. Two sessions never write to the same repository at the same time.
- Each piece of work is one branch and one PR.
- Worker sessions do not merge to `main`, do not tag releases, and do not push backup branches. The owner does merges and tags.
- The provider session develops against the local client checkout with a `go.work` file that is not committed. Before a provider PR is merged, `go.mod` must point to a client release tag and CI must pass without `go.work`.

## References

| Source | Use |
|---|---|
| `glinet-client-go/testdata/fixtures/axt1800_4.7.0/` and its `REPORT.md` | Real responses from the owner's router (GL-AXT1800, firmware 4.7.0). Highest authority. |
| [Archived official reference, 2023-09](https://web.archive.org/web/20230909030901if_/https://dev.gl-inet.com/router-4.x-api/docs/api_docs_api/?version=4) | 55 modules with parameters and examples. Authority for write methods until verified on the router. |
| [Archived API guide](https://web.archive.org/web/20240121142533/https://dev.gl-inet.com/router-4.x-api/) | Login flow and limits. |
| [HarvsG/gli4py](https://github.com/HarvsG/gli4py) | Maintained Python client. Reference for login variants. |
| [angolo40/GLiNet_HomeAssistant](https://github.com/angolo40/GLiNet_HomeAssistant) | Reference for firewall and VPN calls. |
| [shauneccles/glinet-registry](https://github.com/shauneccles/glinet-registry) | Method probe of an MT6000 on 4.9.0. GPL-3.0: read only, do not copy content. |
| [hashicorp/terraform-provider-scaffolding-framework](https://github.com/hashicorp/terraform-provider-scaffolding-framework) | Current provider layout to align with. |

## Verified facts (router probe)

- Login: `challenge` returns `alg: 1` (MD5-crypt) and no `hash_method`. The nonce is valid for 1000 ms.
- All calls: `{"method":"call","params":[sid, module, func, {args}]}`.
- All errors are a JSON-RPC `error` object. No response has `err_code` inside `result`.
- Unknown method: `-32601`. Invalid or expired session ID: `-32000 "Access denied"`.
- `wg-client`, `ovpn-client`, `wg-server`, `ovpn-server` exist on 4.7.0. `vpn-client` does not. On 4.9.0 the situation is the opposite for the client modules (not verified by us).
- `system.get_timezone_config` uses the field `autotimezone_enabled`.
- `lan.get_config_list` returns `interfaces[]`. `enable`, `ap_isolate`, `transfer_enable` are numbers (0 or 1). `leasetime` is a string such as `"12h"`.
- `adguardhome.get_config` returns `enabled` and `dns_enabled`.
- `wg-server.get_status` nests the server fields in `server{}`.
- `network.get_dhcp_leases`, `network.routes`, `network.routes6` do not exist on 4.7.0.

Not verified: all write methods, and the item fields of lists that were empty on the router (port forwards, firewall rules, static leases, WireGuard server peers).

## Test router

- Address `http://192.168.8.1`, connected to the Mac by Ethernet (`en8`).
- Credentials are in `glinet-client-go/.env` (ignored by git). Sessions must not print them.
- Tests on the real router run locally only, with `TF_ACC=1`. They must put the router back into its initial state.
- Resources that can cut access to the router (`lan_config`, `wifi_iface`) need a recovery procedure in the PR description before their real-router test runs.

## Phase 0: Cleanup

Status: done for the two repositories.

Client (done):
- Old generated code is on the local branch `wip-2025-07-generated` (not pushed). `main` is clean.
- `cmd/probe/` and `testdata/` are untracked. SSIDs, client hostnames, and WireGuard public keys in the fixtures must be replaced with fake values before they are committed (decision 4).

Provider:
1. Commit the uncommitted changes on `add-mocks` (`main.go` registry address, `test.yml`, `docs/index.md`, `.tool-versions`, `CLAUDE.md`).
2. Merge `add-mocks` into `main`.
3. Close the 7 open Dependabot PRs from 2024 (owner approval).
4. Delete merged and obsolete branches (owner approval).

## Phase 1: Router probe

Status: done. See `REPORT.md` in the fixtures directory.

## Phase 2: Client v0.1.0

Status: done. Released as `v0.1.0` (ryanrishi/glinet-client-go#20). A release workflow creates the tag and the GitHub release when the `Version` constant in `glinet.go` changes on `main` (ryanrishi/glinet-client-go#21).

One PR in `glinet-client-go`. Commit the probe and the fixtures in the same PR or in a PR before it.

1. `NewClient...` returns `(*Client, error)`. Remove `log.Fatal`.
2. One login sequence: one `challenge`, then `login` in the 1000 ms window. Remove the duplicate challenge call.
3. When a call returns `-32000`, log in again one time and repeat the call.
4. One generic method `Call(ctx, module, method, params, result)`. Remove the three `CallWith...` variants.
5. A typed `RPCError` with `Code` and `Message`. Callers can test for "method not found" and "access denied".
6. Fix `autotimezone_enabled`.
7. `adguardhome.set_config` sends `enabled` and `dns_enabled`.
8. New package `glinettest`: an `httptest` server that implements `challenge`, `login`, and `call`, with handlers for each module and state in memory. It gives the responses from the fixtures as initial state.
9. Client tests use `glinettest`.
10. Go 1.25 as the minimum version. Update `.tool-versions` and CI.

Deferred: `alg` 5 and 6 and `hash_method`. The owner's router does not use them. Add them when a router that needs them is available for a test.

Done: `go build`, `go vet`, `go test` pass; the login and one read call pass on the real router; the owner tags `v0.1.0`.

## Phase 3: Provider modernization

Status: done on the local branch `modernize` (not pushed). `make lint`, `make generate` (no diff) and `make test` pass locally on Terraform 1.16, 1.17 and OpenTofu 1.13. Open: `make testacc` on the real router (the router was not connected), CI run on GitHub, PR, merge.

One PR in this repository, after the `v0.1.0` tag.

1. Go 1.27. `terraform-plugin-framework` 1.19, `terraform-plugin-testing` 1.16, `terraform-plugin-docs` 0.25, `terraform-plugin-go` 0.31, `terraform-plugin-log` 0.11, client `v0.1.0`.
2. Align with the scaffold:
   - `tools/` as its own module, with `go generate` for `terraform fmt` and `tfplugindocs`.
   - `GNUmakefile` targets: `build`, `install`, `fmt`, `lint`, `generate`, `test`, `testacc`.
   - `.golangci.yml` in the v2 format.
   - `.goreleaser.yml` in the v2 format.
   - Current GitHub Action versions, pinned by commit SHA.
   - Dependabot groups for `terraform-plugin-*` and for GitHub Actions.
3. CI matrix: Terraform 1.16 and 1.17, and the latest OpenTofu.
4. Provider fixes:
   - `username` is optional, with the default `root`.
   - Login errors become diagnostics.
   - Remove scaffold remainders (wrong comments, the unused second `req.Config.Get`).
5. Tests:
   - Remove `interfaces.go`, `mocks.go`, and the testify tests.
   - Tests use `resource.UnitTest` with `ProtoV6ProviderFactories` and a `glinettest` server.
   - The same test steps run on the real router when `TF_ACC=1` and `GLINET_HOST` are set.
   - Use `statecheck` and `knownvalue` for assertions.
   - Cover provider configuration errors (no password, login failure) and an API error on a data source read, each through Terraform with `ExpectError`.
6. Update `README.md` and `CLAUDE.md` to the new commands and test method.

Not in scope: provider functions, ephemeral resources, actions, list resources.

Done: `make lint`, `make generate` (no diff), `make test` pass in CI; `make testacc` passes on the real router for the timezone data source.

## Phase 4: Resources and data sources

Status: resources 1 (`glinet_system_timezone`) and 2 (`glinet_lan_static_lease`) are done. They use client `v0.2.0`.

One item at a time. For each item:

| Step | Repository | Content |
|---|---|---|
| A | client | Typed methods, `glinettest` handler, tests. For write methods: verify create, update, delete on the real router and save the fixtures. PR. |
| B | owner | Merge and tag a client release. |
| C | provider | Resource or data source, import, example in `examples/`, generated documents, tests on the fake server, test on the real router. PR. |

The client session can be one or two items ahead of the provider session.

### Resources

| # | Name | API | Type | Notes |
|---|---|---|---|---|
| 1 | `glinet_system_timezone` | `system.get_timezone_config`, `set_timezone_config` | singleton | Proves the singleton pattern. |
| 2 | `glinet_lan_static_lease` | `lan.get_static_bind_list`, `add_static_bind`, `set_static_bind`, `remove_static_bind` | CRUD, ID is the MAC | Proves the CRUD pattern. |
| 3 | `glinet_firewall_port_forward` | `firewall.*_port_forward*` | CRUD, ID from the router | |
| 4 | `glinet_firewall_rule` | `firewall.*_rule*` | CRUD, ID from the router | |
| 5 | `glinet_firewall_dmz` | `firewall.get_dmz`, `set_dmz` | singleton | |
| 6 | `glinet_dns` | `dns.get_config`, `set_config` | singleton | Real fields differ from the 2023 reference. |
| 7 | `glinet_adguardhome` | `adguardhome.get_config`, `set_config` | singleton | |
| 8 | `glinet_wifi_iface` | `wifi.get_config`, `set_config` | update-only, import by `iface_name` | The key is sensitive. Can cut Wi-Fi access. |
| 9 | `glinet_lan_config` | `lan.get_config_list`, `set_config` | singleton for each interface (`lan`, `guest`) | Can cut router access. |
| 10 | `glinet_wireguard_server_peer` | `wg-server.get_peer_list`, `add_peer`, `set_peer`, `remove_peer` | CRUD | Needs the WireGuard server to be initialized. |
| 11 | `glinet_wireguard_client_config` | `wg-client.get_config_list`, `add_config`, `set_config`, `remove_config` | CRUD in a group | Works on 4.7.0. Not expected to work on 4.9.0 (`vpn-client`). |

### Data sources

| # | Name | API |
|---|---|---|
| 1 | `glinet_system_info` | `system.get_info` |
| 2 | `glinet_system_status` | `system.get_status` |
| 3 | `glinet_clients` | `clients.get_list` |
| 4 | `glinet_firewall_zones` | `firewall.get_zone_list` |

The existing `glinet_system_timezone_config` data source stays.

### Deferred

OpenVPN client and server, VPN policy, `vpn-client` (firmware 4.9.0), Tailscale, ZeroTier, DDNS, WAN (`cable`) configuration, LED, schedules.

## Conventions for resources

- Each resource has import.
- Secrets (Wi-Fi key, private key, preshared key) are `Sensitive`.
- API numbers that mean true or false (0 or 1) are `Bool` in the schema.
- A resource does not start, stop, or restart services unless the API does it as part of the write.
- Singleton destroy removes the resource from state and does not change the router.

## Decisions (owner, 2026-11-12)

1. CI matrix: the two latest Terraform minor versions (1.16, 1.17) and the latest OpenTofu.
2. Singleton destroy: remove from state and do not change the router.
3. Terraform Registry: the provider is published at https://registry.terraform.io/providers/ryanrishi/glinet/latest/docs and releases continue there.
4. Fixtures: no real SSIDs, client hostnames, or WireGuard public keys. The probe replaces them with stable fake values before a commit.
