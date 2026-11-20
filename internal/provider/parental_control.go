package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
)

// The helpers in this file are shared by the parental control resources.

const (
	parentalControlWarning = "~> **Warning:** parental control can block clients, also the computer that runs Terraform: " +
		"`drop_anonymous_enabled` of `glinet_parental_control` blocks each client that is in no group, " +
		"and a group with the default rule set `drop` blocks its clients when no schedule applies. " +
		"Then Terraform and the web UI can lose the access to the router."
	parentalControlAccelerationNote = "The web UI says that parental control does not work while network acceleration is on."
	parentalControlIDNote           = "The router gives the ID. It was not verified that the ID stays the same when other items are added or removed, " +
		"so the provider keeps the ID in the state and, when the router has no item with that ID and the name of the state, " +
		"finds the item by its name. When no item has the name, the resource is gone from the router and Terraform plans to create it again. " +
		"When the item with the ID of the state has another name and a different item has the name of the state, " +
		"read, update and destroy fail, because either item can be the item of this resource: " +
		"remove the resource from the state with `terraform state rm`, and import the right item by its name."
)

// The preset rule sets cannot be changed or removed. Their IDs are "drop" and "accept", and their names
// parentalControlPresetNames.
var (
	parentalControlPresetIDs   = []string{glinet.ParentalControlRuleDrop, glinet.ParentalControlRuleAccept}
	parentalControlPresetNames = []string{"drop_all", "accept_all"}
)

// findParentalControlItem returns the index of the item with the ID id and the name name.
// When no item has both, it returns the index of the only item with the name name.
// It returns -1 when no item has the name. It returns an error when more than one item has the name, and when
// an item has the ID with another name while a different item has the name, because then either item can be
// the item of the state.
func findParentalControlItem[T any](items []T, key func(T) (id, name string), id, name, object string) (int, error) {
	byID, byName := -1, -1
	count := 0
	for i, item := range items {
		itemID, itemName := key(item)
		if itemID == id {
			byID = i
		}
		if itemName != name {
			continue
		}
		if itemID == id {
			return i, nil
		}
		byName = i
		count++
	}
	if count > 1 {
		return -1, fmt.Errorf("the router has %d %ss with the name %q, and none of them has the ID %q of the state. "+
			"Give each %s a unique name in the web UI", count, object, name, id, object)
	}
	if byID >= 0 && byName >= 0 {
		_, renamed := key(items[byID])
		otherID, _ := key(items[byName])
		return -1, fmt.Errorf("the %s of the state (ID %q, name %q) is not clear: the router has the %s %q with the ID %q of the state, "+
			"and the %s %q with the ID %q has the name of the state. "+
			"Remove the resource from the state with terraform state rm, then import the %s that this resource manages by its name if you want to keep managing it",
			object, id, name, object, renamed, id, object, name, otherID, object)
	}
	return byName, nil
}

func ruleKey(r glinet.ParentalControlRule) (string, string)   { return r.ID, r.Name }
func groupKey(g glinet.ParentalControlGroup) (string, string) { return g.ID, g.Name }

// customRules returns the rule sets that are not presets.
func customRules(config *glinet.ParentalControlConfig) []glinet.ParentalControlRule {
	var rules []glinet.ParentalControlRule
	for _, r := range config.Rules {
		if !r.Preset && !slices.Contains(parentalControlPresetIDs, r.ID) {
			rules = append(rules, r)
		}
	}
	return rules
}

// checkRulesets adds an error for each rule set ID in ids that the router does not have.
func checkRulesets(config *glinet.ParentalControlConfig, ids []string, diags *diag.Diagnostics) {
	var known []string
	for _, r := range config.Rules {
		known = append(known, r.ID)
	}
	for _, id := range ids {
		if !slices.Contains(known, id) {
			diags.AddError("Unknown Parental Control Rule Set",
				fmt.Sprintf("The router has no parental control rule set with the ID %q. The router has the rule sets %s.", id, quotedList(known)))
		}
	}
}

func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// emptyOr returns the empty collection prior when the router has no items and prior is empty, so that a configured
// empty collection does not become null. Else it returns value.
func emptyOr[T interface {
	attr.Value
	Elements() []attr.Value
}](prior T, value T, routerEmpty bool) T {
	if routerEmpty && !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
		return prior
	}
	return value
}

func int64Set(ctx context.Context, values []int64, diags *diag.Diagnostics) types.Set {
	if len(values) == 0 {
		return types.SetNull(types.Int64Type)
	}
	set, d := types.SetValueFrom(ctx, types.Int64Type, values)
	diags.Append(d...)
	return set
}
