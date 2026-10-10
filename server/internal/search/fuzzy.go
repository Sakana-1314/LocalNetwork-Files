package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Fuzzy matching in the spirit of fzf's v1 algorithm: a pattern matches when
// its characters appear in order (subsequence). The shortest matching window
// is located and scored; contiguous runs and matches at word boundaries
// (start, after a separator, camelCase humps, CJK/latin transitions) score
// higher, gaps are penalized and exact substrings/prefixes get a large bonus.

const (
	scoreMatch       = 16
	bonusBoundary    = 10
	bonusCamel       = 8
	bonusConsecutive = 6
	bonusFirstChar   = 12
	penaltyGapStart  = 3
	penaltyGapExt    = 1
	bonusSubstring   = 40
	bonusPrefix      = 30
	bonusExact       = 60
)

type pattern struct {
	text  string // lower-cased
	runes []rune
	path  bool // matches against the relative path instead of the name
}

func parseQuery(q string) []pattern {
	var out []pattern
	for _, f := range strings.Fields(lowerRunes(q)) {
		out = append(out, pattern{text: f, runes: []rune(f), path: strings.Contains(f, "/")})
	}
	return out
}

// quickReject cheaply checks that p is a subsequence of lower without allocating.
func quickReject(lower string, p []rune) bool {
	i := 0
	for _, r := range p {
		j := strings.IndexRune(lower[i:], r)
		if j < 0 {
			return true
		}
		i += j + utf8.RuneLen(r)
	}
	return false
}

type charClass int

const (
	clsOther charClass = iota
	clsLower
	clsUpper
	clsDigit
	clsCJK
	clsSep
)

func classOf(r rune) charClass {
	switch {
	case r >= 'a' && r <= 'z':
		return clsLower
	case r >= 'A' && r <= 'Z':
		return clsUpper
	case r >= '0' && r <= '9':
		return clsDigit
	case strings.ContainsRune(" -_./\\()[]{}~·、，。【】（）《》", r):
		return clsSep
	case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
		return clsCJK
	case unicode.IsUpper(r):
		return clsUpper
	case unicode.IsLower(r):
		return clsLower
	}
	return clsOther
}

func boundaryBonus(prev, cur charClass) int {
	switch {
	case prev == clsSep:
		return bonusBoundary
	case prev == clsLower && cur == clsUpper:
		return bonusCamel
	case prev != cur && (prev == clsCJK || cur == clsCJK):
		return bonusCamel
	case prev != clsDigit && cur == clsDigit:
		return bonusCamel / 2
	}
	return 0
}

// matchOne scores a single pattern against text. orig is the original-case
// text (for camelCase detection), lower its lower-cased form. Positions are
// rune indices.
func matchOne(orig, lower []rune, p []rune, wantPos bool) (int, []int, bool) {
	n, m := len(lower), len(p)
	if m == 0 {
		return 0, nil, true
	}
	if m > n {
		return 0, nil, false
	}
	// Exact substring: score directly.
	if idx := indexRunes(lower, p); idx >= 0 {
		score := m*scoreMatch + (m-1)*bonusConsecutive + bonusSubstring
		prev := clsSep
		if idx > 0 {
			prev = classOf(orig[idx-1])
		}
		score += boundaryBonus(prev, classOf(orig[idx])) * 2
		if idx == 0 {
			score += bonusPrefix + bonusFirstChar
			if m == n {
				score += bonusExact
			}
		}
		var pos []int
		if wantPos {
			pos = make([]int, m)
			for i := range pos {
				pos[i] = idx + i
			}
		}
		return score, pos, true
	}
	// Forward pass: first end position of a greedy subsequence match.
	pi, end := 0, -1
	for i := 0; i < n; i++ {
		if lower[i] == p[pi] {
			pi++
			if pi == m {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	// Backward pass: shortest window ending at end.
	pi, start := m-1, end
	for i := end; i >= 0; i-- {
		if lower[i] == p[pi] {
			pi--
			if pi < 0 {
				start = i
				break
			}
		}
	}
	// Score the window, preferring boundary characters when choosing positions.
	score := 0
	var pos []int
	if wantPos {
		pos = make([]int, 0, m)
	}
	pi = 0
	lastMatch := -1
	for i := start; i <= end && pi < m; i++ {
		if lower[i] != p[pi] {
			continue
		}
		prev := clsSep
		if i > 0 {
			prev = classOf(orig[i-1])
		}
		s := scoreMatch + boundaryBonus(prev, classOf(orig[i]))
		if i == 0 {
			s += bonusFirstChar
		}
		if lastMatch >= 0 {
			if gap := i - lastMatch - 1; gap == 0 {
				s += bonusConsecutive
			} else {
				s -= penaltyGapStart + (gap-1)*penaltyGapExt
			}
		}
		score += s
		lastMatch = i
		if wantPos {
			pos = append(pos, i)
		}
		pi++
	}
	return score, pos, pi == m
}

func indexRunes(s, sub []rune) int {
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := range sub {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
