package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseNormalisesIDNAndRoundTrips(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"домкрай.рф", "Домкрай.РФ", "домкрай.xn--p1ai", "xn--80ahqcjlq.xn--p1ai", "домкрай.рф."} {
		name, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", input, err)
		}
		if name.Unicode != "домкрай.рф" || name.ASCII != "xn--80ahqcjlq.xn--p1ai" || name.Zone != ZoneRF {
			t.Fatalf("Parse(%q) = %#v", input, name)
		}
		if name.Display() != "домкрай.рф (xn--80ahqcjlq.xn--p1ai)" {
			t.Fatalf("Display() = %q", name.Display())
		}
	}

	if short, err := Parse("x.com"); err != nil || short.ASCII != "x.com" {
		t.Fatalf("single-character .com label = %#v, %v; grandfathered names must parse", short, err)
	}

	latin, err := Parse("KUBAN-Kvartal.RU")
	if err != nil || latin.ASCII != "kuban-kvartal.ru" || latin.Unicode != latin.ASCII || latin.Display() != "kuban-kvartal.ru" {
		t.Fatalf("Parse(latin) = %#v, %v", latin, err)
	}
}

func TestParseExposesHandMadePunycodeMismatch(t *testing.T) {
	t.Parallel()

	handMade, err := Parse("xn--80ajmfjh7a.xn--p1ai")
	if err != nil {
		t.Fatalf("Parse(hand-made) error = %v", err)
	}
	if !handMade.FromPunycode || handMade.Unicode != "ейамткн.рф" {
		t.Fatalf("hand-made punycode decoded to %#v; the wrong Unicode form must be visible", handMade)
	}
	intended, err := Parse("домкрай.рф")
	if err != nil {
		t.Fatal(err)
	}
	if intended.FromPunycode || intended.ASCII == handMade.ASCII {
		t.Fatalf("intended %#v must differ from hand-made %#v", intended, handMade)
	}
}

func TestParseRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"", "https://kuban.ru", "kuban.ru/path", "user@kuban.ru", "kuban .ru", "www.kuban.ru",
		"kuban", "kuban.org", "ab--c.ru", "-bad.ru", "bad-.ru", "дом_край.рф", "kubanрф.рф",
		"кубань.ru", "a.ru", strings.Repeat("a", 64) + ".com",
	} {
		if _, err := Parse(input); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalidName", input, err)
		}
	}
}

func TestParseZoneAliases(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]Zone{"RU": ZoneRU, ".рф": ZoneRF, "rf": ZoneRF, "xn--p1ai": ZoneRF, "su": ZoneSU, "com": ZoneCOM, "net": ZoneNET} {
		got, err := ParseZone(input)
		if err != nil || got != want {
			t.Errorf("ParseZone(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseZone("org"); err == nil {
		t.Error("ParseZone(org) succeeded")
	}
}

func TestTransliterateUsesPrimaryThenSingleSubstitutions(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"домкрай":  {"domkray", "domkrai", "domkraj"},
		"кварталы": {"kvartaly", "kvartali"},
		"кубань":   {"kuban"},
		"экспресс": {"ekspress", "express"},
		"щука":     {"schuka", "shchuka"},
		"южная":    {"yuzhnaya", "juzhnaya", "uzhnaya", "yujnaya"},
	}
	for word, want := range cases {
		got, err := Transliterate(word)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Transliterate(%q) = %v, %v; want %v", word, got, err, want)
		}
	}
}

func candidateNames(candidates []Candidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Name.Unicode)
	}
	return names
}

