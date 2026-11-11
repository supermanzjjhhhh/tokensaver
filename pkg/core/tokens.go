package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// EstimateTokens is a deterministic, dependency-free heuristic. It intentionally
// over-weights non-ASCII text so CJK prompts are not treated as ASCII bytes.
func EstimateTokensBytes(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for len(body) > 0 {
		r, size := utf8.DecodeRune(body)
		if r == utf8.RuneError && size == 1 {
			ascii++
			body = body[1:]
			continue
		}
		if r < 0x80 {
			ascii++
		} else {
			nonASCII++
		}
		body = body[size:]
	}
	// ASCII prose/code averages roughly 4 bytes per token. Non-ASCII is
	// conservatively counted as one token per rune.
	t := (ascii + 3) / 4
	return t + nonASCII
}

func EstimateTokensString(s string) int {
	return EstimateTokensBytes([]byte(s))
}

func SavingsPercent(original, compressed int) float64 {
	if original <= 0 || compressed >= original {
		return 0
	}
	return float64(original-compressed) * 100 / float64(original)
}

var wildcardCache sync.Map

func matchModel(pattern, model string) bool {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	model = strings.TrimSpace(strings.ToLower(model))
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if v, ok := wildcardCache.Load(pattern); ok {
		return v.(*regexp.Regexp).MatchString(model)
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return pattern == model
	}
	wildcardCache.Store(pattern, re)
	return re.MatchString(model)
}

func ModelAllowed(cfg Config, model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	allow := false
	for _, p := range cfg.ModelAllow {
		if matchModel(p, model) {
			allow = true
			break
		}
	}
	if !allow {
		return false
	}
	for _, p := range cfg.ModelDeny {
		if matchModel(p, model) {
			return false
		}
	}
	return true
}

func stableJSONHash(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
