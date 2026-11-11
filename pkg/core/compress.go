package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type RequestMeta struct {
	Protocol     string `json:"protocol,omitempty"`
	Model        string `json:"model,omitempty"`
	SourceFormat string `json:"source_format,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	Stream       bool   `json:"stream,omitempty"`
}

type Stats struct {
	Protocol             string   `json:"protocol"`
	Applied              bool     `json:"applied"`
	Model                string   `json:"model,omitempty"`
	RequestID            string   `json:"request_id,omitempty"`
	OriginalBytes        int      `json:"original_bytes"`
	CompressedBytes      int      `json:"compressed_bytes"`
	OriginalTokens       int      `json:"original_tokens"`
	CompressedTokens     int      `json:"compressed_tokens"`
	SavingsPercent       float64  `json:"savings_percent"`
	CompressionMs        float64  `json:"compression_ms"`
	Engines              []string `json:"engines"`
	Lossy                bool     `json:"lossy"`
	Warnings             []string `json:"warnings,omitempty"`
	SkippedReason        string   `json:"skipped_reason,omitempty"`
	Fallback             bool     `json:"fallback"`
	PrefixItems          int      `json:"prefix_items"`
	PrefixPreservedItems int      `json:"prefix_preserved_items"`
	FirstChangedItem     int      `json:"first_changed_item"`
	CurrentTurnStart     int      `json:"current_turn_start"`
	AlreadyCompressed    bool     `json:"already_compressed"`
}

type Result struct {
	Body    []byte `json:"body"`
	Applied bool   `json:"applied"`
	Stats   Stats  `json:"stats"`
}

type document struct {
	root       map[string]any
	protocol   string
	arrayKey   string
	items      []any
	original   []any
	currentIdx int
}

// CompressRequest is the fail-open entry point. Any parse/engine error returns
// the caller's original bytes unchanged and marks stats.fallback.
func CompressRequest(body []byte, cfg Config, meta RequestMeta) (res Result) {
	start := time.Now()
	cfg.ApplyDefaults()
	stats := Stats{
		Protocol:         detectProtocolName(meta, body),
		Model:            meta.Model,
		RequestID:        meta.RequestID,
		OriginalBytes:    len(body),
		CompressedBytes:  len(body),
		OriginalTokens:   EstimateTokensBytes(body),
		CompressedTokens: EstimateTokensBytes(body),
		FirstChangedItem: -1,
		CurrentTurnStart: -1,
	}
	res = Result{Body: body, Stats: stats}
	defer func() {
		if r := recover(); r != nil {
			res.Body = body
			res.Applied = false
			res.Stats = stats
			res.Stats.CompressedBytes = len(body)
			res.Stats.CompressedTokens = stats.OriginalTokens
			res.Stats.SavingsPercent = 0
			res.Stats.Fallback = true
			res.Stats.Warnings = append(res.Stats.Warnings, fmt.Sprintf("fail-open: %v", r))
			res.Stats.CompressionMs = float64(time.Since(start).Microseconds()) / 1000
		}
	}()

	finish := func(skipped string) Result {
		stats.SkippedReason = skipped
		stats.CompressionMs = float64(time.Since(start).Microseconds()) / 1000
		stats.Applied = false
		stats.CompressedBytes = len(body)
		stats.CompressedTokens = stats.OriginalTokens
		stats.SavingsPercent = 0
		return Result{Body: body, Applied: false, Stats: stats}
	}

	if !cfg.Enabled || cfg.Mode == ModeOff {
		return finish("disabled")
	}
	if !ModelAllowed(cfg, meta.Model) {
		return finish("model-not-allowed")
	}
	if stats.OriginalTokens < cfg.ThresholdTokens {
		return finish("below-threshold")
	}
	engines := cfg.EffectiveEngines()
	if len(engines) == 0 {
		return finish("no-engines")
	}

	doc, err := parseDocument(body, meta)
	if err != nil {
		return finish("unsupported-or-invalid-json: " + err.Error())
	}
	stats.Protocol = doc.protocol
	stats.CurrentTurnStart = doc.currentIdx
	stats.PrefixItems = minInt(cfg.PreservePrefixItems, len(doc.items))

	enginesUsed := []string{}
	changed, err := compressDocument(doc, cfg, engines, &enginesUsed, &stats)
	if err != nil {
		return finish("compression-error: " + err.Error())
	}
	if !changed {
		stats.Engines = uniqueStrings(enginesUsed)
		return finish("no-effective-change")
	}

	compressed, err := json.Marshal(doc.root)
	if err != nil {
		return finish("marshal-error: " + err.Error())
	}
	stats.CompressedBytes = len(compressed)
	stats.CompressedTokens = EstimateTokensBytes(compressed)
	stats.SavingsPercent = SavingsPercent(stats.OriginalTokens, stats.CompressedTokens)
	stats.Engines = uniqueStrings(enginesUsed)
	stats.Lossy = anyLossy(stats.Engines)
	stats.PrefixPreservedItems = firstChangedItem(doc.original, currentItems(doc))
	stats.FirstChangedItem = stats.PrefixPreservedItems
	stats.CompressionMs = float64(time.Since(start).Microseconds()) / 1000

	if stats.OriginalTokens-stats.CompressedTokens < cfg.MinSavingsTokens {
		return finish("below-min-savings")
	}
	stats.Applied = true
	return Result{Body: compressed, Applied: true, Stats: stats}
}

func parseDocument(body []byte, meta RequestMeta) (*document, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	// Reject trailing data to avoid forwarding a mutated partial JSON document.
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("trailing JSON data")
	} else if err != io.EOF {
		return nil, fmt.Errorf("trailing data: %w", err)
	}
	protocol := normalizeProtocol(meta.Protocol)
	if protocol == "" {
		protocol = normalizeProtocol(meta.SourceFormat)
	}
	if protocol == "" {
		protocol = detectProtocol(root)
	}
	var key string
	switch protocol {
	case "responses":
		key = "input"
	case "anthropic", "chat":
		key = "messages"
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
	raw, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := raw.([]any)
	if !ok {
		// Responses input can be a single string; it is pass-through only.
		return nil, fmt.Errorf("%s is not an array", key)
	}
	original := make([]any, len(items))
	for i := range items {
		original[i] = deepCopyJSON(items[i])
	}
	return &document{root: root, protocol: protocol, arrayKey: key, items: items, original: original, currentIdx: currentTurnIndex(protocol, items)}, nil
}

func detectProtocolName(meta RequestMeta, body []byte) string {
	for _, candidate := range []string{meta.Protocol, meta.SourceFormat} {
		if p := normalizeProtocol(candidate); p != "" {
			return p
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root map[string]any
	if dec.Decode(&root) == nil {
		return detectProtocol(root)
	}
	return "unknown"
}

func normalizeProtocol(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "openai-responses", "responses", "response", "codex", "codex-responses":
		return "responses"
	case "anthropic", "claude", "messages", "claude-messages":
		return "anthropic"
	case "openai", "chat", "chat-completions", "openai-chat":
		return "chat"
	default:
		return ""
	}
}

func detectProtocol(root map[string]any) string {
	if _, ok := root["input"]; ok {
		return "responses"
	}
	if _, ok := root["system"]; ok {
		if _, hasMessages := root["messages"]; hasMessages {
			return "anthropic"
		}
	}
	if _, ok := root["messages"]; ok {
		return "chat"
	}
	return "unknown"
}

func currentItems(doc *document) []any { return doc.items }

func currentTurnIndex(protocol string, items []any) int {
	for i := len(items) - 1; i >= 0; i-- {
		m, ok := items[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if protocol == "responses" {
			t, _ := m["type"].(string)
			if t == "message" && role == "user" {
				return i
			}
			continue
		}
		if role == "user" {
			return i
		}
	}
	return -1
}

func compressDocument(doc *document, cfg Config, engines []EngineConfig, used *[]string, stats *Stats) (bool, error) {
	changed := false
	if hasEngine(engines, EngineExactDedup) {
		if next, ok := dedupMessages(doc, cfg); ok {
			doc.items = next
			doc.root[doc.arrayKey] = next
			*used = append(*used, EngineExactDedup)
			changed = true
		}
	}
	effCurrent := effectiveCurrentIndex(doc, cfg)
	for i, item := range doc.items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if i < cfg.PreservePrefixItems || (effCurrent >= 0 && i >= effCurrent) {
			continue
		}
		switch doc.protocol {
		case "chat":
			if cfg.ProtectSystem {
				role, _ := m["role"].(string)
				if role == "system" || role == "developer" {
					continue
				}
			}
			role, _ := m["role"].(string)
			tool := role == "tool"
			if c, ok := compressField(m, "content", tool, cfg, engines, used); ok {
				changed = true
				_ = c
			}
		case "anthropic":
			if cfg.ProtectSystem {
				role, _ := m["role"].(string)
				if role == "system" || role == "developer" {
					continue
				}
			}
			if c, ok := compressAnthropicContent(m, cfg, engines, used); ok {
				changed = true
				_ = c
			}
		case "responses":
			typ, _ := m["type"].(string)
			role, _ := m["role"].(string)
			if cfg.ProtectSystem && (role == "system" || role == "developer") {
				continue
			}
			if strings.Contains(typ, "call_output") || strings.Contains(typ, "tool_output") {
				if c, ok := compressField(m, "output", true, cfg, engines, used); ok {
					changed = true
					_ = c
				}
			} else if typ == "message" && (role == "user" || role == "assistant") {
				if c, ok := compressField(m, "content", false, cfg, engines, used); ok {
					changed = true
					_ = c
				}
			}
		}
	}
	doc.root[doc.arrayKey] = doc.items
	return changed, nil
}

func effectiveCurrentIndex(doc *document, cfg Config) int {
	if !cfg.PreserveLastUser {
		return -1
	}
	if doc.currentIdx >= 0 {
		return doc.currentIdx
	}
	if len(doc.items) > 0 {
		return len(doc.items) - 1
	}
	return -1
}

func compressField(m map[string]any, key string, tool bool, cfg Config, engines []EngineConfig, used *[]string) (bool, bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	nv, changed := compressContentValue(v, tool, cfg, engines, used)
	if changed {
		m[key] = nv
	}
	return changed, changed
}

func compressContentValue(v any, tool bool, cfg Config, engines []EngineConfig, used *[]string) (any, bool) {
	switch x := v.(type) {
	case string:
		return applyEnginesToText(x, tool, cfg, engines, used)
	case []any:
		changed := false
		for i, part := range x {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := p["type"].(string)
			if typ == "tool_result" {
				if c, ok := compressField(p, "content", true, cfg, engines, used); ok {
					changed = true
					_ = c
				}
				continue
			}
			if typ == "text" || typ == "input_text" || typ == "output_text" {
				if c, ok := compressField(p, "text", tool, cfg, engines, used); ok {
					changed = true
					_ = c
				}
				continue
			}
			// Unknown/multimodal/tool_use/reasoning blocks are preserved byte-for-byte
			// at the JSON value level.
			x[i] = p
		}
		return x, changed
	case map[string]any:
		changed := false
		for _, key := range []string{"text", "content", "output"} {
			if _, ok := x[key]; ok {
				if c, ok := compressField(x, key, tool, cfg, engines, used); ok {
					changed = true
					_ = c
				}
			}
		}
		return x, changed
	default:
		return v, false
	}
}

func compressAnthropicContent(m map[string]any, cfg Config, engines []EngineConfig, used *[]string) (bool, bool) {
	content, ok := m["content"]
	if !ok {
		return false, false
	}
	if s, ok := content.(string); ok {
		nv, changed := applyEnginesToText(s, false, cfg, engines, used)
		if changed {
			m["content"] = nv
		}
		return changed, changed
	}
	blocks, ok := content.([]any)
	if !ok {
		return false, false
	}
	changed := false
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := block["type"].(string)
		switch typ {
		case "tool_result":
			if c, ok := compressField(block, "content", true, cfg, engines, used); ok {
				changed = true
				_ = c
			}
		case "text":
			if c, ok := compressField(block, "text", false, cfg, engines, used); ok {
				changed = true
				_ = c
			}
		default:
			// tool_use, image, document, thinking, and unknown blocks stay intact.
		}
	}
	if changed {
		m["content"] = blocks
	}
	return changed, changed
}

func applyEnginesToText(s string, tool bool, cfg Config, engines []EngineConfig, used *[]string) (string, bool) {
	cur := s
	changed := false
	for _, e := range engines {
		if !e.Enabled {
			continue
		}
		if !tool && IsLossyEngine(e.ID) {
			continue
		}
		switch e.ID {
		case EngineWhitespace:
			n := normalizeWhitespaceV1(cur)
			if n != cur {
				cur = n
				changed = true
				*used = append(*used, e.ID)
			}
		case EngineJSONMinify:
			if !tool || len(cur) < 64 {
				continue
			}
			n, ok := minifyJSONText(cur)
			if ok && n != cur {
				cur = n
				changed = true
				*used = append(*used, e.ID)
			}
		case EngineDiagnosticTrim:
			if !tool || !cfg.AllowLossy {
				continue
			}
			n, ok := diagnosticTrim(cur, cfg.DiagnosticTrim)
			if ok && n != cur {
				cur = n
				changed = true
				*used = append(*used, e.ID)
			}
		case EngineToolTruncate:
			if !tool || !cfg.AllowLossy {
				continue
			}
			n, ok := truncateToolText(cur, cfg.ToolTruncate)
			if ok && n != cur {
				cur = n
				changed = true
				*used = append(*used, e.ID)
			}
		}
	}
	return cur, changed
}

func dedupMessages(doc *document, cfg Config) ([]any, bool) {
	effCurrent := effectiveCurrentIndex(doc, cfg)
	out := make([]any, 0, len(doc.items))
	changed := false
	for i, item := range doc.items {
		if i < cfg.PreservePrefixItems || (effCurrent >= 0 && i >= effCurrent) || len(out) == 0 {
			out = append(out, item)
			continue
		}
		prev, ok1 := out[len(out)-1].(map[string]any)
		cur, ok2 := item.(map[string]any)
		if !ok1 || !ok2 {
			out = append(out, item)
			continue
		}
		prevRole, _ := prev["role"].(string)
		curRole, _ := cur["role"].(string)
		if prevRole == "" || prevRole != curRole || prevRole == "system" || prevRole == "developer" || prevRole == "tool" {
			out = append(out, item)
			continue
		}
		if prev["tool_calls"] != nil || cur["tool_calls"] != nil || prev["tool_call_id"] != nil || cur["tool_call_id"] != nil {
			out = append(out, item)
			continue
		}
		if stableJSONHash(prev) == stableJSONHash(cur) {
			changed = true
			continue
		}
		out = append(out, item)
	}
	return out, changed
}

func firstChangedItem(before, after []any) int {
	n := len(before)
	if len(after) < n {
		n = len(after)
	}
	for i := 0; i < n; i++ {
		if stableJSONHash(before[i]) != stableJSONHash(after[i]) {
			return i
		}
	}
	if len(before) != len(after) {
		return n
	}
	return len(after)
}

func deepCopyJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = deepCopyJSON(val)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, val := range x {
			a[i] = deepCopyJSON(val)
		}
		return a
	default:
		return x
	}
}

func hasEngine(engines []EngineConfig, id string) bool {
	for _, e := range engines {
		if e.ID == id && e.Enabled {
			return true
		}
	}
	return false
}

func anyLossy(engines []string) bool {
	for _, e := range engines {
		if IsLossyEngine(e) {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
