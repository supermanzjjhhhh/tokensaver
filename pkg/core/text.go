package core

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	progressRE   = regexp.MustCompile(`(?m)\r[^\n]*$`)
	errorLineRE  = regexp.MustCompile(`(?i)(error|fail(ed|ure)?|warn(ing)?|exception|panic|assert|traceback|fatal|denied|timeout)`)
	collapseNLRE = regexp.MustCompile(`\n{3,}`)
)

const (
	truncateMarker   = "[... tokensaver tool truncate:"
	diagnosticMarker = "[... tokensaver diagnostic trim:"
)

// normalizeWhitespaceV1 is deliberately conservative: trailing spaces/tabs
// are removed outside fenced code, runs of three or more newlines collapse to
// one blank line, and content inside fenced code is copied byte-for-byte.
func normalizeWhitespaceV1(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	pendingBlank := false
	inFence := false
	fenceChar := byte(0)
	for _, line := range lines {
		trimmedLeft := strings.TrimLeft(line, " \t")
		isFence := strings.HasPrefix(trimmedLeft, "```") || strings.HasPrefix(trimmedLeft, "~~~")
		if inFence {
			out = append(out, line)
			if isFence && len(trimmedLeft) > 0 && trimmedLeft[0] == fenceChar {
				inFence = false
			}
			continue
		}
		if isFence {
			if pendingBlank && len(out) > 0 {
				out = append(out, "")
			}
			pendingBlank = false
			out = append(out, line)
			inFence = true
			fenceChar = trimmedLeft[0]
			continue
		}
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if len(out) > 0 {
				pendingBlank = true
			}
			continue
		}
		if pendingBlank && len(out) > 0 {
			out = append(out, "")
		}
		pendingBlank = false
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func minifyJSONText(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 {
		return s, false
	}
	if !((trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}') || (trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']')) {
		return s, false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err != nil {
		return s, false
	}
	return buf.String(), true
}

func diagnosticTrim(s string, cfg DiagnosticTrimConfig) (string, bool) {
	if len(s) < cfg.MinChars || strings.Contains(s, diagnosticMarker) {
		return s, false
	}
	clean := ansiRE.ReplaceAllString(s, "")
	clean = progressRE.ReplaceAllString(clean, "")
	lines := strings.Split(clean, "\n")
	if len(lines) <= cfg.MaxLines {
		return clean, clean != s
	}
	keep := make([]bool, len(lines))
	head := cfg.MaxLines / 4
	tail := cfg.MaxLines / 3
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	for i := 0; i < head && i < len(lines); i++ {
		keep[i] = true
	}
	for i := len(lines) - tail; i < len(lines); i++ {
		if i >= 0 {
			keep[i] = true
		}
	}
	if cfg.PreserveErrorLines {
		for i, line := range lines {
			if errorLineRE.MatchString(line) {
				keep[i] = true
			}
		}
	}
	var out []string
	omitted := 0
	lastBlank := false
	for i, line := range lines {
		if keep[i] {
			if omitted > 0 {
				out = append(out, diagnosticMarker+" "+itoa(omitted)+" lines omitted ...]")
				omitted = 0
			}
			// Collapse repeated adjacent non-empty lines.
			if len(out) > 0 && line != "" && out[len(out)-1] == line {
				continue
			}
			if line == "" && lastBlank {
				continue
			}
			out = append(out, line)
			lastBlank = line == ""
		} else {
			omitted++
		}
	}
	if omitted > 0 {
		out = append(out, diagnosticMarker+" "+itoa(omitted)+" lines omitted ...]")
	}
	res := strings.Join(out, "\n")
	if len(out) > cfg.MaxLines+8 {
		res = strings.Join(out[:cfg.MaxLines-1], "\n") + "\n" + diagnosticMarker + " output capped ...]"
	}
	return res, res != s
}

func truncateToolText(s string, cfg ToolTruncateConfig) (string, bool) {
	if len([]rune(s)) <= cfg.MaxChars || len(s) < cfg.MinChars || strings.Contains(s, truncateMarker) {
		return s, false
	}
	runes := []rune(s)
	headN := int(float64(cfg.MaxChars) * cfg.HeadPct)
	tailN := int(float64(cfg.MaxChars) * cfg.TailPct)
	if headN < 1 || tailN < 1 || headN+tailN >= len(runes) {
		return s, false
	}
	elided := len(runes) - headN - tailN
	head := string(runes[:headN])
	tail := string(runes[len(runes)-tailN:])
	// Back off to whitespace when close to the cut, avoiding half words.
	if cut := strings.LastIndexAny(head, " \t\n"); cut > len(head)/2 {
		head = head[:cut]
	}
	if cut := strings.IndexAny(tail, " \t\n"); cut >= 0 && cut < len(tail)/2 {
		tail = tail[cut+1:]
	}
	return head + "\n" + truncateMarker + " " + itoa(elided) + " chars omitted ...]\n" + tail, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func validUTF8String(b []byte) bool { return utf8.Valid(b) }
