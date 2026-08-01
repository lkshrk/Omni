package executor

import (
	"context"
	"strings"
	"sync"
)

// MatchRule — Pattern is matched against "name arg1 arg2 ..." (space-joined) using HasPrefix.
type MatchRule struct {
	Pattern  string
	Response MockCall
}

// MatchMockExecutor — Matches on the command instead of consuming responses in order, so call order may vary.
type MatchMockExecutor struct {
	mu       sync.Mutex
	rules    []MatchRule
	fallback MockCall
	Calls    []MockCall
}

func NewMatchMock(rules ...MatchRule) *MatchMockExecutor {
	return &MatchMockExecutor{rules: rules}
}

func (m *MatchMockExecutor) WithFallback(resp MockCall) *MatchMockExecutor {
	m.fallback = resp
	return m
}

func (m *MatchMockExecutor) AddRule(rule MatchRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append(m.rules, rule)
}

func (m *MatchMockExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	return m.run(name, args, nil)
}

func (m *MatchMockExecutor) RunEnv(_ context.Context, env []string, name string, args ...string) (string, string, error) {
	return m.run(name, args, env)
}

func (m *MatchMockExecutor) run(name string, args, env []string) (string, string, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args, Env: env})

	for _, rule := range m.rules {
		if strings.HasPrefix(key, rule.Pattern) {
			return rule.Response.Stdout, rule.Response.Stderr, rule.Response.Err
		}
	}
	return m.fallback.Stdout, m.fallback.Stderr, m.fallback.Err
}

func (m *MatchMockExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

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

func (m *MatchMockExecutor) AssertCalled(t interface{ Errorf(string, ...any) }, prefix string) {
	if len(m.CallsMatching(prefix)) == 0 {
		t.Errorf("expected call matching %q, but none was recorded", prefix)
	}
}

func (m *MatchMockExecutor) MustHaveCalledN(t interface{ Errorf(string, ...any) }, prefix string, n int) {
	if got := len(m.CallsMatching(prefix)); got != n {
		t.Errorf("expected %d calls matching %q, got %d", n, prefix, got)
	}
}

var _ Executor = (*MatchMockExecutor)(nil)
