// Package domain holds the provider-neutral rules for domain names: IDN
// normalisation, supported zones, candidate generation, nameserver sets, and
// the spending policy used before a purchase.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// Zone is a supported top-level zone in its Unicode presentation form.
type Zone string

const (
	ZoneRU  Zone = "ru"
	ZoneRF  Zone = "рф"
	ZoneSU  Zone = "su"
	ZoneCOM Zone = "com"
	ZoneNET Zone = "net"
)

// SupportedZones lists every zone the CLI can check, in documentation order.
var SupportedZones = []Zone{ZoneRU, ZoneRF, ZoneSU, ZoneCOM, ZoneNET}

const (
	minRussianLabelRunes = 2
	maxLabelASCII        = 63
)

// Name is one validated second-level domain name.
type Name struct {
	Unicode string `json:"unicode"`
	ASCII   string `json:"ascii"`
	Zone    Zone   `json:"zone"`
	// FromPunycode reports that the caller typed an xn-- label, so the decoded
	// Unicode form must be shown back before anyone acts on it.
	FromPunycode bool `json:"fromPunycode,omitempty"`
}

// Label returns the ASCII second-level label without the zone.
func (n Name) Label() string {
	label, _, _ := strings.Cut(n.ASCII, ".")
	return label
}

// Display renders both forms, for example "домкрай.рф (xn--80ahqcjlq.xn--p1ai)".
func (n Name) Display() string {
	if n.Unicode == n.ASCII {
		return n.ASCII
	}
	return fmt.Sprintf("%s (%s)", n.Unicode, n.ASCII)
}

// ErrInvalidName wraps every local validation failure.
var ErrInvalidName = errors.New("invalid domain name")

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidName, fmt.Sprintf(format, arguments...))
}

// ParseZone normalises a zone token such as "RU", "рф", "rf", or "xn--p1ai".
func ParseZone(value string) (Zone, error) {
	token := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
	switch token {
	case "ru":
		return ZoneRU, nil
	case "рф", "xn--p1ai", "rf":
		return ZoneRF, nil
	case "su":
		return ZoneSU, nil
	case "com":
		return ZoneCOM, nil
	case "net":
		return ZoneNET, nil
	}
	return "", invalid("zone %q is not supported; use ru, рф, su, com, or net", value)
}

// ASCII returns the ACE form of the zone.
func (z Zone) ASCII() string {
	if z == ZoneRF {
		return "xn--p1ai"
	}
	return string(z)
}

// Parse validates one user-supplied domain name. It folds case with the UTS #46
// lookup mapping, converts with the strict registration profile, requires an
// exact round trip, and applies the per-zone script rules.
func Parse(input string) (Name, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Name{}, invalid("name is empty")
	}
	if !utf8.ValidString(raw) {
		return Name{}, invalid("name is not valid UTF-8")
	}
	for _, character := range raw {
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune(`/@:?#%\`, character) {
			return Name{}, invalid("%q must be a bare domain name, not a URL or address", input)
		}
	}
	raw = strings.TrimSuffix(raw, ".")
	mapped, err := idna.Lookup.ToUnicode(raw)
	if err != nil {
		return Name{}, invalid("%q: %v", input, err)
	}
	labels := strings.Split(mapped, ".")
	if len(labels) != 2 || labels[0] == "" || labels[1] == "" {
		return Name{}, invalid("%q must be exactly one label plus a zone, without subdomains", input)
	}
	zone, err := ParseZone(labels[1])
	if err != nil {
		return Name{}, err
	}
	unicodeName := labels[0] + "." + string(zone)
	ascii, err := idna.Registration.ToASCII(unicodeName)
	if err != nil {
		return Name{}, invalid("%q: %v", input, err)
	}
	roundTrip, err := idna.Registration.ToUnicode(ascii)
	if err != nil {
		return Name{}, invalid("%q does not round-trip: %v", input, err)
	}
	again, err := idna.Registration.ToASCII(roundTrip)
	if err != nil || again != ascii || roundTrip != unicodeName {
		return Name{}, invalid("%q does not round-trip between Unicode and Punycode", input)
	}
	if err := checkZoneScript(labels[0], zone); err != nil {
		return Name{}, err
	}
	label, _, _ := strings.Cut(ascii, ".")
	if len(label) > maxLabelASCII {
		return Name{}, invalid("%q is longer than 63 ASCII characters", input)
	}
	if minimum := minLabelLength(zone); utf8.RuneCountInString(labels[0]) < minimum {
		return Name{}, invalid("%q is shorter than %d characters", input, minimum)
	}
	fromPunycode := false
	for _, part := range strings.Split(strings.ToLower(raw), ".") {
		if strings.HasPrefix(part, "xn--") {
			fromPunycode = true
		}
	}
	return Name{Unicode: unicodeName, ASCII: ascii, Zone: zone, FromPunycode: fromPunycode}, nil
}

func minLabelLength(zone Zone) int {
	switch zone {
	case ZoneRU, ZoneRF, ZoneSU:
		return minRussianLabelRunes
	default:
		return 1
	}
}

func checkZoneScript(label string, zone Zone) error {
	if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return invalid("label %q cannot start or end with a hyphen", label)
	}
	for _, character := range label {
		switch {
		case character == '-' || character >= '0' && character <= '9':
			continue
		case zone == ZoneRF:
			if !unicode.Is(unicode.Cyrillic, character) {
				return invalid(".рф labels allow only Cyrillic letters, digits, and hyphens: %q", label)
			}
		default:
			if character < 'a' || character > 'z' {
				return invalid(".%s labels allow only Latin letters, digits, and hyphens: %q", zone, label)
			}
		}
	}
	return nil
}
