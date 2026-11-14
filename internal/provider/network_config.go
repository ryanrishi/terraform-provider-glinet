package provider

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The helpers in this file are shared by the network configuration resources (WAN, IPv6, LAN, Wi-Fi).

const untestedWriteNote = "The write of this resource was not tested on a router; the parameters come from the router's web UI. "

const destroyNote = "Destroy removes the resource from the Terraform state and does not change the router."

func validIPv6(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6() && !addr.Is4In6() && addr.Zone() == ""
}

// netmaskBits returns the prefix length of a dotted IPv4 netmask such as 255.255.255.0.
func netmaskBits(s string) (int, bool) {
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is4() {
		return 0, false
	}

	b := addr.As4()
	mask := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	bits := 0
	for mask&(1<<31) != 0 {
		mask <<= 1
		bits++
	}
	return bits, mask == 0
}

func validNetmask(s string) bool {
	_, ok := netmaskBits(s)
	return ok
}

func stringList(ctx context.Context, values []string, diags *diag.Diagnostics) types.List {
	list, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return list
}

func listStrings(ctx context.Context, list types.List, diags *diag.Diagnostics) []string {
	var values []string
	if !list.IsNull() {
		diags.Append(list.ElementsAs(ctx, &values, false)...)
	}
	return values
}

// int64OrNull returns null for 0, which the client gives for a number that the router leaves out.
func int64OrNull(v int) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(v))
}

// configRules is a ConfigValidator for the rules between attributes.
//
// Terraform validates a configuration also before it knows the values of variables and of other resources,
// so the rules skip unknown values: forbid rejects only a known value that is not null, require accepts an
// unknown value, and a rule whose deciding attribute is unknown is not checked.
type configRules struct {
	description string
	validate    func(ctx context.Context, req resource.ValidateConfigRequest, diags *diag.Diagnostics)
}

func (v configRules) Description(context.Context) string {
	return v.description
}

func (v configRules) MarkdownDescription(ctx context.Context) string {
	return v.description
}

func (v configRules) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	v.validate(ctx, req, &resp.Diagnostics)
}

const invalidCombination = "Invalid Attribute Combination"

// forbid adds an error when the known value of the attribute is not null.
func forbid(diags *diag.Diagnostics, attribute string, value attr.Value, reason string) {
	if !value.IsNull() && !value.IsUnknown() {
		diags.AddAttributeError(path.Root(attribute), invalidCombination, fmt.Sprintf("%s cannot be set %s.", attribute, reason))
	}
}

// require adds an error when the attribute is null.
func require(diags *diag.Diagnostics, attribute string, value attr.Value, reason string) {
	if value.IsNull() {
		diags.AddAttributeError(path.Root(attribute), invalidCombination, fmt.Sprintf("%s is required %s.", attribute, reason))
	}
}
