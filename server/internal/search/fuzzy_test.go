package search

import (
	"reflect"
	"sort"
	"testing"
)

func score1(name, q string) (int, []int, bool) {
	p := []rune(lowerRunes(q))
	return matchOne([]rune(name), []rune(lowerRunes(name)), p, true)
}

func mustScore(t *testing.T, name, q string) int {
	t.Helper()
	s, _, ok := score1(name, q)
	if !ok {
		t.Fatalf("%q should match %q", q, name)
	}
	return s
}

func TestFuzzyNoMatch(t *testing.T) {
	for _, c := range [][2]string{{"report.pdf", "xyz"}, {"abc", "abcd"}, {"abc", "cba"}, {"报告", "告报"}} {
		if _, _, ok := score1(c[0], c[1]); ok {
			t.Errorf("%q should not match %q", c[1], c[0])
		}
	}
	if s, pos, ok := matchOne([]rune("abc"), []rune("abc"), nil, true); !ok || s != 0 || pos != nil {
		t.Error("empty pattern matches everything with score 0")
	}
}

func TestFuzzyRankingTiers(t *testing.T) {
	exact := mustScore(t, "report", "report")
	prefix := mustScore(t, "report.pdf", "report")
	boundary := mustScore(t, "my report.pdf", "report")
	substring := mustScore(t, "myreport.pdf", "report")
	scattered := mustScore(t, "r_e_p_o_r_t.pdf", "report")
	if !(exact > prefix && prefix > boundary && boundary > substring && substring > scattered) {
		t.Fatalf("tiers: exact=%d prefix=%d boundary=%d substring=%d scattered=%d", exact, prefix, boundary, substring, scattered)
	}
	// Long patterns: substring must still beat a perfect boundary-scattered match.
	long := "abcdefghijklmnopqrstuvwxyz"
	scat := ""
	for _, r := range long {
		scat += string(r) + "_"
	}
	if s1, s2 := mustScore(t, "x"+long, long), mustScore(t, scat, long); s1 <= s2 {
		t.Fatalf("long substring %d <= scattered %d", s1, s2)
	}
	// Case-insensitive.
	if mustScore(t, "README.md", "readme") != mustScore(t, "readme.md", "readme") {
		t.Error("case should not matter for exact prefix")
	}
}

func TestFuzzyBoundaryBonuses(t *testing.T) {
	// "fb" at word starts beats "fb" in the middle of words.
	wb := mustScore(t, "foo_bar.txt", "fb")
	mid := mustScore(t, "xfxxbx.txt", "fb")
	if wb <= mid {
		t.Errorf("word boundary %d <= mid %d", wb, mid)
	}
	camel := mustScore(t, "fooBar.go", "fb")
	flat := mustScore(t, "foobar.go", "fb")
	if camel <= flat {
		t.Errorf("camelCase %d <= flat %d", camel, flat)
	}
	_, pos, _ := score1("fooBar.go", "fb")
	if !reflect.DeepEqual(pos, []int{0, 3}) {
		t.Errorf("camel positions %v", pos)
	}
	// CJK / latin transition counts as a boundary.
	cjk := mustScore(t, "会议记录abc.txt", "a")
	plain := mustScore(t, "xxxxxxabc.txt", "a")
	if cjk <= plain {
		t.Errorf("cjk boundary %d <= plain %d", cjk, plain)
	}
	// Prefer the occurrence on a word boundary.
	_, pos, _ = score1("export_port.go", "port")
	if !reflect.DeepEqual(pos, []int{7, 8, 9, 10}) {
		t.Errorf("best occurrence positions %v", pos)
	}
}

func TestFuzzyPositionsAreRuneIndices(t *testing.T) {
	_, pos, ok := score1("2024年度报告.docx", "报告")
	if !ok || !reflect.DeepEqual(pos, []int{6, 7}) {
		t.Fatalf("报告 positions %v ok=%v", pos, ok)
	}
	_, pos, ok = score1("年度工作报告.docx", "年报")
	if !ok || !reflect.DeepEqual(pos, []int{0, 4}) {
		t.Fatalf("年报 positions %v ok=%v", pos, ok)
	}
	_, pos, ok = score1("Ünïcödé Fïlé.txt", "fïl")
	if !ok || !reflect.DeepEqual(pos, []int{8, 9, 10}) {
		t.Fatalf("accented positions %v", pos)
	}
	// Runes whose lower-case form has a different UTF-8 length keep indices aligned.
	_, pos, ok = score1("İstanbul KELVIN.txt", "kelvin")
	if !ok || !reflect.DeepEqual(pos, []int{9, 10, 11, 12, 13, 14}) {
		t.Fatalf("positions after İ: %v", pos)
	}
	// Subsequence positions are increasing and within bounds.
	name := "Quarterly_Business_Review_2024.pptx"
	_, pos, ok = score1(name, "qbr24")
	if !ok || !sort.IntsAreSorted(pos) || len(pos) != 5 || pos[len(pos)-1] >= len([]rune(name)) {
		t.Fatalf("qbr24 positions %v", pos)
	}
	if !reflect.DeepEqual(pos[:3], []int{0, 10, 19}) {
		t.Errorf("qbr should hit word starts, got %v", pos)
	}
}

func TestParseQuery(t *testing.T) {
	ps := parseQuery("  Foo  docs/Bar 报告 ")
	if len(ps) != 3 || ps[0].text != "foo" || ps[0].path || !ps[1].path || ps[1].text != "docs/bar" || ps[2].text != "报告" {
		t.Fatalf("parseQuery: %+v", ps)
	}
	if len(parseQuery("   ")) != 0 {
		t.Fatal("blank query")
	}
}

func TestQuickRejectAndConsume(t *testing.T) {
	if quickReject("hello world", []rune("hwd")) {
		t.Error("hwd is a subsequence")
	}
	if !quickReject("hello", []rune("hx")) {
		t.Error("hx is not a subsequence")
	}
	if !quickReject("ab", []rune("aab")) {
		t.Error("needs two a's")
	}
	if k := consume("/docs/2024/", []rune("docs/rep"), 0); k != 5 {
		t.Errorf("consume dir = %d", k)
	}
	if k := consume("report.pdf", []rune("docs/rep"), 5); k != 8 {
		t.Errorf("consume name = %d", k)
	}
}

func TestClassOf(t *testing.T) {
	cases := map[rune]charClass{'a': clsLower, 'Z': clsUpper, '5': clsDigit, '_': clsSep, ' ': clsSep, '报': clsCJK,
		'あ': clsCJK, 'ア': clsCJK, '한': clsCJK, 'É': clsUpper, 'é': clsLower, '、': clsSep, '!': clsOther}
	for r, want := range cases {
		if got := classOf(r); got != want {
			t.Errorf("classOf(%q) = %d, want %d", r, got, want)
		}
	}
}
