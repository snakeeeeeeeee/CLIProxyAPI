package claudeapipool

import (
	"math"
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
	wantInput := first.deltaTokens
	wantCreation := first.localCacheableBudgetTokens()
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != wantInput {
		t.Fatalf("first input_tokens = %d, want local delta %d; out=%s", got, wantInput, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != wantCreation {
		t.Fatalf("first cache_creation_input_tokens = %d, want local budget %d", got, wantCreation)
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
		t.Fatalf("second input_tokens = %d, want local delta %d", got, wantInput)
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
	wantCreation := first.localCacheableBudgetTokens()
	if got := firstPayload.Get("usage.cache_creation_input_tokens").Int(); got != wantCreation {
		t.Fatalf("first stream cache_creation_input_tokens = %d, want %d; out=%s", got, wantCreation, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-1", request, time.Now())
	out := second.RewriteClaudeStreamLine(line)
	second.Commit()
	payload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(out), "data:")))
	if got := payload.Get("usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("stream input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, out)
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
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != second.localCacheableBudgetTokens() {
		t.Fatalf("expired entry should rebuild creation from local budget: %s", out)
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
	if sum := rewrittenUsageTotal(out); sum != first.localTotalInputTokens() {
		t.Fatalf("first rewritten total = %d, want local total %d; out=%s", sum, first.localTotalInputTokens(), out)
	}

	second := ledger.begin("claude", "claude-opus", "session-1", request, time.Now())
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	out = second.RewriteClaudeResponseUsage(upstream)
	second.Commit()
	wantRead := minInt64(first.localCacheableBudgetTokens(), int64(math.Round(float64(second.localCacheableBudgetTokens())*0.90)))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second cache_read_input_tokens = %d, want local target read %d; out=%s", got, wantRead, out)
	}
	if got := gjson.GetBytes(out, "usage.cache_creation_input_tokens").Int(); got != second.localCacheableBudgetTokens()-wantRead {
		t.Fatalf("second cache_creation_input_tokens = %d, want remaining local budget; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, out)
	}
	if sum := rewrittenUsageTotal(out); sum != second.localTotalInputTokens() {
		t.Fatalf("second rewritten total = %d, want local total %d; out=%s", sum, second.localTotalInputTokens(), out)
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
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, out)
	}

	second := ledger.begin("claude", "claude-haiku", "session-1", request, time.Now())
	out = second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":80,"output_tokens":1}}`))
	if got := gjson.GetBytes(out, "usage.cache_read_input_tokens").Int(); got != 100 {
		t.Fatalf("cache_read_input_tokens = %d, want local capped budget 100; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, out)
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
	wantRead := minInt64(created, int64(math.Round(float64(second.localCacheableBudgetTokens())*0.90)))
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second cache_read_input_tokens = %d, want local target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("second cache_creation_input_tokens = %d, want rolling delta creation; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != second.localTotalInputTokens() {
		t.Fatalf("second rewritten total = %d, want local total %d; out=%s", sum, second.localTotalInputTokens(), secondOut)
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
	if firstCreated != first.localCacheableBudgetTokens() {
		t.Fatalf("first creation = %d, want local budget %d; out=%s", firstCreated, first.localCacheableBudgetTokens(), firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-target", request, now.Add(4*time.Second))
	if second == nil {
		t.Fatal("second transaction is nil")
	}
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2000,"output_tokens":1}}`))
	wantRead := minInt64(first.localCacheableBudgetTokens(), int64(math.Round(float64(second.localCacheableBudgetTokens())*0.90)))
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("cache_read_input_tokens = %d, want local target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
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
	firstBudget := first.localCacheableBudgetTokens()
	if created := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); created != firstBudget {
		t.Fatalf("first creation = %d, want local budget %d; out=%s", created, firstBudget, firstOut)
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
	wantRead := int64(math.Round(float64(second.localCacheableBudgetTokens()) * 0.95))
	if read != wantRead {
		t.Fatalf("cache_read_input_tokens = %d, want local target read %d; out=%s", read, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != second.localTotalInputTokens() {
		t.Fatalf("second rewritten total = %d, want local total %d; out=%s", sum, second.localTotalInputTokens(), secondOut)
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
	if got := gjson.GetBytes(firstOut, "usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("first input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, firstOut)
	}
	if got := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); got != first.localCacheableBudgetTokens() {
		t.Fatalf("first creation = %d, want local budget %d; out=%s", got, first.localCacheableBudgetTokens(), firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-preserve-input", request, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":900,"cache_read_input_tokens":24000,"output_tokens":1}}`))
	second.Commit()
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	wantRead := int64(math.Round(float64(second.localCacheableBudgetTokens()) * 0.92))
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second read = %d, want local target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got != second.localCacheableBudgetTokens()-wantRead {
		t.Fatalf("second creation = %d, want remaining local budget; out=%s", got, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != second.localTotalInputTokens() {
		t.Fatalf("second total = %d, want local total %d; out=%s", sum, second.localTotalInputTokens(), secondOut)
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
	firstBudget := first.localCacheableBudgetTokens()
	if got := firstPayload.Get("usage.cache_creation_input_tokens").Int(); got != firstBudget {
		t.Fatalf("first stream cache_creation_input_tokens = %d, want local budget %d; out=%s", got, firstBudget, firstOut)
	}
	if got := firstPayload.Get("usage.input_tokens").Int(); got != first.deltaTokens {
		t.Fatalf("first stream input_tokens = %d, want local delta %d; out=%s", got, first.deltaTokens, firstOut)
	}

	second := ledger.begin("claude", "claude-sonnet", "session-stream-target", request, time.Now())
	secondOut := second.RewriteClaudeStreamLine(line)
	second.Commit()
	secondPayload := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(string(secondOut), "data:")))
	wantRead := int64(math.Round(float64(second.localCacheableBudgetTokens()) * 0.90))
	if got := secondPayload.Get("usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second stream cache_read_input_tokens = %d, want local target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := secondPayload.Get("usage.cache_creation_input_tokens").Int(); got != second.localCacheableBudgetTokens()-wantRead {
		t.Fatalf("second stream cache_creation_input_tokens = %d, want remaining local budget; out=%s", got, secondOut)
	}
	if got := secondPayload.Get("usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second stream input_tokens = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
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
	if got := gjson.GetBytes(firstOut, "usage.cache_creation_input_tokens").Int(); got != first.localCacheableBudgetTokens() {
		t.Fatalf("first creation = %d, want local budget %d; out=%s", got, first.localCacheableBudgetTokens(), firstOut)
	}

	second := ledger.begin("claude", "claude-opus", "session-auto-delta", secondRequest, time.Now())
	secondOut := second.RewriteClaudeResponseUsage([]byte(`{"usage":{"input_tokens":2300,"output_tokens":1}}`))
	second.Commit()
	wantRead := minInt64(first.localCacheableBudgetTokens(), int64(math.Round(float64(second.localCacheableBudgetTokens())*0.90)))
	if got := gjson.GetBytes(secondOut, "usage.cache_read_input_tokens").Int(); got != wantRead {
		t.Fatalf("second read = %d, want local target read %d; out=%s", got, wantRead, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.cache_creation_input_tokens").Int(); got <= 0 {
		t.Fatalf("second creation = %d, want auto delta creation; out=%s", got, secondOut)
	}
	if got := gjson.GetBytes(secondOut, "usage.input_tokens").Int(); got != second.deltaTokens {
		t.Fatalf("second input = %d, want local delta %d; out=%s", got, second.deltaTokens, secondOut)
	}
	if sum := rewrittenUsageTotal(secondOut); sum != second.localTotalInputTokens() {
		t.Fatalf("second total = %d, want local total %d; out=%s", sum, second.localTotalInputTokens(), secondOut)
	}
}

func rewrittenUsageTotal(payload []byte) int64 {
	usage := gjson.GetBytes(payload, "usage")
	return usage.Get("input_tokens").Int() +
		usage.Get("cache_creation_input_tokens").Int() +
		usage.Get("cache_read_input_tokens").Int()
}
