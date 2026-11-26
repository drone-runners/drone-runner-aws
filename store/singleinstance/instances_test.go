package singleinstance

import (
	"context"
	"testing"
)

// The single-instance store lists no instances, so counting them gives nothing, and must not panic.
func TestInstanceStore_CountPoolInstancesIsEmpty(t *testing.T) {
	s := NewSingleInstanceStore(nil)
	got, err := s.CountPoolInstances(context.Background(), "pool")
	if err != nil {
		t.Fatalf("CountPoolInstances: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no groups, got %+v", got)
	}
}
