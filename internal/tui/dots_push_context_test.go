package tui

import (
	"context"
	"testing"
)

type dotsPushContextTestKey struct{}

func TestDotsPushContextSurvivesSupersedingTUIOperation(t *testing.T) {
	key := dotsPushContextTestKey{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "value"))
	durable := dotsPushContext(parent)
	cancel()
	if err := durable.Err(); err != nil {
		t.Fatalf("push context inherited cancellation: %v", err)
	}
	if got := durable.Value(key); got != "value" {
		t.Fatalf("push context value = %v", got)
	}
}
