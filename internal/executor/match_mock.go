package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MatchRule maps a command pattern to a fixed response.
// Pattern is matched against "name arg1 arg2 ..." (space-joined) using HasPrefix.
type MatchRule struct {
	Pattern  string
	Response MockCall
}

// MatchMockExecutor returns pre-configured responses by pattern-matching the
// command and its arguments, instead of consuming responses in order.
// This makes it safe for concurrent calls where ordering is non-deterministic.
// Thread-safe.
type MatchMockExecutor struct {
	mu       sync.Mutex
	rules    []MatchRule
	fallback MockCall // returned when no rule matches
	Calls    []MockCall
}

// NewMatchMock creates a MatchMockExecutor with the given rules.
func NewMatchMock(rules ...MatchRule) *MatchMockExecutor {
	return &MatchMockExecutor{rules: rules}
}

// WithFallback sets the response returned when no rule matches.
func (m *MatchMockExecutor) WithFallback(resp MockCall) *MatchMockExecutor {
	m.fallback = resp
	return m
}

// AddRule appends a matching rule at runtime (safe while not running).
func (m *MatchMockExecutor) AddRule(rule MatchRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append(m.rules, rule)
}

// Run records the call and returns the first matching response.
func (m *MatchMockExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})

	for _, rule := range m.rules {
		if strings.HasPrefix(key, rule.Pattern) {
			return rule.Response.Stdout, rule.Response.Stderr, rule.Response.Err
		}
	}
	return m.fallback.Stdout, m.fallback.Stderr, m.fallback.Err
}

// CallCount returns how many times the executor was called.
func (m *MatchMockExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// CallsMatching returns all calls whose key has the given prefix.
func (m *MatchMockExecutor) CallsMatching(prefix string) []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MockCall
	for _, c := range m.Calls {
		key := c.Name
		if len(c.Args) > 0 {
			key = c.Name + " " + strings.Join(c.Args, " ")
		}
		if strings.HasPrefix(key, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// AssertCalled panics if no call matching prefix was recorded.
func (m *MatchMockExecutor) AssertCalled(t interface{ Errorf(string, ...any) }, prefix string) {
	if len(m.CallsMatching(prefix)) == 0 {
		t.Errorf("expected call matching %q, but none was recorded", prefix)
	}
}

// MustHaveCalledN panics if prefix was not called exactly n times.
func (m *MatchMockExecutor) MustHaveCalledN(t interface{ Errorf(string, ...any) }, prefix string, n int) {
	if got := len(m.CallsMatching(prefix)); got != n {
		t.Errorf("expected %d calls matching %q, got %d", n, prefix, got)
	}
}

// Verify that MatchMockExecutor satisfies the Executor interface.
var _ Executor = (*MatchMockExecutor)(nil)

// BrewVersionOutput formats a `brew list --versions <pkg>` output line.
func BrewVersionOutput(pkg, version string) string {
	return fmt.Sprintf("%s %s\n", pkg, version)
}
