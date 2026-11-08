package cli

import (
	"fmt"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/spf13/cobra"
)

const maxCheckNames = 60

func newDomainCommand(app *appRuntime) *cobra.Command {
	command := newGroupCommand(
		"domain",
		"Find, price, buy, and delegate REG.RU domains",
		newDomainSearchCommand(app),
		newDomainCheckCommand(app),
		newDomainQuoteCommand(app),
		newDomainBuyCommand(app),
		newOperationCommand(app, commandSpec{
			use:        "list",
			short:      "List the account's domain services (current state)",
			capability: "domain.list",
			action:     "domain.list",
			args:       cobra.NoArgs,
		}),
		newGroupCommand("ns", "Manage domain nameservers at REG.RU", newDomainNSSetCommand(app)),
	)
	command.Long = "Registry-first domain availability, REG.API prices, and policy-bounded purchase.\n\n" +
		"search and check need no account: TCI WHOIS (.ru/.рф/.su) and Verisign RDAP (.com/.net) answer taken, likely_free, or unknown. " +
		"A name becomes free only when REG.API domain/check also confirms it (partner accounts with registrar_check = true). Unknown is never free.\n\n" +
		"buy proceeds without a prompt only while the live first-year and renewal prices are within the account caps (default 500/1500 RUB); above them it needs --force, and an unknown renewal price always refuses."
	command.Example = `  regru domain search kuban "kvartal|kvartaly"
  regru domain check domkray.ru домкрай.рф --json
  regru --account personal domain quote domkray.ru
  regru --account personal --no-input domain buy domkray.ru --ns nic
  regru --account personal domain ns set domkray.ru --ns nic`
	return command
}

func newDomainSearchCommand(app *appRuntime) *cobra.Command {
	zones := ""
	limit := domain.DefaultCandidateLimit
	command := newOperationCommand(app, commandSpec{
		use:             "search <word> [word...]",
		short:           "Generate candidate names from words and check registry availability",
		capability:      "domain.search",
		action:          "domain.search",
		accountOptional: true,
		args:            cobra.RangeArgs(1, 4),
		parameters: func() map[string][]string {
			return operationParameters("zones", zones, "limit", limit)
		},
	})
	command.Flags().StringVar(&zones, "zones", "ru,рф,com", "comma-separated zones in rank order: ru, рф (rf), su, com, net")
	command.Flags().IntVar(&limit, "limit", domain.DefaultCandidateLimit, fmt.Sprintf("maximum candidates to check (1-%d)", domain.MaxCandidateLimit))
	command.PreRunE = func(_ *cobra.Command, args []string) error {
		if limit < 1 || limit > domain.MaxCandidateLimit {
			return UsageError(fmt.Sprintf("--limit must be between 1 and %d", domain.MaxCandidateLimit))
		}
		parsed, err := parseZoneList(zones)
		if err != nil {
			return UsageError(err.Error())
		}
		if _, err := domain.Generate(args, parsed, limit); err != nil {
			return UsageError(err.Error())
		}
		return nil
	}
	command.Example = `  regru domain search кубань "квартал|кварталы"
  regru domain search domkray --zones ru,su --limit 6 --plain`
	return command
}

func newDomainCheckCommand(app *appRuntime) *cobra.Command {
	command := newOperationCommand(app, commandSpec{
		use:             "check <name> [name...]",
		short:           "Check registry availability of exact names (Unicode or Punycode)",
		capability:      "domain.search",
		action:          "domain.check",
		accountOptional: true,
		args:            domainNameArgs(1, maxCheckNames),
	})
	command.Example = `  regru domain check domkray.ru домкрай.рф
  regru domain check xn--80ahqcjlq.xn--p1ai --json`
	return command
}

func newDomainQuoteCommand(app *appRuntime) *cobra.Command {
	command := newOperationCommand(app, commandSpec{
		use:        "quote <name>",
		short:      "Show first-year and renewal prices from REG.API",
		capability: "domain.quote",
		action:     "domain.quote",
		args:       domainNameArgs(1, 1),
	})
	command.Example = `  regru --account personal domain quote domkray.ru`
	return command
}

