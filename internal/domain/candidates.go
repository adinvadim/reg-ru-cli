package domain

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultCandidateLimit caps one search run so it stays inside the TCI budget.
	DefaultCandidateLimit = 24
	// MaxCandidateLimit is the largest accepted --limit.
	MaxCandidateLimit   = 60
	maxSpellingsPerForm = 4
	maxWords            = 4
	maxFormsPerWord     = 4
)

// DefaultZones is the default --zones order.
var DefaultZones = []Zone{ZoneRU, ZoneRF, ZoneCOM}

// Candidate is one ranked name produced by Generate.
type Candidate struct {
	Name          Name `json:"name"`
	Rank          int  `json:"rank"`
	Hyphenated    bool `json:"hyphenated"`
	Substitutions int  `json:"substitutions"`
}

type translit struct {
	cyrillic     string
	primary      string
	alternatives []string
}

var translitTable = []translit{
	{"кс", "ks", []string{"x"}},
	{"а", "a", nil}, {"б", "b", nil}, {"в", "v", nil}, {"г", "g", nil}, {"д", "d", nil},
	{"е", "e", nil}, {"ё", "e", []string{"yo"}}, {"ж", "zh", []string{"j"}}, {"з", "z", nil},
	{"и", "i", nil}, {"й", "y", []string{"i", "j"}}, {"к", "k", nil}, {"л", "l", nil},
	{"м", "m", nil}, {"н", "n", nil}, {"о", "o", nil}, {"п", "p", nil}, {"р", "r", nil},
	{"с", "s", nil}, {"т", "t", nil}, {"у", "u", nil}, {"ф", "f", nil},
	{"х", "h", []string{"kh"}}, {"ц", "ts", []string{"c"}}, {"ч", "ch", nil},
	{"ш", "sh", nil}, {"щ", "sch", []string{"shch"}}, {"ъ", "", nil},
	{"ы", "y", []string{"i"}}, {"ь", "", nil}, {"э", "e", nil},
	{"ю", "yu", []string{"ju", "u"}}, {"я", "ya", []string{"ja", "a"}},
}

type segment struct {
	primary      string
	alternatives []string
}

type spelling struct {
	text          string
	substitutions int
}

// Transliterate returns the primary Latin spelling followed by spellings that
// differ from it by exactly one well-known substitution, capped at four.
func Transliterate(word string) ([]string, error) {
	spellings, err := transliterate(word)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(spellings))
	for _, value := range spellings {
		result = append(result, value.text)
	}
	return result, nil
}

