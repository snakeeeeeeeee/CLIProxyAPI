package claudeapipool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var debugAuthLabels = struct {
	sync.RWMutex
	values map[string]string
}{values: make(map[string]string)}

// DebugEnabled reports whether verbose Claude API pool diagnostics are enabled.
func DebugEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLAUDE_API_POOL_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

// DebugLogf emits a Claude API pool diagnostic line when CLAUDE_API_POOL_DEBUG is enabled.
func DebugLogf(format string, args ...any) {
	if !DebugEnabled() {
		return
	}
	log.Infof(format, args...)
}

// RegisterAuthDebugLabel stores a non-secret label used by debug routing logs.
func RegisterAuthDebugLabel(authID, label string) {
	authID = strings.TrimSpace(authID)
	label = strings.TrimSpace(label)
	if authID == "" {
		return
	}
	debugAuthLabels.Lock()
	defer debugAuthLabels.Unlock()
	if label == "" {
		delete(debugAuthLabels.values, authID)
		return
	}
	debugAuthLabels.values[authID] = label
}

// UnregisterAuthDebugLabel removes a debug label for an auth ID.
func UnregisterAuthDebugLabel(authID string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	debugAuthLabels.Lock()
	defer debugAuthLabels.Unlock()
	delete(debugAuthLabels.values, authID)
}

// DebugAuthRef returns a short non-secret account reference for diagnostics.
func DebugAuthRef(authID string) string {
	return debugAuthRef(authID)
}

func debugAuthRef(authID string) string {
	authID = strings.TrimSpace(authID)
	hash := debugShortHash(authID)
	if authID == "" {
		return "-"
	}
	debugAuthLabels.RLock()
	label := debugAuthLabels.values[authID]
	debugAuthLabels.RUnlock()
	if label != "" && hash != "" {
		return fmt.Sprintf("%s/%s", label, hash)
	}
	if label != "" {
		return label
	}
	if hash != "" {
		return hash
	}
	return "-"
}

func debugShortHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func debugTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func debugDurationMS(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Millisecond)
}

func debugUntilMS(now, until time.Time) int64 {
	if until.IsZero() || !until.After(now) {
		return 0
	}
	return debugDurationMS(until.Sub(now))
}
