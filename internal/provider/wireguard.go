package provider

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The helpers in this file are shared by the WireGuard resources.

const (
	wireGuardRouterValueNote = "When it is not set, the provider keeps the value of the router: an update sends back the value of the last read. " +
		"Thus, to remove a value from the router, replace the resource; removing the attribute from the configuration keeps the router value."
	wireGuardEmptyNumberNote = "When the router has no value, an update sends `\"\"`, as the web UI does for an empty input. What the router does with `\"\"` is not known."
	wireGuardEmptyListNote   = "When the router has no value, the provider does not send it, as the client does not send an empty value."
)

var (
	// The key rule of the web UI: 44 characters of base64 that end in "=", the encoding of 32 bytes.
	wireGuardKey = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)
	hostName     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)
)

var (
	wireGuardNameValidator = stringCheck{
		description: "must have 1 to 64 characters, not only spaces, must not start with . and must not contain ~",
		valid:       validWireGuardName,
	}
	wireGuardKeyValidator = stringCheck{
		description: "must be a WireGuard key: 44 characters of base64 that end in =",
		valid:       wireGuardKey.MatchString,
	}
	wireGuardSecretKeyValidator = stringCheck{
		description: wireGuardKeyValidator.description,
		valid:       wireGuardKey.MatchString,
		sensitive:   true,
	}
)

// validWireGuardName is the name rule of the web UI for WireGuard groups and peers.
func validWireGuardName(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= 64 && strings.TrimSpace(s) != "" && !strings.HasPrefix(s, ".") && !strings.Contains(s, "~")
}

func validIPOrPrefix(s string, is4 bool) bool {
	if strings.Contains(s, "/") {
		prefix, err := netip.ParsePrefix(s)
		return err == nil && prefix.Addr().Is4() == is4
	}
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4() == is4 && addr.Zone() == ""
}

func validPrefix(s string) bool {
	_, err := netip.ParsePrefix(s)
	return err == nil
}

func validIP(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Zone() == ""
}

// validEndpoint accepts host:port with a host name, an IPv4 address or an IPv6 address in brackets, and a port from 1 to 65535.
func validEndpoint(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || port != strconv.Itoa(n) {
		return false
	}
	if strings.Contains(s, "[") {
		return validIPv6(host)
	}
	return validIPv4(host) || hostName.MatchString(host)
}

// splitList returns the items of a router list in one string, such as "10.0.0.0/24, fd00::/64".
func splitList(s string) []string {
	var items []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// joinList returns the router string of a list, with ", " between the items as the web UI sends it.
// An unknown list, which is not set in the configuration and has no value in the state, gives "", which the client does not send.
func joinList(ctx context.Context, list types.List, diags *diag.Diagnostics) string {
	if list.IsUnknown() {
		return ""
	}
	return strings.Join(listStrings(ctx, list, diags), ", ")
}

// listOrNull returns null for an empty router list.
func listOrNull(ctx context.Context, s string, diags *diag.Diagnostics) types.List {
	items := splitList(s)
	if len(items) == 0 {
		return types.ListNull(types.StringType)
	}
	return stringList(ctx, items, diags)
}

// parseRouterID returns the positive number of an import ID part.
func parseRouterID(s string) (int, bool) {
	id, err := strconv.Atoi(s)
	return id, err == nil && id > 0 && s == strconv.Itoa(id)
}

func invalidImportID(resp *resource.ImportStateResponse, typeName, format, example, id string) {
	resp.Diagnostics.AddError("Invalid Import ID",
		fmt.Sprintf("The import ID of %s must be %s, for example %s. Got %q.", typeName, format, example, id))
}

// addCreateError adds the error of AddGroup or AddPeer. The client reads the list after the add call to get the new ID,
// and its error does not tell whether the add call or that read failed, so the router may have created the object.
func addCreateError(diags *diag.Diagnostics, summary, object string, err error) {
	diags.AddError(summary, fmt.Sprintf("%s\n\nThe router may have created the %s before the error. "+
		"Check the router in the web UI, and remove or import the %s before the next apply.", err, object, object))
}
