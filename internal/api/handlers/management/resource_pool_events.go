package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
)

// StreamResourcePoolEvents streams resource-pool change notifications using SSE.
func (h *Handler) StreamResourcePoolEvents(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	events, unsubscribe := resourcepool.SubscribeEvents(c.Request.Context())
	defer unsubscribe()

	writeEvent := func(event resourcepool.Event) bool {
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeEvent(resourcepool.Event{Type: resourcepool.EventConnected, Resource: "events"}) {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if !writeEvent(event) {
				return
			}
		case <-ticker.C:
			if !writeEvent(resourcepool.Event{Type: resourcepool.EventHeartbeat, Resource: "events"}) {
				return
			}
		}
	}
}
