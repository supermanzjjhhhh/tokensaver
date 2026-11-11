package core

import "testing"

func TestStatsStoreBoundedAndAggregates(t *testing.T) {
	store := NewStatsStore(2)
	store.Record(Stats{Applied: true, OriginalTokens: 100, CompressedTokens: 50, CompressionMs: 2})
	store.Record(Stats{Fallback: true, OriginalTokens: 80, CompressedTokens: 80, CompressionMs: 4})
	store.Record(Stats{Applied: true, OriginalTokens: 20, CompressedTokens: 10, CompressionMs: 6})

	snapshot := store.Snapshot()
	if got := snapshot["count"]; got != uint64(3) {
		t.Fatalf("count=%v", got)
	}
	if got := snapshot["applied"]; got != uint64(2) {
		t.Fatalf("applied=%v", got)
	}
	if got := snapshot["fallback"]; got != uint64(1) {
		t.Fatalf("fallback=%v", got)
	}
	if got := snapshot["original_tokens"]; got != uint64(200) {
		t.Fatalf("original_tokens=%v", got)
	}
	if got := snapshot["compressed_tokens"]; got != uint64(140) {
		t.Fatalf("compressed_tokens=%v", got)
	}
	if got := snapshot["savings_percent"]; got != 30.0 {
		t.Fatalf("savings_percent=%v", got)
	}
	if got := snapshot["average_compression_ms"]; got != 4.0 {
		t.Fatalf("average_compression_ms=%v", got)
	}
	entries, ok := snapshot["entries"].([]Stats)
	if !ok || len(entries) != 2 {
		t.Fatalf("entries=%#v", snapshot["entries"])
	}
	if entries[0].OriginalTokens != 80 || entries[1].OriginalTokens != 20 {
		t.Fatalf("unexpected bounded entries: %+v", entries)
	}
}

func TestStatsStoreLoggingPolicy(t *testing.T) {
	store := NewStatsStore(10)
	store.Configure(LogConfig{Enabled: true, MaxEntries: 1, IncludeModel: false, IncludeEngine: false})
	store.Record(Stats{Applied: true, Model: "first", Engines: []string{"whitespace"}})
	store.Record(Stats{Applied: true, Model: "second", Engines: []string{"json-minify"}})

	snapshot := store.Snapshot()
	entries := snapshot["entries"].([]Stats)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
	if entries[0].Model != "" || entries[0].Engines != nil {
		t.Fatalf("logging include flags not applied: %+v", entries[0])
	}

	store.Configure(LogConfig{Enabled: false, MaxEntries: 1, IncludeModel: true, IncludeEngine: true})
	if got := store.Snapshot()["entries"].([]Stats); len(got) != 0 {
		t.Fatalf("disabled logging retained entries: %+v", got)
	}

	store.Configure(LogConfig{Enabled: true, MaxEntries: 2, IncludeModel: true, IncludeEngine: true})
	store.Record(Stats{Applied: true, Model: "third", Engines: []string{"exact-dedup"}})
	entries = store.Snapshot()["entries"].([]Stats)
	if len(entries) != 1 || entries[0].Model != "third" || len(entries[0].Engines) != 1 {
		t.Fatalf("re-enabled logging mismatch: %+v", entries)
	}
}
