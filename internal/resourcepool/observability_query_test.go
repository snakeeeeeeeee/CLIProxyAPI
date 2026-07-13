package resourcepool

import (
	"context"
	"testing"
	"time"
)

func TestListRoutingEventsQueryFiltersPoolAndWindow(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	for _, event := range []RoutingEvent{
		{PoolID: DefaultAccountPoolID, RequestID: "old-default", Model: "claude-sonnet-4-6", Decision: "selected", CreatedAt: old},
		{PoolID: DefaultAccountPoolID, RequestID: "recent-default", Model: "claude-sonnet-4-6", Decision: "selected", CreatedAt: recent},
		{PoolID: "pool-b", RequestID: "recent-other", Model: "claude-sonnet-4-6", Decision: "selected", CreatedAt: recent},
	} {
		if err := store.RecordRoutingEvent(ctx, event); err != nil {
			t.Fatalf("RecordRoutingEvent(%s) error = %v", event.RequestID, err)
		}
	}

	events, err := store.ListRoutingEventsQuery(ctx, UsageQuery{PoolID: DefaultAccountPoolID, Window: 24 * time.Hour, Limit: 100})
	if err != nil {
		t.Fatalf("ListRoutingEventsQuery() error = %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "recent-default" {
		t.Fatalf("filtered events = %+v", events)
	}

	all, err := store.ListRoutingEventsQuery(ctx, UsageQuery{PoolID: DefaultAccountPoolID, AllTime: true, Limit: 100})
	if err != nil {
		t.Fatalf("ListRoutingEventsQuery(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all-time default events = %+v", all)
	}
}

func TestListRoutingEventsQueryPaginatesAndCounts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	createdAt := time.Now().Add(-time.Minute)
	for _, requestID := range []string{"event-1", "event-2", "event-3"} {
		if err := store.RecordRoutingEvent(ctx, RoutingEvent{
			PoolID:    DefaultAccountPoolID,
			RequestID: requestID,
			Decision:  "selected",
			CreatedAt: createdAt,
		}); err != nil {
			t.Fatalf("RecordRoutingEvent(%s) error = %v", requestID, err)
		}
	}

	query := UsageQuery{PoolID: DefaultAccountPoolID, AllTime: true, Limit: 1, Offset: 1}
	events, err := store.ListRoutingEventsQuery(ctx, query)
	if err != nil {
		t.Fatalf("ListRoutingEventsQuery() error = %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "event-2" {
		t.Fatalf("second page = %#v, want event-2", events)
	}
	total, err := store.CountRoutingEventsQuery(ctx, query)
	if err != nil {
		t.Fatalf("CountRoutingEventsQuery() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
}
