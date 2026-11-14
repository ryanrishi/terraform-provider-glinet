package provider

import "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{Description: description, Computed: true}
}

func computedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Description: description, Computed: true}
}

func computedInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{Description: description, Computed: true}
}

func computedObject(description string, attributes map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Description: description, Computed: true, Attributes: attributes}
}
