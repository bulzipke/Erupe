package securityaudit

import (
	"context"
	"testing"
)

func TestNilRepositoryDoesNotBreakCallers(t *testing.T) {
	repo := NewRepository(nil)
	if err := repo.Record(context.Background(), Event{
		Source: "test", Type: "observation", Severity: "info", Decision: "observed",
	}); err != nil {
		t.Fatalf("Record() with nil DB returned error: %v", err)
	}
}
