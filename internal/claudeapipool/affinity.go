package claudeapipool

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const affinityProvider = "claude"

type affinityRouter struct {
	mu      sync.Mutex
	entries map[string]*affinityEntry
}

type affinityEntry struct {
	authIDs   []string
	pressure  int
	expiresAt time.Time
	updatedAt time.Time
}

// AffinityRequest describes a request that may benefit from real upstream cache affinity.
type AffinityRequest struct {
	Provider          string
	Model             string
	SessionKey        string
	PrefixFingerprint string
	EstimateTokens    int64
	TTL               time.Duration
}

// AffinitySelection describes one selected account and whether affinity was active.
type AffinitySelection struct {
	AuthID string
	Key    string
	Active bool
}

var defaultAffinity = &affinityRouter{entries: make(map[string]*affinityEntry)}

// BuildAffinityRequest inspects a translated Claude payload and returns a
// cache-affinity request descriptor when the current routing policy applies.
func BuildAffinityRequest(model, sessionKey string, payload []byte) (AffinityRequest, bool) {
	policy := CurrentRoutingConfig()
	if !policy.CacheAffinityEnabled {
		return AffinityRequest{}, false
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return AffinityRequest{}, false
	}
	info := AnalyzeCachePrefix(payload)
	if !info.OK || info.Fingerprint == "" {
		return AffinityRequest{}, false
	}
	if info.EstimateTokens < int64(policy.CacheAffinityMinTokens) {
		return AffinityRequest{}, false
	}
	ttl := info.TTL
	configuredTTL := time.Duration(policy.CacheAffinityTTLMS) * time.Millisecond
	if configuredTTL > 0 && (ttl <= 0 || configuredTTL < ttl) {
		ttl = configuredTTL
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return AffinityRequest{
		Provider:          affinityProvider,
		Model:             strings.TrimSpace(model),
		SessionKey:        sessionKey,
		PrefixFingerprint: info.Fingerprint,
		EstimateTokens:    info.EstimateTokens,
		TTL:               ttl,
	}, true
}

// SelectAffinityAuth returns the preferred auth ID for the request. It tries
// warm lanes first, then expands within the stable hash order, then falls back
// to the full pool order when all affinity candidates are unavailable.
func SelectAffinityAuth(req AffinityRequest, authIDs []string, unavailable map[string]struct{}) AffinitySelection {
	return defaultAffinity.selectAuth(req, authIDs, unavailable, CurrentRoutingConfig(), time.Now())
}

// NoteAffinityResult updates pressure for automatic lane adjustment.
func NoteAffinityResult(selection AffinitySelection, statusCode int, success bool) {
	defaultAffinity.noteResult(selection, statusCode, success, CurrentRoutingConfig(), time.Now())
}

// AffinityStats returns active affinity keys and warm lane count.
func AffinityStats() (int, int) {
	return defaultAffinity.stats(time.Now())
}

func (r *affinityRouter) selectAuth(req AffinityRequest, authIDs []string, unavailable map[string]struct{}, policy EffectiveRoutingConfig, now time.Time) AffinitySelection {
	key := affinityKey(req)
	if r == nil || key == "" || len(authIDs) == 0 {
		return AffinitySelection{}
	}
	ordered := rendezvousOrder(key, authIDs)
	if len(ordered) == 0 {
		return AffinitySelection{}
	}
	policy = normalizeEffectiveRoutingConfig(policy)
	laneTarget, laneMax := affinityLaneBoundsForSelection(policy, ordered, unavailable)
	if laneTarget <= 0 || laneMax <= 0 {
		return AffinitySelection{Key: key, Active: true}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	entry := r.entries[key]
	if entry == nil {
		entry = &affinityEntry{}
		r.entries[key] = entry
	}
	entry.expiresAt = now.Add(req.TTL)
	entry.updatedAt = now
	if policy.CacheAffinityAuto && entry.pressure > 0 {
		laneTarget = minInt(laneMax, laneTarget+entry.pressure)
	}
	entry.authIDs = normalizeWarmLanes(entry.authIDs, ordered, laneTarget)
	for _, authID := range entry.authIDs {
		if _, blocked := unavailable[authID]; !blocked {
			return AffinitySelection{AuthID: authID, Key: key, Active: true}
		}
	}
	for _, authID := range ordered {
		if _, blocked := unavailable[authID]; blocked {
			continue
		}
		if !containsString(entry.authIDs, authID) {
			entry.authIDs = append(entry.authIDs, authID)
			if len(entry.authIDs) > laneMax {
				entry.authIDs = entry.authIDs[len(entry.authIDs)-laneMax:]
			}
		}
		return AffinitySelection{AuthID: authID, Key: key, Active: true}
	}
	return AffinitySelection{Key: key, Active: true}
}

func (r *affinityRouter) noteResult(selection AffinitySelection, statusCode int, success bool, policy EffectiveRoutingConfig, now time.Time) {
	if r == nil || !selection.Active || selection.Key == "" || !policy.CacheAffinityAuto {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[selection.Key]
	if entry == nil {
		return
	}
	entry.updatedAt = now
	switch {
	case success:
		if entry.pressure > 0 {
			entry.pressure--
		}
	case statusCode == StatusTooManyRequests || statusCode == StatusOverloaded || statusCode >= http.StatusInternalServerError:
		if entry.pressure < policy.CacheAffinityMaxLanes {
			entry.pressure++
		}
	}
}

func (r *affinityRouter) stats(now time.Time) (int, int) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	active := len(r.entries)
	lanes := 0
	for _, entry := range r.entries {
		if entry != nil {
			lanes += len(entry.authIDs)
		}
	}
	return active, lanes
}

func (r *affinityRouter) pruneLocked(now time.Time) {
	for key, entry := range r.entries {
		if entry == nil || (!entry.expiresAt.IsZero() && !entry.expiresAt.After(now)) {
			delete(r.entries, key)
		}
	}
}

func affinityKey(req AffinityRequest) string {
	parts := []string{
		strings.TrimSpace(req.Provider),
		strings.TrimSpace(req.Model),
		strings.TrimSpace(req.SessionKey),
		strings.TrimSpace(req.PrefixFingerprint),
	}
	for _, part := range parts {
		if part == "" {
			return ""
		}
	}
	return strings.Join(parts, "\x00")
}

func rendezvousOrder(key string, authIDs []string) []string {
	unique := make(map[string]struct{}, len(authIDs))
	entries := make([]affinityScore, 0, len(authIDs))
	for _, authID := range authIDs {
		authID = strings.TrimSpace(authID)
		if authID == "" {
			continue
		}
		if _, seen := unique[authID]; seen {
			continue
		}
		unique[authID] = struct{}{}
		entries = append(entries, affinityScore{
			authID: authID,
			score:  hashAffinityScore(key, authID),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score == entries[j].score {
			return entries[i].authID < entries[j].authID
		}
		return entries[i].score > entries[j].score
	})
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.authID)
	}
	return out
}

type affinityScore struct {
	authID string
	score  uint64
}

func hashAffinityScore(key, authID string) uint64 {
	sum := sha256.Sum256([]byte(key + "\x00" + authID))
	return binary.BigEndian.Uint64(sum[:8])
}

func normalizeWarmLanes(current, ordered []string, target int) []string {
	if target <= 0 || len(ordered) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(ordered))
	for _, authID := range ordered {
		allowed[authID] = struct{}{}
	}
	out := make([]string, 0, target)
	for _, authID := range current {
		if len(out) >= target {
			break
		}
		if _, ok := allowed[authID]; !ok || containsString(out, authID) {
			continue
		}
		out = append(out, authID)
	}
	for _, authID := range ordered {
		if len(out) >= target {
			break
		}
		if !containsString(out, authID) {
			out = append(out, authID)
		}
	}
	return out
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
