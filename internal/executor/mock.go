package executor

import (
	"context"
	"sync"
)

type MockCall struct {
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

// MockExecutor — Pre-configure Responses in order; each Run consumes the next response.
type MockExecutor struct {
	mu        sync.Mutex
	Calls     []MockCall
	Responses []MockCall
	index     int
}

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

func (m *MockExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
	m.index = 0
}
