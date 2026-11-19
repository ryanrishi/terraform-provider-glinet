# Contributing

Thank you for your help. This page gives the rules that are not obvious from the code.

## Before you start

- For a bug, open an issue with the bug report form.
- For a new resource or attribute, open an issue first. A new router method needs work in the client [glinet-client-go](https://github.com/ryanrishi/glinet-client-go) before the provider can use it.

## Use only made-up data

Tests, examples, documentation, commit messages and pull requests must contain only made-up values. Do not use values from your own router or network, also when they are not secret.

- IP addresses: use `192.168.8.x` (the router default) or the documentation ranges `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24` and `2001:db8::/32`.
- MAC addresses: use made-up addresses such as `02:00:00:00:00:01`.
- Names: use names such as `example`. Do not use a real SSID, hostname, VPN peer name or DDNS name.
- Never include a password, a Wi-Fi key, a WireGuard key, a serial number or a session ID.

When you post a router result in an issue, replace these values first. The structure, the key names and the value types are sufficient.

## Run the checks

You need Go (version in `.tool-versions`), [golangci-lint](https://golangci-lint.run/) v2 for `make lint`, and [Terraform](https://developer.hashicorp.com/terraform/install) on `PATH` for `make generate` and `make test`. To run the tests with OpenTofu, see "Tests" in `README.md`.

```bash
make fmt
make lint
make generate
make test
make vet32
```

- `make test` runs all tests on a fake router and does not need a router. It stops after 20 minutes. Make sure that `TF_ACC` is not set: with `TF_ACC` and `GLINET_HOST` set, some tests use the real router and change its configuration.
- `make generate` updates `docs/` from the schemas and the examples. Commit the result.
- `make vet32` runs `go vet` for the 32-bit platforms of the release builds (`linux/386` and `linux/arm`).
- Do not run `make testacc` with `GLINET_HOST` set unless you know what the tests write to your router. `README.md` has the details.

## Rules for resources

- Each resource has import, an example in `examples/`, and tests on the fake router.
- A secret is `Sensitive`. A router number that means true or false is a `Bool`.
- A resource supports each option that the router web UI can set, not only the options of one router model.
- When an attribute is not set, a write sends back the value of the router. A write does not change a router value that the user did not configure.
- The description of a resource states what destroy does, and states it when a write was not tested on a real router.

`CLAUDE.md` has the full list of conventions and the test architecture.
