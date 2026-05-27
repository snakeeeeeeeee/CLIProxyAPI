package claudeapipool

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestVirtualCacheLedgerRewritesClaudeUsageOnSecondRequest(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{Enabled: true, HitRate: 0.90})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{
		"system": [{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	first := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if first == nil {
		t.Fatal("first transaction is nil")
	}
	upstream := []byte(`{"usage":{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":90}}`)
	out := first.RewriteClaudeResponseUsage(upstream)
	first.Commit()
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != 90 {
		t.Fatalf("first cache_creation_input_tokens = %d, want 90", got)
	}
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Exists(); got {
		t.Fatalf("first cache_read_input_tokens should not be added: %s", out)
	}

	second := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out = second.RewriteClaudeResponseUsage(upstream)
	second.Commit()
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 100 {
		t.Fatalf("second input_tokens = %d, want 100", got)
	}
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 90 {
		t.Fatalf("second cache_read_input_tokens = %d, want 90", got)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != 0 {
		t.Fatalf("second cache_creation_input_tokens = %d, want 0", got)
	}
}

func TestVirtualCacheLedgerRewritesClaudeStreamUsage(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{Enabled: true, HitRate: 0.90})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{
		"messages": [{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]
	}`)
	first := ledger.begin("claude", "claude-sonnet", "session-1", request, time.Now())
	line := []byte(`data: {"type":"message_delta","usage":{"input_tokens":40,"output_tokens":3,"cache_creation_input_tokens":30}}`)
	_ = first.RewriteClaudeStreamLine(line)
	first.Commit()

	second := ledger.begin("claude", "claude-sonnet", "session-1", request, time.Now())
	out := second.RewriteClaudeStreamLine(line)
	second.Commit()
	payload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(out), "data:")))
	if got := payload.Get("usage.input_tokens").Int(); got != 40 {
		t.Fatalf("stream input_tokens = %d, want 40; out=%s", got, out)
	}
	if got := payload.Get("usage.cache_read_input_tokens").Int(); got != 30 {
		t.Fatalf("stream cache_read_input_tokens = %d, want 30; out=%s", got, out)
	}
	if got := payload.Get("usage.cache_creation_input_tokens").Int(); got != 0 {
		t.Fatalf("stream cache_creation_input_tokens = %d, want 0; out=%s", got, out)
	}
}

func TestVirtualCacheLedgerTTLExpires(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	now := time.Now()
	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	first := ledger.begin("claude", "claude-haiku", "session-1", request, now)
	_ = first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":10,"cache_creation_input_tokens":8}}`))
	ledger.commit(first)

	second := ledger.begin("claude", "claude-haiku", "session-1", request, now.Add(defaultCacheTTL+time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":10,"cache_creation_input_tokens":8}}`))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Exists(); got {
		t.Fatalf("expired entry should not rewrite cache_read_input_tokens: %s", out)
	}
}

func TestVirtualCacheLedgerEstimatesWhenUpstreamCacheUsageIsZero(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:               true,
		HitRate:               0.90,
		TargetCacheReuseRatio: 0.90,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	prefix := strings.Repeat("stable prompt cache prefix ", 200)
	request := []byte(`{
		"system": [{"type":"text","text":` + strconv.Quote(prefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)
	upstream := []byte(`{"usage":{"input_tokens":1500,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)

	first := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if first == nil {
		t.Fatal("first transaction is nil")
	}
	out := first.RewriteClaudeResponseUsage(upstream)
	first.Commit()
	created := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int()
	if created <= 0 {
		t.Fatalf("first cache_creation_input_tokens = %d, want > 0; out=%s", created, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation.ephemeral_1h_input_tokens").Int(); got != created {
		t.Fatalf("ephemeral_1h_input_tokens = %d, want %d; out=%s", got, created, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first cache_read_input_tokens = %d, want 0; out=%s", got, out)
	}
	if sum := rewrittenUsageTotal(out); sum != 1500 {
		t.Fatalf("first rewritten total = %d, want 1500; out=%s", sum, out)
	}

	second := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out = second.RewriteClaudeResponseUsage(upstream)
	second.Commit()
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 1350 {
		t.Fatalf("second cache_read_input_tokens = %d, want 1350; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != 0 {
		t.Fatalf("second cache_creation_input_tokens = %d, want 0; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 150 {
		t.Fatalf("second input_tokens = %d, want 150; out=%s", got, out)
	}
	if sum := rewrittenUsageTotal(out); sum != 1500 {
		t.Fatalf("second rewritten total = %d, want 1500; out=%s", sum, out)
	}
}

func TestVirtualCacheLedgerConfigCapsEstimatedTokens(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:             true,
		HitRate:             1,
		MaxCacheTokens:      100,
		UncachedInputTokens: 25,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"system":[{"type":"text","text":"` + strings.Repeat("cache me ", 200) + `","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`)
	first := ledger.begin("claude", "claude-haiku", "session-1", request, time.Now())
	out := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1000,"output_tokens":1}}`))
	first.Commit()
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != 100 {
		t.Fatalf("cache_creation_input_tokens = %d, want max cap 100; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 900 {
		t.Fatalf("input_tokens = %d, want 900; out=%s", got, out)
	}

	second := ledger.begin("claude", "claude-haiku", "session-1", request, time.Now())
	out = second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":80,"output_tokens":1}}`))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 55 {
		t.Fatalf("cache_read_input_tokens = %d, want uncached floor 55; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 25 {
		t.Fatalf("input_tokens = %d, want uncached floor 25; out=%s", got, out)
	}
}

