package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var (
	_ basetypes.StringTypable                    = caseInsensitiveStringType{}
	_ basetypes.StringValuableWithSemanticEquals = caseInsensitiveString{}
)

// caseInsensitiveStringType is the type of caseInsensitiveString.
type caseInsensitiveStringType struct {
	basetypes.StringType
}

func (t caseInsensitiveStringType) Equal(o attr.Type) bool {
	other, ok := o.(caseInsensitiveStringType)
	return ok && t.StringType.Equal(other.StringType)
}

func (t caseInsensitiveStringType) String() string {
	return "caseInsensitiveStringType"
}

func (t caseInsensitiveStringType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return caseInsensitiveString{StringValue: in}, nil
}

func (t caseInsensitiveStringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	value, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}

	stringValue, ok := value.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", value)
	}
	return caseInsensitiveString{StringValue: stringValue}, nil
}

func (t caseInsensitiveStringType) ValueType(context.Context) attr.Value {
	return caseInsensitiveString{}
}

// caseInsensitiveString is a string that is semantically equal to the same string in another letter case.
// When the router returns a value in another letter case, the state keeps the configured value.
type caseInsensitiveString struct {
	basetypes.StringValue
}

func caseInsensitiveStringValue(value string) caseInsensitiveString {
	return caseInsensitiveString{StringValue: basetypes.NewStringValue(value)}
}

func (v caseInsensitiveString) Type(context.Context) attr.Type {
	return caseInsensitiveStringType{}
}

func (v caseInsensitiveString) Equal(o attr.Value) bool {
	other, ok := o.(caseInsensitiveString)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v caseInsensitiveString) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	newValue, diags := newValuable.ToStringValue(ctx)
	return strings.EqualFold(v.ValueString(), newValue.ValueString()), diags
}

// requiresReplaceIfNotLetterCase replaces the resource when the value changes in more than its letter case.
func requiresReplaceIfNotLetterCase() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = req.PlanValue.IsUnknown() || !strings.EqualFold(req.PlanValue.ValueString(), req.StateValue.ValueString())
		},
		"A change of the value in more than its letter case replaces the resource.",
		"A change of the value in more than its letter case replaces the resource.",
	)
}
