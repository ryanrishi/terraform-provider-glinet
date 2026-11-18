package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

const (
	dnsSecureServersAddress          = "data.glinet_dns_secure_servers.test"
	dnsSecureServersDataSourceConfig = `
data "glinet_dns_secure_servers" "test" {}
`
)

func TestDNSSecureServersDataSource(t *testing.T) {
	parallel(t)
	if realRouter() {
		readDataSource(t, `provider "glinet" {}`+dnsSecureServersDataSourceConfig, expectAttributes(dnsSecureServersAddress, map[string]knownvalue.Check{
			"protocols": knownvalue.ListPartial(map[int]knownvalue.Check{0: nonEmptyString}),
			"servers": knownvalue.ListPartial(map[int]knownvalue.Check{
				0: knownvalue.ObjectExact(map[string]knownvalue.Check{
					"name":      nonEmptyString,
					"protocol":  nonEmptyString,
					"dnssec":    anyBool,
					"ipv6":      anyBool,
					"no_filter": anyBool,
					"no_log":    anyBool,
				}),
			}),
		}))
		return
	}

	srv := glinettest.NewServer(t)
	server := func(name, protocol string, dnssec, ipv6, noFilter, noLog bool) knownvalue.Check {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{
			"name":      knownvalue.StringExact(name),
			"protocol":  knownvalue.StringExact(protocol),
			"dnssec":    knownvalue.Bool(dnssec),
			"ipv6":      knownvalue.Bool(ipv6),
			"no_filter": knownvalue.Bool(noFilter),
			"no_log":    knownvalue.Bool(noLog),
		})
	}

	readDataSource(t, fakeRouterConfig(srv)+dnsSecureServersDataSourceConfig, expectAttributes(dnsSecureServersAddress, map[string]knownvalue.Check{
		"protocols": stringsExact([]string{"DoT", "DNSCrypt", "DoH", "oDoH"}),
		"servers": knownvalue.ListExact([]knownvalue.Check{
			server("example-doh", "DoH", true, false, true, true),
			server("example-doh-ipv6", "DoH", true, true, false, true),
			server("example-dnscrypt", "DNSCrypt", false, false, true, false),
			server("example-odoh", "oDoH", true, false, true, true),
		}),
	}))
}

func TestDNSSecureServersDataSourceEmpty(t *testing.T) {
	parallel(t)
	srv := glinettest.NewServer(t)
	srv.Handle("dns", "get_info", func(json.RawMessage) (any, error) {
		return json.RawMessage(`{"dnscrypt_version": 2}`), nil
	})

	readDataSource(t, fakeRouterConfig(srv)+dnsSecureServersDataSourceConfig, expectAttributes(dnsSecureServersAddress, map[string]knownvalue.Check{
		"protocols": knownvalue.ListExact([]knownvalue.Check{}),
		"servers":   knownvalue.ListExact([]knownvalue.Check{}),
	}))
}

func TestDNSSecureServersDataSourceAPIError(t *testing.T) {
	parallel(t)
	expectReadError(t, glinettest.NewServer(t), dnsSecureServersDataSourceConfig, "dns", "get_info", "Unable to Read DNS Secure Servers")
}