func newDomainBuyCommand(app *appRuntime) *cobra.Command {
	var nameservers, maxPrice, maxRenewal, userServID string
	var plan map[string]any
	command := newOperationCommand(app, commandSpec{
		use:           "buy <name>",
		short:         "Register a domain for one year from the REG.RU balance with nameservers set",
		capability:    "domain.buy",
		action:        "domain.buy",
		mutating:      true,
		selfConfirmed: true,
		dryRunPlan:    func() map[string]any { return plan },
		args:          domainNameArgs(1, 1),
		parameters: func() map[string][]string {
			return operationParameters("ns", nameservers, "max-price", maxPrice, "max-renewal", maxRenewal, "user-servid", userServID)
		},
	})
	command.Flags().StringVar(&nameservers, "ns", "", "nic or a comma-separated list of 2-8 nameservers")
	command.Flags().StringVar(&maxPrice, "max-price", "", "first-year cap in RUB (default: account max_price or 500)")
	command.Flags().StringVar(&maxRenewal, "max-renewal", "", "renewal cap in RUB (default: account max_renewal or 1500)")
	command.Flags().StringVar(&userServID, "user-servid", "", "reuse the 32-hex user_servid from an outcome_unknown result")
	_ = command.MarkFlagRequired("ns")
	command.PreRunE = func(_ *cobra.Command, args []string) error {
		name, err := domain.Parse(args[0])
		if err != nil {
			return UsageError(err.Error())
		}
		servers, err := domain.ParseNameservers(nameservers)
		if err != nil {
			return UsageError(err.Error())
		}
		for flag, value := range map[string]string{"--max-price": maxPrice, "--max-renewal": maxRenewal} {
			if value != "" && !domain.ValidAmount(value) {
				return UsageError(flag + " must be a rouble amount such as 500 or 499.90")
			}
		}
		if userServID != "" && !validUserServID(userServID) {
			return UsageError("--user-servid must be 32 lower-case hexadecimal characters")
		}
		plan = map[string]any{"domain": name, "nameservers": servers, "period": 1, "payment": "balance"}
		if maxPrice != "" {
			plan["maxPrice"] = maxPrice
		}
		if maxRenewal != "" {
			plan["maxRenewal"] = maxRenewal
		}
		return nil
	}
	command.Long = "Registers <name> for one year with REG.API domain/create, using the account's saved contact profile and nameservers in the same call, paid only from the REG.RU balance.\n\n" +
		"Before sending it re-checks the registry and reads the live price. Within the caps it proceeds without a prompt (also with --no-input); above them it needs --force; an unknown renewal price always refuses. " +
		"The order is sent once. An ambiguous result is reconciled through service/get_info by user_servid and never retried automatically."
	command.Example = `  regru --account personal --dry-run domain buy domkray.ru --ns nic
  regru --account personal --no-input --json domain buy domkray.ru --ns nic --max-price 300
  regru --account personal domain buy example.ru --ns ns1.example.net,ns2.example.net`
	return command
}

func newDomainNSSetCommand(app *appRuntime) *cobra.Command {
	var nameservers string
	var plan map[string]any
	command := newOperationCommand(app, commandSpec{
		use:          "set <name>",
		short:        "Replace a domain's nameservers through REG.API domain/update_nss",
		capability:   "domain.ns",
		action:       "domain.ns.set",
		mutating:     true,
		args:         domainNameArgs(1, 1),
		confirmation: "Changing nameservers can make the domain unreachable for up to 24 hours.",
		dryRunPlan:   func() map[string]any { return plan },
		parameters: func() map[string][]string {
			return operationParameters("ns", nameservers)
		},
	})
	command.Flags().StringVar(&nameservers, "ns", "", "nic or a comma-separated list of 2-8 nameservers")
	_ = command.MarkFlagRequired("ns")
	command.PreRunE = func(_ *cobra.Command, args []string) error {
		name, err := domain.Parse(args[0])
		if err != nil {
			return UsageError(err.Error())
		}
		servers, err := domain.ParseNameservers(nameservers)
		if err != nil {
			return UsageError(err.Error())
		}
		plan = map[string]any{"domain": name, "nameservers": servers}
		return nil
	}
	command.Example = `  regru --account personal domain ns set domkray.ru --ns nic
  regru --account personal --force domain ns set domkray.ru --ns ns1.example.net,ns2.example.net`
	return command
}

func domainNameArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if err := cobra.RangeArgs(minimum, maximum)(command, args); err != nil {
			return err
		}
		for _, value := range args {
			if _, err := domain.Parse(value); err != nil {
				return UsageError(err.Error())
			}
		}
		return nil
	}
}

func parseZoneList(value string) ([]domain.Zone, error) {
	zones := []domain.Zone{}
	for _, token := range strings.Split(value, ",") {
		zone, err := domain.ParseZone(token)
		if err != nil {
			return nil, err
		}
		zones = append(zones, zone)
	}
	return zones, nil
}

func validUserServID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
