package database_test

import (
	"errors"
	"testing"

	"github.com/ryan053857/go-kit/database"
)

func TestLeader(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	a := database.NewLeader(db.DB, "jobs")
	b := database.NewLeader(db.DB, "jobs")
	other := database.NewLeader(db.DB, "other")

	if err := a.IsLeader(ctx); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := a.IsLeader(ctx); err != nil {
		t.Fatalf("a again: %v", err)
	}
	if err := b.IsLeader(ctx); !errors.Is(err, database.ErrNotLeader) {
		t.Fatalf("b: %v", err)
	}
	if err := other.IsLeader(ctx); err != nil {
		t.Fatalf("different name: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.IsLeader(ctx); err != nil {
		t.Fatalf("b after a closed: %v", err)
	}
	_ = b.Close()
	_ = other.Close()
}
