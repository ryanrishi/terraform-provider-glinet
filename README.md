# terraform-provider-glinet
[![Tests](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml/badge.svg)](https://github.com/ryanrishi/terraform-provider-glinet/actions/workflows/test.yml)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ryanrishi/terraform-provider-glinet)](https://github.com/ryanrishi/terraform-provider-glinet/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/ryanrishi/terraform-provider-glinet)](https://goreportcard.com/report/github.com/ryanrishi/terraform-provider-glinet)

This provider allows Terraform to manage [GL.iNet](https://www.gl-inet.com/) routers running [v4 firmware](https://docs.gl-inet.com/router/en/4/).

## Building The Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

## Using the provider

You can find the latest release and its documentation in the [Terraform Registry](https://registry.terraform.io/providers/ryanrishi/glinet/latest/docs).

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `go generate`.

In order to run the full suite of Acceptance tests, run `make testacc`.

*Note:* Acceptance tests create real resources, and often cost money to run.

```shell
make testacc
```
