package domain

import (
	"math/big"
	"regexp"
	"strings"
)

const (
	// NICPreset selects the RU-CENTER (NIC.RU) DNS-hosting nameservers.
	NICPreset = "nic"
	// DefaultMaxPrice is the first-year cap in roubles when no profile or flag sets one.
	DefaultMaxPrice = "500"
	// DefaultMaxRenewal is the renewal cap in roubles when no profile or flag sets one.
	DefaultMaxRenewal = "1500"
	minNameservers    = 2
	maxNameservers    = 8
)

// NICNameservers is the nameserver set NIC.RU assigns to DNS-hosting zones.
var NICNameservers = []string{
	"ns3-l2.nic.ru", "ns4-l2.nic.ru", "ns8-l2.nic.ru", "ns4-cloud.nic.ru", "ns8-cloud.nic.ru",
}

var (
	hostnameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	amountPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,15})(?:\.[0-9]{1,2})?$`)
)

// ParseNameservers accepts the "nic" preset or a comma-separated list of two
// to eight distinct hostnames and returns them lower-cased in input order.
func ParseNameservers(value string) ([]string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == NICPreset {
		return append([]string(nil), NICNameservers...), nil
	}
	if trimmed == "" {
		return nil, invalid("--ns is required: use nic or a comma-separated nameserver list")
	}
	result := []string{}
	seen := map[string]bool{}
	for _, item := range strings.Split(trimmed, ",") {
		host := strings.TrimSuffix(strings.TrimSpace(item), ".")
		if !validHostname(host) {
			return nil, invalid("nameserver %q is not a valid hostname", item)
		}
		if seen[host] {
			return nil, invalid("nameserver %q is listed twice", host)
		}
		seen[host] = true
		result = append(result, host)
	}
	if len(result) < minNameservers || len(result) > maxNameservers {
		return nil, invalid("between %d and %d nameservers are required", minNameservers, maxNameservers)
	}
	return result, nil
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !hostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidAmount reports whether value is a non-negative rouble amount with at
// most two decimal places.
func ValidAmount(value string) bool { return amountPattern.MatchString(value) }

// SpendingCaps bounds an unattended purchase.
type SpendingCaps struct {
	MaxPrice   string `json:"maxPrice"`
	MaxRenewal string `json:"maxRenewal"`
}

// Quote is the live price the policy is evaluated against.
type Quote struct {
	FirstYear string
	Renewal   string
}

// Decision is the outcome of evaluating a quote against the caps.
type Decision string

const (
	// DecisionProceed means the quote is within both caps.
	DecisionProceed Decision = "proceed"
	// DecisionNeedsForce means at least one cap is exceeded; --force is required.
	DecisionNeedsForce Decision = "needs_force"
	// DecisionRefuse means the quote cannot be evaluated safely.
	DecisionRefuse Decision = "refuse"
)

// Evaluation explains a Decision.
type Evaluation struct {
	Decision        Decision `json:"decision"`
	Reason          string   `json:"reason"`
	PriceOverCap    bool     `json:"priceOverCap"`
	RenewalOverCap  bool     `json:"renewalOverCap"`
	ForceOverridden bool     `json:"forceOverridden"`
}

// Evaluate applies the spending policy: within both caps proceeds without a
// prompt, above a cap needs --force, and an unknown renewal price refuses.
func Evaluate(quote Quote, caps SpendingCaps, force bool) Evaluation {
	firstYear, firstOK := positiveRat(quote.FirstYear)
	if !firstOK {
		return Evaluation{Decision: DecisionRefuse, Reason: "first_year_price_unknown"}
	}
	renewal, renewalOK := positiveRat(quote.Renewal)
	if !renewalOK {
		return Evaluation{Decision: DecisionRefuse, Reason: "renewal_price_unknown"}
	}
	maxPrice, priceCapOK := amountRat(caps.MaxPrice)
	maxRenewal, renewalCapOK := amountRat(caps.MaxRenewal)
	if !priceCapOK || !renewalCapOK {
		return Evaluation{Decision: DecisionRefuse, Reason: "invalid_cap"}
	}
	evaluation := Evaluation{
		PriceOverCap:   firstYear.Cmp(maxPrice) > 0,
		RenewalOverCap: renewal.Cmp(maxRenewal) > 0,
	}
	switch {
	case !evaluation.PriceOverCap && !evaluation.RenewalOverCap:
		evaluation.Decision = DecisionProceed
		evaluation.Reason = "within_caps"
	case force:
		evaluation.Decision = DecisionProceed
		evaluation.Reason = "caps_overridden_by_force"
		evaluation.ForceOverridden = true
	default:
		evaluation.Decision = DecisionNeedsForce
		evaluation.Reason = "above_cap"
	}
	return evaluation
}

// Shortfall returns price minus balance when the balance is insufficient.
func Shortfall(price, balance string) (string, bool) {
	priceValue, priceOK := new(big.Rat).SetString(price)
	balanceValue, balanceOK := new(big.Rat).SetString(balance)
	if !priceOK || !balanceOK || balanceValue.Cmp(priceValue) >= 0 {
		return "", false
	}
	return new(big.Rat).Sub(priceValue, balanceValue).FloatString(2), true
}

// SameAmount compares two decimal strings numerically.
func SameAmount(first, second string) bool {
	firstValue, firstOK := new(big.Rat).SetString(first)
	secondValue, secondOK := new(big.Rat).SetString(second)
	return firstOK && secondOK && firstValue.Cmp(secondValue) == 0
}

func positiveRat(value string) (*big.Rat, bool) {
	parsed, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || parsed.Sign() <= 0 {
		return nil, false
	}
	return parsed, true
}

func amountRat(value string) (*big.Rat, bool) {
	if !ValidAmount(value) {
		return nil, false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return parsed, ok
}
