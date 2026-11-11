package gateway

import (
	"sort"
	"sync"
	"time"
)

type RequestRecord struct {
	Timestamp          time.Time `json:"timestamp"`
	Method             string    `json:"method"`
	Path               string    `json:"path"`
	Protocol           string    `json:"protocol,omitempty"`
	Model              string    `json:"model,omitempty"`
	RequestID          string    `json:"request_id,omitempty"`
	Status             int       `json:"status"`
	CompressionApplied bool      `json:"compression_applied"`
	Fallback           bool      `json:"fallback"`
	OriginalTokens     int       `json:"original_tokens,omitempty"`
	CompressedTokens   int       `json:"compressed_tokens,omitempty"`
	SavingsPercent     float64   `json:"savings_percent,omitempty"`
	CompressionMs      float64   `json:"compression_ms,omitempty"`
	TotalMs            float64   `json:"total_ms"`
	UpstreamMs         float64   `json:"upstream_ms,omitempty"`
	BytesIn            int       `json:"bytes_in,omitempty"`
	BytesOut           int64     `json:"bytes_out,omitempty"`
	Engines            []string  `json:"engines,omitempty"`
	Error              string    `json:"error,omitempty"`
}

type Snapshot struct {
	StartedAt          time.Time       `json:"started_at"`
	Requests           uint64          `json:"requests"`
	CompressedRequests uint64          `json:"compressed_requests"`
	FallbackRequests   uint64          `json:"fallback_requests"`
	ErrorRequests      uint64          `json:"error_requests"`
	InFlight           int64           `json:"in_flight"`
	OriginalTokens     uint64          `json:"original_tokens"`
	CompressedTokens   uint64          `json:"compressed_tokens"`
	SavingsPercent     float64         `json:"savings_percent"`
	P50CompressionMs   float64         `json:"p50_compression_ms"`
	P95CompressionMs   float64         `json:"p95_compression_ms"`
	P50TotalMs         float64         `json:"p50_total_ms"`
	P95TotalMs         float64         `json:"p95_total_ms"`
	Recent             []RequestRecord `json:"recent"`
}

type StatsStore struct {
	mu         sync.Mutex
	startedAt  time.Time
	maxRecent  int
	recent     []RequestRecord
	requests   uint64
	compressed uint64
	fallbacks  uint64
	errors     uint64
	origTokens uint64
	compTokens uint64
	inFlight   int64
}

func NewStatsStore(maxRecent int) *StatsStore {
	if maxRecent < 1 {
		maxRecent = 100
	}
	return &StatsStore{startedAt: time.Now(), maxRecent: maxRecent}
}

func (s *StatsStore) Enter() {
	s.mu.Lock()
	s.inFlight++
	s.mu.Unlock()
}

func (s *StatsStore) Leave() {
	s.mu.Lock()
	if s.inFlight > 0 {
		s.inFlight--
	}
	s.mu.Unlock()
}

func (s *StatsStore) Record(r RequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if r.CompressionApplied {
		s.compressed++
	}
	if r.Fallback {
		s.fallbacks++
	}
	if r.Status >= 500 || r.Error != "" {
		s.errors++
	}
	s.origTokens += uint64(maxInt(r.OriginalTokens, 0))
	s.compTokens += uint64(maxInt(r.CompressedTokens, 0))
	if len(s.recent) == s.maxRecent {
		copy(s.recent, s.recent[1:])
		s.recent[len(s.recent)-1] = r
	} else {
		s.recent = append(s.recent, r)
	}
}

func (s *StatsStore) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Snapshot{
		StartedAt:          s.startedAt,
		Requests:           s.requests,
		CompressedRequests: s.compressed,
		FallbackRequests:   s.fallbacks,
		ErrorRequests:      s.errors,
		InFlight:           s.inFlight,
		OriginalTokens:     s.origTokens,
		CompressedTokens:   s.compTokens,
		Recent:             append([]RequestRecord(nil), s.recent...),
	}
	if s.origTokens > 0 {
		out.SavingsPercent = float64(s.origTokens-s.compTokens) * 100 / float64(s.origTokens)
	}
	var comp, total []float64
	for _, r := range s.recent {
		if r.CompressionMs > 0 {
			comp = append(comp, r.CompressionMs)
		}
		total = append(total, r.TotalMs)
	}
	out.P50CompressionMs = percentile(comp, 0.50)
	out.P95CompressionMs = percentile(comp, 0.95)
	out.P50TotalMs = percentile(total, 0.50)
	out.P95TotalMs = percentile(total, 0.95)
	return out
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	idx := int(float64(len(cp)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