func TestGenerateIsDeterministicAndRanked(t *testing.T) {
	t.Parallel()

	first, err := Generate([]string{"кубань", "квартал|кварталы"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := Generate([]string{"кубань", "квартал|кварталы"}, nil, 0)
		if err != nil || !reflect.DeepEqual(first, again) {
			t.Fatalf("Generate is not deterministic: %v vs %v (%v)", candidateNames(first), candidateNames(again), err)
		}
	}
	want := []string{
		"kubankvartal.ru", "kubankvartaly.ru", "kubankvartali.ru",
		"kuban-kvartal.ru", "kuban-kvartaly.ru", "kuban-kvartali.ru",
		"кубаньквартал.рф", "кубанькварталы.рф", "кубань-квартал.рф", "кубань-кварталы.рф",
		"kubankvartal.com", "kubankvartaly.com", "kubankvartali.com",
		"kuban-kvartal.com", "kuban-kvartaly.com", "kuban-kvartali.com",
	}
	if got := candidateNames(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates =\n%v\nwant\n%v", got, want)
	}
	for index, candidate := range first {
		if candidate.Rank != index+1 {
			t.Fatalf("rank %d at index %d", candidate.Rank, index)
		}
	}
}

func TestGenerateHonoursZonesLimitAndLatinInput(t *testing.T) {
	t.Parallel()

	got, err := Generate([]string{"kubankvartal"}, []Zone{ZoneCOM, ZoneRF, ZoneRU}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if names := candidateNames(got); !reflect.DeepEqual(names, []string{"kubankvartal.com", "kubankvartal.ru"}) {
		t.Fatalf("latin input candidates = %v; .рф must not receive Latin labels", names)
	}

	limited, err := Generate([]string{"домкрай"}, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if names := candidateNames(limited); !reflect.DeepEqual(names, []string{"domkray.ru", "domkrai.ru"}) {
		t.Fatalf("limited candidates = %v", names)
	}

	explicit, err := Generate([]string{"домкрай|domkraj"}, []Zone{ZoneRU}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if names := candidateNames(explicit); !reflect.DeepEqual(names, []string{"domkray.ru", "domkraj.ru"}) {
		t.Fatalf("explicit spelling lost its rank to a duplicate substitution: %v", names)
	}

	if _, err := Generate([]string{"a"}, []Zone{ZoneRU}, 0); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("too-short word error = %v", err)
	}
	if _, err := Generate([]string{"kuban"}, nil, MaxCandidateLimit+1); err == nil {
		t.Fatal("limit above the maximum was accepted")
	}
	if _, err := Generate([]string{"ku ban!"}, nil, 0); err == nil {
		t.Fatal("punctuation in a word was accepted")
	}
}

func TestParseNameservers(t *testing.T) {
	t.Parallel()

	nic, err := ParseNameservers("NIC")
	if err != nil || !reflect.DeepEqual(nic, NICNameservers) {
		t.Fatalf("nic preset = %v, %v", nic, err)
	}
	custom, err := ParseNameservers("NS1.example.com., ns2.example.com")
	if err != nil || !reflect.DeepEqual(custom, []string{"ns1.example.com", "ns2.example.com"}) {
		t.Fatalf("custom = %v, %v", custom, err)
	}
	for _, value := range []string{"", "ns1.example.com", "ns1.example.com,ns1.example.com", "bad_host.com,ns2.example.com", "localhost,ns2.example.com"} {
		if _, err := ParseNameservers(value); err == nil {
			t.Errorf("ParseNameservers(%q) succeeded", value)
		}
	}
}

func TestEvaluateSpendingPolicy(t *testing.T) {
	t.Parallel()

	caps := SpendingCaps{MaxPrice: "500", MaxRenewal: "1500"}
	cases := []struct {
		name   string
		quote  Quote
		caps   SpendingCaps
		force  bool
		want   Decision
		reason string
	}{
		{"within caps", Quote{"169", "1199"}, caps, false, DecisionProceed, "within_caps"},
		{"exactly at caps", Quote{"500.00", "1500"}, caps, false, DecisionProceed, "within_caps"},
		{"first year above cap", Quote{"500.01", "900"}, caps, false, DecisionNeedsForce, "above_cap"},
		{"renewal above cap", Quote{"169", "3075"}, caps, false, DecisionNeedsForce, "above_cap"},
		{"force overrides cap", Quote{"1905", "1905"}, caps, true, DecisionProceed, "caps_overridden_by_force"},
		{"unknown renewal refuses even with force", Quote{"169", ""}, caps, true, DecisionRefuse, "renewal_price_unknown"},
		{"zero renewal is unknown", Quote{"169", "0"}, caps, false, DecisionRefuse, "renewal_price_unknown"},
		{"unknown first year refuses", Quote{"", "1000"}, caps, true, DecisionRefuse, "first_year_price_unknown"},
		{"custom caps", Quote{"169", "1199"}, SpendingCaps{"100", "1500"}, false, DecisionNeedsForce, "above_cap"},
		{"invalid cap refuses", Quote{"169", "1199"}, SpendingCaps{"1e3", "1500"}, false, DecisionRefuse, "invalid_cap"},
	}
	for _, testCase := range cases {
		got := Evaluate(testCase.quote, testCase.caps, testCase.force)
		if got.Decision != testCase.want || got.Reason != testCase.reason {
			t.Errorf("%s: Evaluate = %#v, want %s/%s", testCase.name, got, testCase.want, testCase.reason)
		}
	}
}

func TestShortfall(t *testing.T) {
	t.Parallel()

	if missing, short := Shortfall("169", "131.50"); !short || missing != "37.50" {
		t.Fatalf("Shortfall = %q, %v", missing, short)
	}
	if _, short := Shortfall("169", "300"); short {
		t.Fatal("sufficient balance reported a shortfall")
	}
}
