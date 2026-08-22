package executor

import (
	"context"
	"sync"
)

type MockCall struct {
	Name   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  []byte
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
	return m.run(name, args, nil, "", nil)
}

func (m *MockExecutor) RunEnv(_ context.Context, env []string, name string, args ...string) (string, string, error) {
	return m.run(name, args, env, "", nil)
}

func (m *MockExecutor) RunDir(_ context.Context, dir, name string, args ...string) (string, string, error) {
	return m.run(name, args, nil, dir, nil)
}

func (m *MockExecutor) RunDirEnv(_ context.Context, dir string, env []string, name string, args ...string) (string, string, error) {
	return m.run(name, args, env, dir, nil)
}

func (m *MockExecutor) RunDirEnvStdin(_ context.Context, dir string, env []string, stdin []byte, name string, args ...string) (string, string, error) {
	return m.run(name, args, env, dir, stdin)
}

func (m *MockExecutor) run(name string, args, env []string, dir string, stdin []byte) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args, Env: env, Dir: dir, Stdin: append([]byte(nil), stdin...)})
	if m.index < len(m.Responses) {
		r := m.Responses[m.index]
		m.index++
		return r.Stdout, r.Stderr, r.Err
	}
	return "", "", nil
}

// CommandAvailable — mocked commands are assumed to exist; availability-gated tests use their own stub.
func (m *MockExecutor) CommandAvailable(string) bool {
	return true
}

func (m *MockExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
	m.index = 0
}
