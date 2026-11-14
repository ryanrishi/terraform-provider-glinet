package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/ryanrishi/glinet-client-go/glinettest"
)

// canonicalJSON returns s with sorted object keys and no spaces, so that two JSON values can be compared as strings.
func canonicalJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", s, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// callRecorder records the params of the calls of a router method.
type callRecorder struct {
	t      *testing.T
	mu     sync.Mutex
	params []string
}

// recordCalls wraps module.method of srv so that each call is recorded and then served as before.
func recordCalls(t *testing.T, srv *glinettest.Server, module, method string) *callRecorder {
	c := &callRecorder{t: t}
	next := srv.Handler(module, method)
	srv.Handle(module, method, func(params json.RawMessage) (any, error) {
		c.mu.Lock()
		c.params = append(c.params, canonicalJSON(t, string(params)))
		c.mu.Unlock()
		return next(params)
	})
	return c
}

// take returns the recorded params and clears them.
func (c *callRecorder) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	params := c.params
	c.params = nil
	return params
}

// expect returns a check that the calls since the last check had exactly the params want, in this order.
func (c *callRecorder) expect(want ...string) resource.TestCheckFunc {
	for i := range want {
		want[i] = canonicalJSON(c.t, want[i])
	}
	return func(*terraform.State) error {
		got := c.take()
		if !slices.Equal(got, want) {
			return fmt.Errorf("calls have params %q, want %q", got, want)
		}
		return nil
	}
}

// expectUnordered is expect for calls that can come in any order.
func (c *callRecorder) expectUnordered(want ...string) resource.TestCheckFunc {
	for i := range want {
		want[i] = canonicalJSON(c.t, want[i])
	}
	slices.Sort(want)
	return func(*terraform.State) error {
		got := c.take()
		slices.Sort(got)
		if !slices.Equal(got, want) {
			return fmt.Errorf("calls have params %q, want %q", got, want)
		}
		return nil
	}
}

// expectRouterJSON checks that the fake router result that get returns is the JSON value want.
func expectRouterJSON(t *testing.T, get func() json.RawMessage, want string) resource.TestCheckFunc {
	want = canonicalJSON(t, want)
	return func(*terraform.State) error {
		if got := canonicalJSON(t, string(get())); got != want {
			return fmt.Errorf("router result is %s, want %s", got, want)
		}
		return nil
	}
}

// savedRouterJSON saves a fake router result after one test step, so that a later step can check that the
// router has it again.
type savedRouterJSON struct {
	get   func() json.RawMessage
	saved json.RawMessage
}

func saveRouterJSON(get func() json.RawMessage) *savedRouterJSON {
	return &savedRouterJSON{get: get}
}

func (s *savedRouterJSON) save(*terraform.State) error {
	s.saved = s.get()
	return nil
}

func (s *savedRouterJSON) expect(*terraform.State) error {
	var got, want any
	if err := json.Unmarshal(s.get(), &got); err != nil {
		return err
	}
	if err := json.Unmarshal(s.saved, &want); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("router result is %s, want %s", s.get(), s.saved)
	}
	return nil
}

// expectJSONEqual reports an error when the JSON values got and want are not equal.
func expectJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if g, w := canonicalJSON(t, string(got)), canonicalJSON(t, want); g != w {
		t.Errorf("router result is %s, want %s", g, w)
	}
}

// wrapped returns a pattern for the text s in an error message, which Terraform can wrap at each space.
func wrapped(s string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(s), " ", `\s+`)
}
