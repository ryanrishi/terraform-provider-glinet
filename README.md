# terraform-provider-glinet
[![Tests](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml/badge.svg)](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ryanrishi/terraform-provider-glinet)](https://github.com/ryanrishi/terraform-provider-glinet/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/ryanrishi/terraform-provider-glinet)](https://goreportcard.com/report/github.com/ryanrishi/terraform-provider-glinet)

This provider lets Terraform manage [GL.iNet](https://www.gl-inet.com/) routers that run [v4 firmware](https://docs.gl-inet.com/router/en/4/).
It uses the Go client [glinet-client-go](https://github.com/ryanrishi/glinet-client-go).

## Using the provider

The latest release and its documentation are in the [Terraform Registry](https://registry.terraform.io/providers/ryanrishi/glinet/latest/docs).

```terraform
provider "glinet" {
  host     = "http://192.168.8.1" # default
  username = "root"               # default
  password = "password"
}
```

The provider also reads `GLINET_HOST`, `GLINET_USERNAME` and `GLINET_PASSWORD`.

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

When `TF_ACC=1` and `GLINET_HOST` are set, the data source tests read the router at `GLINET_HOST` instead of the fake router.
The provider gets the credentials from `GLINET_USERNAME` and `GLINET_PASSWORD`.
Because the values on a router are not fixed, these tests check only the types and that the values are not empty.
The tests for errors always use the fake router.

```shell
GLINET_HOST=http://192.168.8.1 GLINET_USERNAME=root GLINET_PASSWORD=... make testacc
```

Tests on a real router run locally only. A test that changes the router must put the router back into its initial state.