func TestVirtualCacheLedgerKeepsClaudeLikeRollingCacheAcrossPrefixGrowth(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.90,
		TargetCacheReuseRatio:   0.90,
		ContextShrinkResetRatio: 0.70,
		MinCreationTokens:       0,
		MaxCreationTokens:       400,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	firstPrefix := strings.Repeat("stable cache prefix ", 200)
	secondPrefix := firstPrefix + strings.Repeat("new turn content ", 80)
	firstRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(firstPrefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	secondRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(secondPrefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	first := ledger.begin("claude", "claude-sonnet", "session-rolling", firstRequest, time.Now())
	firstOut := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1400,"output_tokens":1}}`))
	first.Commit()
	created := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int()
	if created <= 0 {
		t.Fatalf("first creation = %d, want > 0; out=%s", created, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-rolling", secondRequest, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1800,"output_tokens":1}}`))
	second.Commit()
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != 1620 {
		t.Fatalf("second cache_read_input_tokens = %d, want 1620; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("second cache_creation_input_tokens = %d, want rolling delta creation; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 1800 {
		t.Fatalf("second rewritten total = %d, want 1800; out=%s", sum, secondOut)
	}
}

func TestVirtualCacheLedgerResetsOnContextShrink(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.90,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	largePrefix := strings.Repeat("long cache prefix ", 500)
	smallPrefix := strings.Repeat("short cache prefix ", 40)
	largeRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(largePrefix) + `,"cache_control":{"type":"ephemeral"}}]}]}`)
	smallRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(smallPrefix) + `,"cache_control":{"type":"ephemeral"}}]}]}`)

	first := ledger.begin("claude", "claude-sonnet", "session-compress", largeRequest, time.Now())
	_ = first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":3000,"output_tokens":1}}`))
	first.Commit()

	compressed := ledger.begin("claude", "claude-sonnet", "session-compress", smallRequest, time.Now())
	if compressed == nil {
		t.Fatal("compressed transaction is nil")
	}
	out := compressed.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":400,"output_tokens":1}}`))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("compressed cache_read_input_tokens = %d, want 0 after shrink reset; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("compressed cache_creation_input_tokens = %d, want rebuilt creation; out=%s", got, out)
	}
}

func TestVirtualCacheReuseSnapshotTracksWindow(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	now := time.Now()

	ledger.mu.Lock()
	ledger.recordReuseSampleLocked(10, 90, 0, now.Add(-cacheReuseWindow-time.Second))
	ledger.recordReuseSampleLocked(20, 80, 100, now)
	ledger.mu.Unlock()

	snapshot := ledger.reuseSnapshot(0.9, now)
	if !snapshot.Enabled {
		t.Fatal("snapshot.Enabled = false, want true")
	}
	if snapshot.WindowSeconds != int64(cacheReuseWindow/time.Second) {
		t.Fatalf("WindowSeconds = %d, want %d", snapshot.WindowSeconds, int64(cacheReuseWindow/time.Second))
	}
	if snapshot.SampleCount != 1 {
		t.Fatalf("SampleCount = %d, want 1", snapshot.SampleCount)
	}
	if snapshot.InputTokens != 20 || snapshot.CacheReadInputTokens != 80 || snapshot.CacheCreationInputTokens != 100 {
		t.Fatalf("snapshot tokens = %#v", snapshot)
	}
	if snapshot.DenominatorTokens != 200 {
		t.Fatalf("DenominatorTokens = %d, want 200", snapshot.DenominatorTokens)
	}
	if snapshot.ActualRatio != 0.4 {
		t.Fatalf("ActualRatio = %v, want 0.4", snapshot.ActualRatio)
	}
}

func TestVirtualCacheTargetReuseRatioZeroKeepsPolicy(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	policy := EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.90,
		TargetCacheReuseRatio:   0,
		UncachedInputTokens:     20,
		ContextShrinkResetRatio: 0.70,
		MinCreationTokens:       10,
		MaxCreationTokens:       400,
	}
	ledger.mu.Lock()
	ledger.recordReuseSampleLocked(900, 50, 50, time.Now())
	ledger.recordReuseSampleLocked(900, 50, 50, time.Now().Add(time.Second))
	ledger.recordReuseSampleLocked(900, 50, 50, time.Now().Add(2*time.Second))
	ledger.mu.Unlock()

	got := ledger.effectivePolicy(policy, time.Now())
	if got.HitRate != policy.HitRate ||
		got.UncachedInputTokens != policy.UncachedInputTokens ||
		got.MinCreationTokens != policy.MinCreationTokens ||
		got.MaxCreationTokens != policy.MaxCreationTokens ||
		got.ReadScale != 0 {
		t.Fatalf("effectivePolicy changed when target disabled: got %#v want %#v", got, policy)
	}
}

