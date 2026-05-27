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
	wantInput := int64(100)
	wantCreation := int64(90)
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != wantInput {
		t.Fatalf("first input_tokens = %d, want anchored upstream input %d; out=%s", got, wantInput, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != wantCreation {
		t.Fatalf("first cache_creation_input_tokens = %d, want anchored budget %d", got, wantCreation)
	}
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first cache_read_input_tokens = %d, want 0: %s", got, out)
	}

	second := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out = second.RewriteClaudeResponseUsage(upstream)
	second.Commit()
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != wantInput {
		t.Fatalf("second input_tokens = %d, want anchored upstream input %d", got, wantInput)
	}
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != wantCreation {
		t.Fatalf("second cache_read_input_tokens = %d, want prior local creation %d", got, wantCreation)
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
	firstOut := first.RewriteClaudeStreamLine(line)
	first.Commit()
	firstPayload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(firstOut), "data:")))
	wantCreation := int64(30)
	if got := firstPayload.Get("usage.input_tokens").Int(); got != 40 {
		t.Fatalf("first stream input_tokens = %d, want anchored upstream input 40; out=%s", got, firstOut)
	}
	if got := firstPayload.Get("usage.cache_creation_input_tokens").Int(); got != wantCreation {
		t.Fatalf("first stream cache_creation_input_tokens = %d, want anchored budget %d; out=%s", got, wantCreation, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-1", request, time.Now())
	out := second.RewriteClaudeStreamLine(line)
	second.Commit()
	payload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(out), "data:")))
	if got := payload.Get("usage.input_tokens").Int(); got != 40 {
		t.Fatalf("stream input_tokens = %d, want anchored upstream input 40; out=%s", got, out)
	}
	if got := payload.Get("usage.cache_read_input_tokens").Int(); got != wantCreation {
		t.Fatalf("stream cache_read_input_tokens = %d, want prior local creation %d; out=%s", got, wantCreation, out)
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
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("expired entry should not rewrite cache_read_input_tokens: %s", out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != 8 {
		t.Fatalf("expired entry should rebuild creation from anchored budget: %s", out)
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
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("first input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, out)
	}
	if sum := rewrittenUsageTotal(out); sum != 1500 {
		t.Fatalf("first rewritten total = %d, want anchored total 1500; out=%s", sum, out)
	}

	second := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out = second.RewriteClaudeResponseUsage(upstream)
	second.Commit()
	cacheBudget := int64(1500) - second.deltaTokens
	wantRead := minInt64(created, cacheBudget)
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second cache_read_input_tokens = %d, want prior local cache read %d; out=%s", got, wantRead, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != cacheBudget-wantRead {
		t.Fatalf("second cache_creation_input_tokens = %d, want remaining local budget; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, out)
	}
	if sum := rewrittenUsageTotal(out); sum != 1500 {
		t.Fatalf("second rewritten total = %d, want anchored total 1500; out=%s", sum, out)
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
		t.Fatalf("input_tokens = %d, want anchored total minus max cache cap; out=%s", got, out)
	}

	second := ledger.begin("claude", "claude-haiku", "session-1", request, time.Now())
	out = second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":80,"output_tokens":1}}`))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 55 {
		t.Fatalf("cache_read_input_tokens = %d, want anchored budget after uncached floor; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 25 {
		t.Fatalf("input_tokens = %d, want uncached input floor; out=%s", got, out)
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
	cacheBudget := int64(1800) - second.deltaTokens
	wantRead := minInt64(created, cacheBudget)
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second cache_read_input_tokens = %d, want prior local cache read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("second cache_creation_input_tokens = %d, want rolling delta creation; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 1800 {
		t.Fatalf("second rewritten total = %d, want anchored total 1800; out=%s", sum, secondOut)
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
	firstBudget := int64(2000) - first.deltaTokens
	if firstCreated != firstBudget {
		t.Fatalf("first creation = %d, want anchored budget %d; out=%s", firstCreated, firstBudget, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-target", request, now.Add(4*time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	secondBudget := int64(2000) - second.deltaTokens
	wantRead := minInt64(firstCreated, secondBudget)
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("cache_read_input_tokens = %d, want prior local cache read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 2000 {
		t.Fatalf("second rewritten total = %d, want anchored total 2000; out=%s", sum, secondOut)
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
	firstBudget := int64(2000) - first.deltaTokens
	if created := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); created != firstBudget {
		t.Fatalf("first creation = %d, want anchored budget %d; out=%s", created, firstBudget, firstOut)
	}
	if read := gjson.GetBytes(firstOut, "usage.cache_read_input_tokens").Int(); read != 0 {
		t.Fatalf("first cache_read_input_tokens = %d, want 0; out=%s", read, firstOut)
	}
	if got := gjson.GetBytes(firstOut, "usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("first input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-force-target", request, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	read := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int()
	secondBudget := int64(2000) - second.deltaTokens
	wantRead := minInt64(firstBudget, secondBudget)
	if read != wantRead {
		t.Fatalf("cache_read_input_tokens = %d, want prior local cache read %d; out=%s", read, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 2000 {
		t.Fatalf("second rewritten total = %d, want anchored total 2000; out=%s", sum, secondOut)
	}
}

func TestVirtualCacheTargetReuseRatioIgnoresUpstreamCacheBreakdown(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.90,
		TargetCacheReuseRatio:   0.92,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(strings.Repeat("cache prefix ", 500)) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	first := ledger.begin("claude", "claude-opus", "session-preserve-input", request, time.Now())
	firstOut := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":23000,"cache_read_input_tokens":0,"output_tokens":1}}`))
	first.Commit()
	if got := gjson.GetBytes(firstOut, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("first input_tokens = %d, want anchored upstream input 1; out=%s", got, firstOut)
	}
	firstBudget := int64(23000)
	if got := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); got != firstBudget {
		t.Fatalf("first creation = %d, want anchored budget %d; out=%s", got, firstBudget, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-preserve-input", request, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":900,"cache_read_input_tokens":24000,"output_tokens":1}}`))
	second.Commit()
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("second input_tokens = %d, want anchored upstream input 1; out=%s", got, secondOut)
	}
	secondBudget := int64(24900)
	wantRead := minInt64(firstBudget, secondBudget)
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second read = %d, want prior local cache read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got != secondBudget-wantRead {
		t.Fatalf("second creation = %d, want remaining local budget; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 24901 {
		t.Fatalf("second total = %d, want anchored total 24901; out=%s", sum, secondOut)
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
	firstBudget := int64(1000) - first.deltaTokens
	if got := firstPayload.Get("usage.cache_creation_input_tokens").Int(); got != firstBudget {
		t.Fatalf("first stream cache_creation_input_tokens = %d, want anchored budget %d; out=%s", got, firstBudget, firstOut)
	}
	if got := firstPayload.Get("usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first stream cache_read_input_tokens = %d, want 0; out=%s", got, firstOut)
	}
	if got := firstPayload.Get("usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("first stream input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-stream-target", request, time.Now())
	secondOut := second.RewriteClaudeStreamLine(line)
	second.Commit()
	secondPayload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(secondOut), "data:")))
	secondBudget := int64(1000) - second.deltaTokens
	wantRead := minInt64(firstBudget, secondBudget)
	if got := secondPayload.Get("usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second stream cache_read_input_tokens = %d, want prior local cache read %d; out=%s", got, wantRead, secondOut)
	}
	if got := secondPayload.Get("usage.cache_creation_input_tokens").Int(); got != secondBudget-wantRead {
		t.Fatalf("second stream cache_creation_input_tokens = %d, want remaining local budget; out=%s", got, secondOut)
	}
	if got := secondPayload.Get("usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second stream input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotalFromResult(secondPayload.Get("usage")); sum != 1000 {
		t.Fatalf("second stream total = %d, want anchored total 1000; out=%s", sum, secondOut)
	}
}

func TestVirtualCacheTargetReuseRatioAutoCreatesDeltaWithoutAdvancedCaps(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.90,
		TargetCacheReuseRatio:   0.90,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	firstPrefix := strings.Repeat("stable target cache prefix ", 200)
	secondPrefix := firstPrefix + strings.Repeat("new user turn ", 100)
	firstRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(firstPrefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	secondRequest := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(secondPrefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	first := ledger.begin("claude", "claude-opus", "session-auto-delta", firstRequest, time.Now())
	firstOut := first.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	first.Commit()
	firstBudget := int64(2000) - first.deltaTokens
	if got := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); got != firstBudget {
		t.Fatalf("first creation = %d, want anchored budget %d; out=%s", got, firstBudget, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-auto-delta", secondRequest, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2300,"output_tokens":1}}`))
	second.Commit()
	secondBudget := int64(2300) - second.deltaTokens
	wantRead := minInt64(firstBudget, secondBudget)
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second read = %d, want prior local cache read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("second creation = %d, want auto delta creation; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second input = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != 2300 {
		t.Fatalf("second total = %d, want anchored total 2300; out=%s", sum, secondOut)
	}
}

func TestVirtualCacheLedgerAnchorsCCTestLikeMultiRoundAudit(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		HitRate:                 0.92,
		TargetCacheReuseRatio:   0.92,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	prefix := strings.Repeat("cctest stable cache prefix ", 1800)
	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + strconv.Quote(prefix) + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	usages := []struct {
		name     string
		upstream string
		total    int64
		input    int64
		output   int64
	}{
		{name: "R1", upstream: `{"usage":{"input_tokens":6,"cache_creation_input_tokens":8625,"cache_read_input_tokens":15617,"output_tokens":150}}`, total: 24248, input: 6, output: 150},
		{name: "R2", upstream: `{"usage":{"input_tokens":1,"cache_creation_input_tokens":9047,"cache_read_input_tokens":15617,"output_tokens":173}}`, total: 24665, input: 1, output: 173},
		{name: "R3", upstream: `{"usage":{"input_tokens":1,"cache_creation_input_tokens":591,"cache_read_input_tokens":24242,"output_tokens":117}}`, total: 24834, input: 1, output: 117},
		{name: "R4", upstream: `{"usage":{"input_tokens":1,"cache_creation_input_tokens":1010,"cache_read_input_tokens":24664,"output_tokens":359}}`, total: 25675, input: 1, output: 359},
	}

	var previousCached int64
	for idx, tc := range usages {
		tx := ledger.begin("claude", "claude-opus-4-7", "session-cctest", request, time.Now().Add(time.Duration(idx)*time.Second))
		if tx == nil {
			t.Fatalf("%s transaction is nil", tc.name)
		}
		out := tx.RewriteClaudeResponseUsage([]byte(tc.upstream))
		tx.Commit()

		if got := rewrittenUsageTotal(out); got != tc.total {
			t.Fatalf("%s total = %d, want anchored upstream total %d; out=%s", tc.name, got, tc.total, out)
		}
		if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != tc.input {
			t.Fatalf("%s input_tokens = %d, want upstream input %d; out=%s", tc.name, got, tc.input, out)
		}
		if got := gjson.GetBytes(out, "usage.output_tokens").Int(); got != tc.output {
			t.Fatalf("%s output_tokens = %d, want upstream output %d; out=%s", tc.name, got, tc.output, out)
		}

		cacheBudget := tc.total - tc.input
		read := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int()
		creation := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int()
		if idx == 0 {
			if read != 0 {
				t.Fatalf("%s read = %d, want 0 on first local ledger request; out=%s", tc.name, read, out)
			}
			if creation != cacheBudget {
				t.Fatalf("%s creation = %d, want full cache budget %d; out=%s", tc.name, creation, cacheBudget, out)
			}
		} else {
			wantRead := minInt64(previousCached, cacheBudget)
			if read != wantRead {
				t.Fatalf("%s read = %d, want prior local cache read %d; out=%s", tc.name, read, wantRead, out)
			}
			if creation != cacheBudget-read {
				t.Fatalf("%s creation = %d, want remaining budget %d; out=%s", tc.name, creation, cacheBudget-read, out)
			}
		}
		previousCached = read + creation
	}
}

func TestVirtualCacheLedgerForcedModeTargetsWarmRoundReuse(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:                 true,
		Mode:                    VirtualCacheModeForced,
		HitRate:                 0.90,
		TargetCacheReuseRatio:   0.999,
		ContextShrinkResetRatio: 0.70,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"stable cache prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	upstream := []byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":9000,"output_tokens":12}}`)

	first := ledger.begin("claude", "claude-opus", "session-forced", request, time.Now())
	if first == nil {
		t.Fatal("first transaction is nil")
	}
	firstOut := first.RewriteClaudeResponseUsage(upstream)
	first.Commit()
	if got := gjson.GetBytes(firstOut, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first read = %d, want cold-start 0; out=%s", got, firstOut)
	}
	if got := rewrittenUsageTotal(firstOut); got != 10001 {
		t.Fatalf("first total = %d, want anchored total 10001; out=%s", got, firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-forced", request, time.Now().Add(time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage(upstream)
	second.Commit()

	cacheBudget := int64(10000)
	wantRead := int64(9990)
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("second input = %d, want anchored upstream input 1; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second read = %d, want forced target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got != cacheBudget-wantRead {
		t.Fatalf("second creation = %d, want forced remainder %d; out=%s", got, cacheBudget-wantRead, secondOut)
	}
	if got := rewrittenUsageTotal(secondOut); got != 10001 {
		t.Fatalf("second total = %d, want anchored total 10001; out=%s", got, secondOut)
	}
}

func TestVirtualCacheLedgerForcedModeKeepsMinimumCreation(t *testing.T) {
	ledger := newVirtualCacheLedger(100)
	old := CurrentVirtualCacheConfig()
	SetVirtualCacheConfig(EffectiveVirtualCacheConfig{
		Enabled:               true,
		Mode:                  VirtualCacheModeForced,
		HitRate:               0.90,
		TargetCacheReuseRatio: 1,
	})
	t.Cleanup(func() { SetVirtualCacheConfig(old) })

	request := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"stable cache prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	upstream := []byte(`{"usage":{"input_tokens":0,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"output_tokens":1}}`)

	first := ledger.begin("claude", "claude-opus", "session-forced-min", request, time.Now())
	if first == nil {
		t.Fatal("first transaction is nil")
	}
	_ = first.RewriteClaudeResponseUsage(upstream)
	first.Commit()

	second := ledger.begin("claude", "claude-opus", "session-forced-min", request, time.Now().Add(time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage(upstream)
	second.Commit()

	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("forced input = %d, want minimum 1; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got != 1 {
		t.Fatalf("forced creation = %d, want minimum 1; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != 98 {
		t.Fatalf("forced read = %d, want remaining read 98; out=%s", got, secondOut)
	}
	if got := rewrittenUsageTotal(secondOut); got != 100 {
		t.Fatalf("forced total = %d, want anchored total 100; out=%s", got, secondOut)
	}
}

func rewrittenUsageTotal(payload []byte) int64 {
	usage := gjson.GetBytes(payload, "usage")
	return rewrittenUsageTotalFromResult(usage)
}

func rewrittenUsageTotalFromResult(usage gjson.Result) int64 {
	return usage.Get("input_tokens").Int() +
		usage.Get("cache_creation_input_tokens").Int() +
		usage.Get("cache_read_input_tokens").Int()
}
