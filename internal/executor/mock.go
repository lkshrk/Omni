package executor

import (
	"context"
	"sync"
)

// MockCall records one invocation or configures one response.
type MockCall struct {
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

// MockExecutor is a test-double for Executor.
// Pre-configure Responses in order; each Run consumes the next response.
// All calls are recorded in Calls for assertion. Thread-safe.
type MockExecutor struct {
	mu        sync.Mutex
	Calls     []MockCall
	Responses []MockCall
	index     int
}

// Run records the call and returns the next pre-configured response.
// If no response is left it returns empty strings and nil error.
func (m *MockExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	if m.index < len(m.Responses) {
		r := m.Responses[m.index]
		m.index++
		return r.Stdout, r.Stderr, r.Err
	}
	return "", "", nil
}

// Reset clears recorded calls and resets the response pointer.
func (m *MockExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
	m.index = 0
}
