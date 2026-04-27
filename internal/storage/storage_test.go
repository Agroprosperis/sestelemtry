package storage

import (
	"context"
	"testing"
)

func TestInsertSamplesEmptyNoPoolNeeded(t *testing.T) {
	if err := InsertSamples(context.Background(), nil, nil); err != nil {
		t.Fatalf("expected nil for empty samples, got %v", err)
	}
}
