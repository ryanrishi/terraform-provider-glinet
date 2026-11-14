package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStringCheckSensitive(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		check := stringCheck{description: "must be valid", valid: func(string) bool { return false }, sensitive: sensitive}
		resp := &validator.StringResponse{}
		check.ValidateString(context.Background(), validator.StringRequest{
			Path:        path.Root("key"),
			ConfigValue: types.StringValue("the-secret"),
		}, resp)

		if !resp.Diagnostics.HasError() {
			t.Fatalf("sensitive %t: no error", sensitive)
		}
		detail := resp.Diagnostics[0].Detail()
		if got := strings.Contains(detail, "the-secret"); got == sensitive {
			t.Errorf("sensitive %t: error shows the value: %t (%s)", sensitive, got, detail)
		}
	}
}

func TestValidWifiKeyRejectsDEL(t *testing.T) {
	if validWifiKey("passwor\x7f") {
		t.Error("a key with the DEL character is accepted")
	}
	if !validWifiKey("passwor~") {
		t.Error("a key with ~ is rejected")
	}
}

func TestLeaseSecondsOverflow(t *testing.T) {
	for _, s := range []string{"307445734561825863m", "9223372036854775807h"} {
		if _, ok := leaseSeconds(s); ok {
			t.Errorf("leaseSeconds(%q) is accepted", s)
		}
		if validLeaseTime(s) {
			t.Errorf("validLeaseTime(%q) is true", s)
		}
	}
}
