package core

import (
	"encoding/json"
	"testing"
)

type multiTurnCacheResult struct {
	Turn             int  `json:"turn"`
	OriginalTokens   int  `json:"original_tokens"`
	CompressedTokens int  `json:"compressed_tokens"`
	Applied          bool `json:"applied"`
	FirstChangedItem int  `json:"first_changed_item"`
	PrefixStable     bool `json:"prefix_stable"`
}

func buildMultiTurnBody(turns int) []byte {
	items := []any{
		map[string]any{"role": "system", "content": "system instructions stay stable"},
	}
	for i := 0; i < turns; i++ {
		items = append(items,
			map[string]any{"role": "assistant", "content": "assistant response " + itoa(i) + "  \n\n\n text"},
			map[string]any{"role": "user", "content": "user request " + itoa(i) + "  \n\n\n text"},
		)
	}
	body, _ := json.Marshal(map[string]any{"model": "gpt-test", "messages": items})
	return body
}

func analyzeMultiTurnCache(t *testing.T, cfg Config, turns int) []multiTurnCacheResult {
	t.Helper()
	var previous []any
	results := make([]multiTurnCacheResult, 0, turns)
	for turn := 0; turn < turns; turn++ {
		body := buildMultiTurnBody(turn + 1)
		res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "gpt-test"})
		if res.Stats.Fallback {
			t.Fatalf("turn %d fallback: %+v", turn, res.Stats)
		}
		var root map[string]any
		if err := json.Unmarshal(res.Body, &root); err != nil {
			t.Fatalf("turn %d invalid JSON: %v", turn, err)
		}
		current := root["messages"].([]any)
		stable := true
		if len(previous) > len(current) {
			stable = false
		} else {
			for i := range previous {
				if stableJSONHash(previous[i]) != stableJSONHash(current[i]) {
					stable = false
					break
				}
			}
		}
		results = append(results, multiTurnCacheResult{
			Turn:             turn + 1,
			OriginalTokens:   res.Stats.OriginalTokens,
			CompressedTokens: res.Stats.CompressedTokens,
			Applied:          res.Applied,
			FirstChangedItem: res.Stats.FirstChangedItem,
			PrefixStable:     stable,
		})
		previous = current
	}
	return results
}

func TestMultiTurnPrefixStability(t *testing.T) {
	defaultCfg := testConfig()
	defaultCfg.PreservePrefixItems = 1
	defaultCfg.PreserveLastUser = true
	defaultCfg.ProtectSystem = true
	defaultResults := analyzeMultiTurnCache(t, defaultCfg, 6)

	highThresholdCfg := defaultCfg
	highThresholdCfg.ThresholdTokens = 1_000_000
	highThresholdResults := analyzeMultiTurnCache(t, highThresholdCfg, 6)

	defaultUnstable := 0
	for _, item := range defaultResults {
		if !item.PrefixStable {
			defaultUnstable++
		}
	}
	highUnstable := 0
	for _, item := range highThresholdResults {
		if !item.PrefixStable {
			highUnstable++
		}
	}
	if defaultUnstable == 0 {
		t.Fatalf("expected default multi-turn compression to change a prefix: %+v", defaultResults)
	}
	if highUnstable != 0 {
		t.Fatalf("high threshold unexpectedly changed a prefix: %+v", highThresholdResults)
	}

	output := map[string]any{
		"default_safe": map[string]any{
			"unstable_turns": defaultUnstable,
			"turns":          defaultResults,
		},
		"high_threshold": map[string]any{
			"threshold_tokens": highThresholdCfg.ThresholdTokens,
			"unstable_turns":   highUnstable,
			"turns":            highThresholdResults,
		},
	}
	raw, _ := json.MarshalIndent(output, "", "  ")
	t.Logf("multi-turn cache analysis:\n%s", raw)
}