func TestVirtualCacheTargetReuseRatioBiasesFutureRewritesTowardTarget(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.50,
		TargetCacheReuseRatio:   0.90,
		ContextShrinkResetRatio: 0.70,
		UncachedInputTokens:     100,
		MinCreationTokens:       0,
		MaxCreationTokens:       1200,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	ledger.mu.Lock()
	now := time.Now()
	ledger.recordReuseSampleLocked(900, 50, 50, now)
	ledger.recordReuseSampleLocked(900, 50, 50, now.Add(time.Second))
	ledger.recordReuseSampleLocked(900, 50, 50, now.Add(2*time.Second))
	ledger.mu.Unlock()

	prefix := strings.Repeat("target cache prefix ", 500)
	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(prefix) + `,"cache_control":{"type":"ephemeral"}}]}]}`)

	first := ledger.begin("claude", "claude-opus", "session-target", request, now.Add(3*time.Second))
	if first == nil {
		t.Fatal("first transaction is nil")
	}
	firstOut := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	first.Commit()
	firstCreated := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int()
	if firstCreated <= 0 {
		t.Fatalf("first creation = %d, want > 0; out=%s", firstCreated, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-target", request, now.Add(4*time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got <= firstCreated {
		t.Fatalf("cache_read_input_tokens = %d, want > baseline creation %d; out=%s", got, firstCreated, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got >= 2000-firstCreated {
		t.Fatalf("input_tokens = %d, want lower than baseline %d; out=%s", got, 2000-firstCreated, secondOut)
	}
}

func TestVirtualCacheTargetReuseRatioRewritesTowardConfiguredRatio(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.50,
		TargetCacheReuseRatio:   0.95,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(strings.Repeat("cache prefix ", 500)) + `,"cache_control":{"type":"ephemeral"}}]}]}`)
	first := ledger.begin("claude", "claude-opus", "session-force-target", request, time.Now())
	firstOut := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	first.Commit()
	if created := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); created != 1000 {
		t.Fatalf("first creation = %d, want 1000; out=%s", created, firstOut)
	}
	if read := gjson.GetBytes(firstOut, "usage.cache_read_input_tokens").Int(); read != 0 {
		t.Fatalf("first cache_read_input_tokens = %d, want 0; out=%s", read, firstOut)
	}
	if got := gjson.GetBytes(firstOut, "usage.input_tokens").Int(); got != 1000 {
		t.Fatalf("first input_tokens = %d, want 1000; out=%s", got, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-force-target", request, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	read := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int()
	if read != 1900 {
		t.Fatalf("cache_read_input_tokens = %d, want 1900; out=%s", read, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != 100 {
		t.Fatalf("input_tokens = %d, want 100; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 2000 {
		t.Fatalf("second rewritten total = %d, want 2000; out=%s", sum, secondOut)
	}
}

func TestVirtualCacheTargetReuseRatioRewritesStreamUsage(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.50,
		TargetCacheReuseRatio:   0.90,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(strings.Repeat("stream cache prefix ", 300)) + `,"cache_control":{"type":"ephemeral"}}]}]}`)
	line := []byte(`data: {"type":"message_delta","usage":{"input_tokens":1000,"output_tokens":3}}`)

	first := ledger.begin("claude", "claude-sonnet", "session-stream-target", request, time.Now())
	firstOut := first.RewriteClaudeStreamLine(line)
	first.Commit()
	firstPayload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(firstOut), "data:")))
	if got := firstPayload.Get("usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first stream cache_read_input_tokens = %d, want 0; out=%s", got, firstOut)
	}
	if got := firstPayload.Get("usage.cache_creation_input_tokens").Int(); got != 500 {
		t.Fatalf("first stream cache_creation_input_tokens = %d, want 500; out=%s", got, firstOut)
	}
	if got := firstPayload.Get("usage.input_tokens").Int(); got != 500 {
		t.Fatalf("first stream input_tokens = %d, want 500; out=%s", got, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-stream-target", request, time.Now())
	secondOut := second.RewriteClaudeStreamLine(line)
	second.Commit()
	secondPayload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(secondOut), "data:")))
	if got := secondPayload.Get("usage.cache_read_input_tokens").Int(); got != 900 {
		t.Fatalf("second stream cache_read_input_tokens = %d, want 900; out=%s", got, secondOut)
	}
	if got := secondPayload.Get("usage.cache_creation_input_tokens").Int(); got != 0 {
		t.Fatalf("second stream cache_creation_input_tokens = %d, want 0; out=%s", got, secondOut)
	}
	if got := secondPayload.Get("usage.input_tokens").Int(); got != 100 {
		t.Fatalf("second stream input_tokens = %d, want 100; out=%s", got, secondOut)
	}
}

func rewrittenUsageTotal(payload []byte) int64 {
	usage := gjson.GetBytes(payload, "usage")
	return usage.Get("input_tokens").Int() +
		usage.Get("cache_creation_input_tokens").Int() +
		usage.Get("cache_read_input_tokens").Int()
}