func transliterate(word string) ([]spelling, error) {
	segments := []segment{}
	rest := strings.ToLower(word)
	for rest != "" {
		matched := false
		for _, entry := range translitTable {
			if strings.HasPrefix(rest, entry.cyrillic) {
				segments = append(segments, segment{primary: entry.primary, alternatives: entry.alternatives})
				rest = rest[len(entry.cyrillic):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		character, size := utf8.DecodeRuneInString(rest)
		if character == '-' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' {
			segments = append(segments, segment{primary: string(character)})
			rest = rest[size:]
			continue
		}
		return nil, invalid("word %q contains a character that cannot be transliterated", word)
	}
	join := func(override int, value string) string {
		var builder strings.Builder
		for index, part := range segments {
			if index == override {
				builder.WriteString(value)
				continue
			}
			builder.WriteString(part.primary)
		}
		return builder.String()
	}
	result := []spelling{{text: join(-1, "")}}
	seen := map[string]bool{result[0].text: true}
	for index, part := range segments {
		for _, alternative := range part.alternatives {
			if len(result) >= maxSpellingsPerForm {
				return result, nil
			}
			text := join(index, alternative)
			if seen[text] {
				continue
			}
			seen[text] = true
			result = append(result, spelling{text: text, substitutions: 1})
		}
	}
	return result, nil
}

type wordForm struct {
	raw      string
	cyrillic bool
}

// ErrNoCandidates means the words produced no valid name in the given zones.
var ErrNoCandidates = errors.New("the words produce no valid domain name in the requested zones")

// Generate turns words into a deterministic, ranked candidate list. A word may
// carry caller-chosen alternatives separated by "|". Cyrillic words are
// transliterated for Latin zones; .рф receives only Cyrillic input as typed.
func Generate(words []string, zones []Zone, limit int) ([]Candidate, error) {
	if len(words) == 0 || len(words) > maxWords {
		return nil, invalid("search needs one to %d words", maxWords)
	}
	if len(zones) == 0 {
		zones = DefaultZones
	}
	if limit <= 0 {
		limit = DefaultCandidateLimit
	}
	if limit > MaxCandidateLimit {
		return nil, invalid("limit cannot exceed %d", MaxCandidateLimit)
	}
	forms := make([][]wordForm, 0, len(words))
	for _, word := range words {
		parsed, err := parseWord(word)
		if err != nil {
			return nil, err
		}
		forms = append(forms, parsed)
	}

	type ranked struct {
		candidate Candidate
		exact     bool
		zoneIndex int
		length    int
	}
	all := []ranked{}
	add := func(label string, zone Zone, zoneIndex int, hyphen bool, substitutions int, exact bool) {
		name, err := Parse(label + "." + string(zone))
		if err != nil {
			return
		}
		length := len(name.Label())
		if zone == ZoneRF {
			length = utf8.RuneCountInString(strings.TrimSuffix(name.Unicode, ".рф"))
		}
		all = append(all, ranked{
			candidate: Candidate{Name: name, Hyphenated: hyphen, Substitutions: substitutions},
			exact:     exact, zoneIndex: zoneIndex, length: length,
		})
	}

	for _, combination := range cartesian(forms) {
		allFirst := true
		allCyrillic := true
		for wordIndex, formIndex := range combination {
			if formIndex != 0 {
				allFirst = false
			}
			if !forms[wordIndex][formIndex].cyrillic {
				allCyrillic = false
			}
		}
		for zoneIndex, zone := range zones {
			if zone == ZoneRF {
				if !allCyrillic {
					continue
				}
				parts := make([]string, len(combination))
				for wordIndex, formIndex := range combination {
					parts[wordIndex] = forms[wordIndex][formIndex].raw
				}
				add(strings.Join(parts, ""), zone, zoneIndex, false, 0, allFirst && zoneIndex == 0)
				if len(parts) > 1 {
					add(strings.Join(parts, "-"), zone, zoneIndex, true, 0, false)
				}
				continue
			}
			for _, phrase := range latinPhrases(forms, combination) {
				exact := allFirst && phrase.substitutions == 0 && zoneIndex == 0
				add(strings.Join(phrase.parts, ""), zone, zoneIndex, false, phrase.substitutions, exact)
				if len(phrase.parts) > 1 {
					add(strings.Join(phrase.parts, "-"), zone, zoneIndex, true, phrase.substitutions, false)
				}
			}
		}
	}
	if len(all) == 0 {
		return nil, ErrNoCandidates
	}
	sort.SliceStable(all, func(i, j int) bool {
		left, right := all[i], all[j]
		if left.exact != right.exact {
			return left.exact
		}
		if left.zoneIndex != right.zoneIndex {
			return left.zoneIndex < right.zoneIndex
		}
		if left.candidate.Hyphenated != right.candidate.Hyphenated {
			return !left.candidate.Hyphenated
		}
		if left.candidate.Substitutions != right.candidate.Substitutions {
			return left.candidate.Substitutions < right.candidate.Substitutions
		}
		if left.length != right.length {
			return left.length < right.length
		}
		return left.candidate.Name.ASCII < right.candidate.Name.ASCII
	})
	seen := map[string]bool{}
	result := make([]Candidate, 0, limit)
	for _, item := range all {
		if seen[item.candidate.Name.ASCII] {
			continue
		}
		seen[item.candidate.Name.ASCII] = true
		if len(result) == limit {
			break
		}
		item.candidate.Rank = len(result) + 1
		result = append(result, item.candidate)
	}
	return result, nil
}

func parseWord(word string) ([]wordForm, error) {
	parts := strings.Split(word, "|")
	if len(parts) > maxFormsPerWord {
		return nil, invalid("word %q has more than %d alternatives", word, maxFormsPerWord)
	}
	forms := make([]wordForm, 0, len(parts))
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			return nil, invalid("word %q has an empty alternative", word)
		}
		cyrillic := false
		for _, character := range value {
			switch {
			case unicode.Is(unicode.Cyrillic, character):
				cyrillic = true
			case character == '-' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z':
			default:
				return nil, invalid("word %q may contain only letters, digits, and hyphens", word)
			}
		}
		forms = append(forms, wordForm{raw: value, cyrillic: cyrillic})
	}
	return forms, nil
}

type phrase struct {
	parts         []string
	substitutions int
}

func latinPhrases(forms [][]wordForm, combination []int) []phrase {
	spellings := make([][]spelling, len(combination))
	for wordIndex, formIndex := range combination {
		values, err := transliterate(forms[wordIndex][formIndex].raw)
		if err != nil {
			return nil
		}
		spellings[wordIndex] = values
	}
	base := make([]string, len(spellings))
	for index, values := range spellings {
		base[index] = values[0].text
	}
	result := []phrase{{parts: base}}
	for wordIndex, values := range spellings {
		for _, value := range values[1:] {
			parts := append([]string(nil), base...)
			parts[wordIndex] = value.text
			result = append(result, phrase{parts: parts, substitutions: value.substitutions})
		}
	}
	return result
}

func cartesian(forms [][]wordForm) [][]int {
	result := [][]int{{}}
	for _, options := range forms {
		next := make([][]int, 0, len(result)*len(options))
		for _, prefix := range result {
			for index := range options {
				combination := append(append([]int(nil), prefix...), index)
				next = append(next, combination)
			}
		}
		result = next
	}
	return result
}
