package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const clockFormat = "15:04"

var clockValidator = stringCheck{description: "must be a time from 00:00 to 23:59 in the format HH:MM", valid: validClock}

func validClock(s string) bool {
	t, err := time.Parse(clockFormat, s)
	return err == nil && t.Format(clockFormat) == s
}

func clockAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Description: description + " Format `HH:MM`, 24 hours, in the time zone of the router.",
		Required:    true,
		Validators:  []validator.String{clockValidator},
	}
}

// splitClock returns the hour and the minute of a valid "HH:MM" time.
func splitClock(clock string) (hour, minute string) {
	return clock[:2], clock[3:]
}

// joinClock returns "HH:MM" for the hour and minute strings of the router, which can have one digit.
func joinClock(hour, minute string) string {
	return twoDigits(hour) + ":" + twoDigits(minute)
}

func twoDigits(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return s
	}
	return fmt.Sprintf("%02d", n)
}

func daysAttribute(description string) schema.SetAttribute {
	return schema.SetAttribute{
		Description: description + " Each day is a number from 0 (Sunday) to 6 (Saturday) (`week` in the router API).",
		ElementType: types.Int64Type,
		Required:    true,
		Validators: []validator.Set{
			setvalidator.SizeAtLeast(1),
			setvalidator.ValueInt64sAre(int64validator.Between(0, 6)),
		},
	}
}

// routerDays returns the days of the set in ascending order.
func routerDays(ctx context.Context, days types.Set, diags *diag.Diagnostics) []int {
	var values []int64
	diags.Append(days.ElementsAs(ctx, &values, false)...)

	week := make([]int, len(values))
	for i, v := range values {
		week[i] = int(v)
	}
	slices.Sort(week)
	return week
}

// daysSet returns the days of the router as a set. The router keeps the order and the duplicates of a write.
func daysSet(week []int) types.Set {
	values := make([]int64, len(week))
	for i, d := range week {
		values[i] = int64(d)
	}
	slices.Sort(values)
	values = slices.Compact(values)

	elements := make([]attr.Value, len(values))
	for i, v := range values {
		elements[i] = types.Int64Value(v)
	}
	return types.SetValueMust(types.Int64Type, elements)
}
