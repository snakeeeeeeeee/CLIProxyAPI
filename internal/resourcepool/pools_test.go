package resourcepool

import (
	"context"
	"errors"
	"testing"
)

func TestAccountPoolsDefaultMigrationAndCRUD(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	pools, err := store.ListAccountPools(ctx, false)
	if err != nil {
		t.Fatalf("ListAccountPools() error = %v", err)
	}
	if len(pools) != 1 || pools[0].ID != DefaultAccountPoolID || pools[0].Name != DefaultAccountPoolID || !pools[0].IsDefault {
		t.Fatalf("default pools = %+v", pools)
	}
	created, err := store.CreateAccountPool(ctx, "Team A", "isolated accounts")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	if created.ID == DefaultAccountPoolID || created.Name != "Team A" || !created.Enabled {
		t.Fatalf("created pool = %+v", created)
	}
	description := "updated"
	enabled := false
	updated, err := store.PatchAccountPool(ctx, created.ID, ClaudeCodeAccountPoolPatch{Description: &description, Enabled: &enabled})
	if err != nil {
		t.Fatalf("PatchAccountPool() error = %v", err)
	}
	if updated.Description != description || updated.Enabled {
		t.Fatalf("updated pool = %+v", updated)
	}
	if err := store.ArchiveAccountPool(ctx, created.ID); err != nil {
		t.Fatalf("ArchiveAccountPool() error = %v", err)
	}
	withArchived, err := store.ListAccountPools(ctx, true)
	if err != nil {
		t.Fatalf("ListAccountPools(include archived) error = %v", err)
	}
	if len(withArchived) != 2 || withArchived[1].ArchivedAt == nil {
		t.Fatalf("pools with archived = %+v", withArchived)
	}
}

func TestDefaultPoolIsImmutableAndPoolMembershipIsStrict(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	name := "renamed"
	if _, err := store.PatchAccountPool(ctx, DefaultAccountPoolID, ClaudeCodeAccountPoolPatch{Name: &name}); !errors.Is(err, ErrDefaultPoolImmutable) {
		t.Fatalf("PatchAccountPool(default) error = %v", err)
	}
	if err := store.ArchiveAccountPool(ctx, DefaultAccountPoolID); !errors.Is(err, ErrDefaultPoolImmutable) {
		t.Fatalf("ArchiveAccountPool(default) error = %v", err)
	}
	custom, err := store.CreateAccountPool(ctx, "Team B", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	account, err := store.RegisterClaudeCodeAccountInPool(ctx, custom.ID, "claude-team-b.json", "team-b@example.com", "")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountInPool() error = %v", err)
	}
	if account.PoolID != custom.ID {
		t.Fatalf("account pool = %q, want %q", account.PoolID, custom.ID)
	}
	if _, err := store.RegisterClaudeCodeAccountInPool(ctx, DefaultAccountPoolID, account.AuthID, account.Email, ""); !errors.Is(err, ErrAccountInOtherPool) {
		t.Fatalf("cross-pool register error = %v", err)
	}
	if err := store.ArchiveAccountPool(ctx, custom.ID); !errors.Is(err, ErrAccountPoolNotEmpty) {
		t.Fatalf("ArchiveAccountPool(non-empty) error = %v", err)
	}
	defaultAccounts, err := store.ListAccountsByPool(ctx, DefaultAccountPoolID)
	if err != nil {
		t.Fatalf("ListAccountsByPool(default) error = %v", err)
	}
	if len(defaultAccounts) != 0 {
		t.Fatalf("default account count = %d, want 0", len(defaultAccounts))
	}
	moved, err := store.MoveAccountToPool(ctx, account.ID, DefaultAccountPoolID)
	if err != nil {
		t.Fatalf("MoveAccountToPool() error = %v", err)
	}
	if moved.PoolID != DefaultAccountPoolID {
		t.Fatalf("moved account pool = %q", moved.PoolID)
	}
}
