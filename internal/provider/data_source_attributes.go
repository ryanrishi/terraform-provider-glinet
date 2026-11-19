package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{Description: description, Computed: true}
}

func computedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Description: description, Computed: true}
}

func computedInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{Description: description, Computed: true}
}

func computedFloat64(description string) schema.Float64Attribute {
	return schema.Float64Attribute{Description: description, Computed: true}
}

func computedObject(description string, attributes map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Description: description, Computed: true, Attributes: attributes}
}

func computedList(description string, attributes map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Description:  description,
		Computed:     true,
		NestedObject: schema.NestedAttributeObject{Attributes: attributes},
	}
}

// channelOrNull returns null for channel 0, which the client gives when the router has no channel.
// 0 is not a Wi-Fi channel.
func channelOrNull(channel int) types.Int64 {
	if channel == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(channel))
}

// stringOrNull returns null for the empty string, which the client gives for a field that the router leaves out.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
