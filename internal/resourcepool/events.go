package resourcepool

import (
	"context"
	"sync"
	"time"
)

const (
	EventConnected     = "connected"
	EventHeartbeat     = "heartbeat"
	EventProxyChanged  = "proxy_changed"
	EventAccountChange = "account_changed"
	EventModelChanged  = "model_changed"
	EventConfigChanged = "config_changed"
	EventStatsChanged  = "stats_changed"
)

// Event is a lightweight notification that tells management clients which
// resource class changed. It intentionally does not carry secrets or full rows.
type Event struct {
	Type      string    `json:"type"`
	Resource  string    `json:"resource,omitempty"`
	ID        string    `json:"id,omitempty"`
	Action    string    `json:"action,omitempty"`
	Timestamp time.Time `json:"ts"`
}

type eventHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
}

var defaultEventHub = &eventHub{subscribers: map[uint64]chan Event{}}

// SubscribeEvents subscribes to resource-pool change notifications.
func SubscribeEvents(ctx context.Context) (<-chan Event, func()) {
	return defaultEventHub.subscribe(ctx)
}

// PublishEvent broadcasts one resource-pool notification without blocking.
func PublishEvent(event Event) {
	defaultEventHub.publish(event)
}

func PublishProxyChanged(id, action string) {
	PublishEvent(Event{Type: EventProxyChanged, Resource: "proxy", ID: id, Action: action})
}

func PublishAccountChanged(id, action string) {
	PublishEvent(Event{Type: EventAccountChange, Resource: "account", ID: id, Action: action})
}

func PublishModelChanged(id, action string) {
	PublishEvent(Event{Type: EventModelChanged, Resource: "model", ID: id, Action: action})
}

func PublishConfigChanged(action string) {
	PublishEvent(Event{Type: EventConfigChanged, Resource: "config", Action: action})
}

func PublishStatsChanged(action string) {
	PublishEvent(Event{Type: EventStatsChanged, Resource: "stats", Action: action})
}

func (h *eventHub) subscribe(ctx context.Context) (<-chan Event, func()) {
	if h == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.subscribers[id] = ch
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.subscribers[id]; ok {
				delete(h.subscribers, id)
				close(ch)
			}
			h.mu.Unlock()
		})
	}
	if ctx != nil {
		go func() {
			<-ctx.Done()
			unsubscribe()
		}()
	}
	return ch, unsubscribe
}

func (h *eventHub) publish(event Event) {
	if h == nil || event.Type == "" {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}
