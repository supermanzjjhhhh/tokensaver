package core

import (
	"sync"
	"time"
)

// StatsStore is an in-memory, bounded store for compression summaries. It never
// stores request bodies, headers, credentials, or model output.
type StatsStore struct {
	mu        sync.RWMutex
	startedAt time.Time
	max       int
	logging   LogConfig
	entries   []Stats
	count     uint64
	applied   uint64
	fallback  uint64
	origTok   uint64
	compTok   uint64
	totalMS   float64
}

func NewStatsStore(max int) *StatsStore {
	if max < 1 {
		max = 100
	}
	return &StatsStore{
		startedAt: time.Now(),
		max:       max,
		logging: LogConfig{
			Enabled:       true,
			MaxEntries:    max,
			IncludeModel:  true,
			IncludeEngine: true,
		},
	}
}

// Configure applies the Core logging policy to the bounded recent-entry store.
// Aggregates remain available even when recent-entry logging is disabled.
func (s *StatsStore) Configure(cfg LogConfig) {
	if cfg.MaxEntries < 1 {
		cfg.MaxEntries = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logging = cfg
	s.max = cfg.MaxEntries
	if !cfg.Enabled {
		s.entries = nil
		return
	}
	if len(s.entries) > s.max {
		s.entries = append([]Stats(nil), s.entries[len(s.entries)-s.max:]...)
	}
}

func (s *StatsStore) Record(stats Stats) {
	if stats.Fallback && stats.SkippedReason == "" {
		stats.SkippedReason = "fallback"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	if stats.Applied {
		s.applied++
	}
	if stats.Fallback {
		s.fallback++
	}
	s.origTok += uint64(maxInt(stats.OriginalTokens, 0))
	s.compTok += uint64(maxInt(stats.CompressedTokens, 0))
	s.totalMS += stats.CompressionMs
	if s.logging.Enabled {
		entry := stats
		if !s.logging.IncludeModel {
			entry.Model = ""
		}
		if !s.logging.IncludeEngine {
			entry.Engines = nil
		}
		if len(s.entries) == s.max {
			copy(s.entries, s.entries[1:])
			s.entries[len(s.entries)-1] = entry
		} else {
			s.entries = append(s.entries, entry)
		}
	}
}

func (s *StatsStore) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	savings := 0.0
	if s.origTok > 0 {
		savings = float64(s.origTok-s.compTok) * 100 / float64(s.origTok)
	}
	avg := 0.0
	if s.count > 0 {
		avg = s.totalMS / float64(s.count)
	}
	return map[string]any{
		"started_at":             s.startedAt,
		"count":                  s.count,
		"applied":                s.applied,
		"fallback":               s.fallback,
		"original_tokens":        s.origTok,
		"compressed_tokens":      s.compTok,
		"savings_percent":        savings,
		"average_compression_ms": avg,
		"entries":                append([]Stats(nil), s.entries...),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
