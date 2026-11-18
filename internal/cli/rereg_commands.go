package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/spf13/cobra"
)

// reregNow is the clock of the backorder guard window; tests replace it.
var reregNow = time.Now

const (
	reregContactProfile    = "profile"
	reregContactCredential = "credential"
	maxCatalogSearchLimit  = 10000
	defaultCatalogLimit    = 50
)

func newDomainReregCommand(app *appRuntime) *cobra.Command {
	command := newGroupCommand(
		"rereg",
		"Bid on releasing domains in the REG.RU backorder (освобождающиеся домены)",
		newGroupCommand("catalog", "Download and search the public list of releasing domains",
			newReregCatalogFetchCommand(app), newReregCatalogSearchCommand(app)),
		newReregQuoteCommand(app),
		newOperationCommand(app, commandSpec{
			use:        "bids",
			short:      "List the account's backorder bids, including outbid ones (REG.API)",
			capability: "domain.rereg.bids",
			action:     "domain.rereg.bids",
			args:       cobra.NoArgs,
		}),
		newReregBidCommand(app),
	)
	command.Long = "REG.RU backorder for .ru, .рф and .su names that are about to be released by the registry.\n\n" +
		"catalog and quote read public www.reg.ru pages through a throwaway headless browser (the site serves a JavaScript anti-bot page to plain HTTP clients); no account is needed. " +
		"bids and bid use REG.API. A bid charges the balance immediately and cannot be cancelled; an outbid bid is refunded. " +
		"The last payment is accepted at 16:45 MSK on the release day, and bids between 16:55 and 17:15 MSK are refused unless --allow-release-window, because the REG.API account may be shared with an automated buyer at 17:00."
	command.Example = `  regru domain rereg catalog fetch
  regru domain rereg catalog search kuban --date tomorrow
  regru --account personal domain rereg quote moonbot.ru --json
  regru --account personal domain rereg bids --plain
  regru --account personal --dry-run domain rereg bid moonbot.ru --price 450 --max-price 1000 --contact profile --ns nic`
	return command
}

func newReregCatalogFetchCommand(app *appRuntime) *cobra.Command {
	command := newOperationCommand(app, commandSpec{
		use:             "fetch",
		short:           "Download the public catalogue of releasing domains into the local cache",
		capability:      "domain.rereg.catalog",
		action:          "domain.rereg.catalog.fetch",
		accountOptional: true,
		args:            cobra.NoArgs,
		timeout:         func() time.Duration { return 4 * app.flags.timeout },
	})
	command.Long = "Downloads https://www.reg.ru/static_files/rereg_extlist.csv (about 300,000 names, refreshed during the day) through a throwaway headless browser, validates it, and replaces the local snapshot atomically. " +
		"The file says only whether a name has bids, never the amounts. The command deadline is four times --timeout."
	return command
}

func newReregCatalogSearchCommand(app *appRuntime) *cobra.Command {
	var date, zones, registrar string
	var withBids bool
	limit := defaultCatalogLimit
	command := newOperationCommand(app, commandSpec{
		use:             "search [pattern]",
		short:           "Search the local catalogue snapshot (no network)",
		capability:      "domain.rereg.catalog",
		action:          "domain.rereg.catalog.search",
		accountOptional: true,
		args:            cobra.MaximumNArgs(1),
		parameters: func() map[string][]string {
			return operationParameters("date", date, "zones", zones, "registrar", registrar, "with-bids", withBids, "limit", limit)
		},
	})
	command.Flags().StringVar(&date, "date", "", "release date: YYYY-MM-DD, today, or tomorrow (Moscow time)")
	command.Flags().StringVar(&zones, "zones", "", "comma-separated zones: ru, рф (rf), su (default all)")
	command.Flags().StringVar(&registrar, "registrar", "", "current registrar as listed, for example REGRU or RUCENTER")
	command.Flags().BoolVar(&withBids, "with-bids", false, "only names that already have bids")
	command.Flags().IntVar(&limit, "limit", defaultCatalogLimit, fmt.Sprintf("maximum rows (1-%d)", maxCatalogSearchLimit))
	command.PreRunE = func(_ *cobra.Command, args []string) error {
		if limit < 1 || limit > maxCatalogSearchLimit {
			return UsageError(fmt.Sprintf("--limit must be between 1 and %d", maxCatalogSearchLimit))
		}
		if date != "" && date != "today" && date != "tomorrow" && !dateOnly(date) {
			return UsageError("--date must be YYYY-MM-DD, today, or tomorrow")
		}
		if zones != "" {
			parsed, err := parseZoneList(zones)
			if err != nil {
				return UsageError(err.Error())
			}
			for _, zone := range parsed {
				if !domain.ReregZone(zone) {
					return UsageError("REG.RU backorders cover only ru, рф and su")
				}
			}
		}
		if len(args) == 1 && strings.TrimSpace(args[0]) == "" {
			return UsageError("the search pattern cannot be empty")
		}
		return nil
	}
	command.Long = "Filters the snapshot stored by catalog fetch. The pattern is a case-insensitive substring of the name (Unicode or Punycode). Rows are sorted by release date, then name."
	command.Example = `  regru domain rereg catalog search kuban --date tomorrow
  regru domain rereg catalog search --date 2026-11-19 --with-bids --plain`
	return command
}

func newReregQuoteCommand(app *appRuntime) *cobra.Command {
	command := newOperationCommand(app, commandSpec{
		use:             "quote <name>",
		short:           "Show the bid ladder, next bid, own bid, deadline, and balance for one name",
		capability:      "domain.rereg.quote",
		action:          "domain.rereg.quote",
		accountOptional: true,
		args:            reregNameArgs,
		timeout:         func() time.Duration { return 3 * app.flags.timeout },
	})
	command.Long = "Reads the public lot page (minimum next bid, blitz ceiling, ladder) and the zone blitz through a throwaway headless browser. " +
		"With an account it adds the own bid from REG.API domain/get_user_rereg_bids and the balance; the release date comes from the own bid or the local catalogue snapshot. " +
		"data follows the shared dropcatch quote contract (amounts in kopecks). The command deadline is three times --timeout."
	command.Example = `  regru --account personal domain rereg quote moonbot.ru
  regru --no-input --json domain rereg quote moonbot.ru`
	return command
}

func newReregBidCommand(app *appRuntime) *cobra.Command {
	var price, maxPrice, contact, nameservers string
	var overLimit, allowWindow bool
	var plan dryRunPlan
	command := newOperationCommand(app, commandSpec{
		use:        "bid <name>",
		short:      "Place or raise a backorder bid; charges the balance immediately (REG.API)",
		capability: "domain.rereg.bid",
		action:     "domain.rereg.bid",
		mutating:   true,
		args:       reregNameArgs,
		timeout:    func() time.Duration { return 3 * app.flags.timeout },
		confirmation: "A backorder bid charges the REG.RU balance immediately and cannot be cancelled; " +
			"an outbid bid is refunded to the balance.",
		dryRunPlan: func() dryRunPlan { return plan },
		parameters: func() map[string][]string {
			return operationParameters("price", price, "max-price", maxPrice, "contact", contact, "ns", nameservers,
				"over-limit", overLimit, "allow-release-window", allowWindow)
		},
	})
	command.Flags().StringVar(&price, "price", "", "bid in RUB; must be a step of the REG.RU ladder (see quote)")
	command.Flags().StringVar(&maxPrice, "max-price", "", "the most you approve for this bid, RUB; --price above it is refused")
	command.Flags().StringVar(&contact, "contact", "", "registrant source: profile (account contact profile) or credential (rereg.contacts from the credential process)")
	command.Flags().StringVar(&nameservers, "ns", "", "nic or a comma-separated list of 2-8 nameservers")
	command.Flags().BoolVar(&overLimit, "over-limit", false, "allow a bid above the per-bid or daily cap (rereg_max_bid, rereg_max_daily)")
	command.Flags().BoolVar(&allowWindow, "allow-release-window", false, "allow bidding between 16:55 and 17:15 MSK and after the 16:45 MSK release-day deadline")
	for _, flag := range []string{"price", "max-price", "contact", "ns"} {
		_ = command.MarkFlagRequired(flag)
	}
	command.PreRunE = func(_ *cobra.Command, args []string) error {
		name, err := domain.Parse(args[0])
		if err != nil {
			return UsageError(err.Error())
		}
		priceKopecks, priceOK := reregAmount(price)
		maxKopecks, maxOK := reregAmount(maxPrice)
		if !priceOK {
			return UsageError("--price must be a positive rouble amount such as 450")
		}
		if !maxOK {
			return UsageError("--max-price must be a positive rouble amount such as 1000")
		}
		if priceKopecks > maxKopecks {
			return UsageError(fmt.Sprintf("--price %s is above --max-price %s", price, maxPrice))
		}
		if contact != reregContactProfile && contact != reregContactCredential {
			return UsageError("--contact must be profile or credential")
		}
		servers, err := domain.ParseNameservers(nameservers)
		if err != nil {
			return UsageError(err.Error())
		}
		if start, end, inside := domain.ReregGuardWindow(reregNow()); inside && !allowWindow {
			return PolicyRefused("guard_window", fmt.Sprintf(
				"backorder bids are refused between %s and %s MSK because the REG.API account may be busy with the 17:00 auto-buyer; rerun later or pass --allow-release-window",
				start.Format("15:04"), end.Format("15:04"),
			), map[string]any{"override": "--allow-release-window", "windowStart": start.Format(time.RFC3339), "windowEnd": end.Format(time.RFC3339)})
		}
		plan = dryRunPlan{
			data: map[string]any{
				"domain": name, "priceKopecks": priceKopecks, "maxPriceKopecks": maxKopecks,
				"contact": contact, "nameservers": servers, "payment": "balance, charged immediately",
				"overLimit": overLimit, "allowReleaseWindow": allowWindow,
			},
			human: fmt.Sprintf("Backorder bid %s RUB on %s (max %s RUB), contact %s, NS %s; charged from the balance immediately",
				domain.Roubles(priceKopecks), name.Display(), domain.Roubles(maxKopecks), contact, strings.Join(servers, ", ")),
			plain: strings.Join([]string{
				"plan", escapePlainField(name.Unicode), escapePlainField(name.ASCII),
				fmt.Sprint(priceKopecks), fmt.Sprint(maxKopecks), contact, escapePlainField(strings.Join(servers, ",")),
			}, "\t"),
		}
		return nil
	}
	command.Long = "Sends one REG.API domain/set_rereg_bids for <name>. REG.RU charges the balance at once; a paid bid cannot be cancelled, and an outbid one is refunded.\n\n" +
		"Before sending it checks, in order: the 16:55-17:15 MSK guard window, the per-bid cap (rereg_max_bid, default 3000 RUB) and the daily cap (rereg_max_daily, default 10000 RUB, from the local bid journal), " +
		"the own bid and ladder from REG.API, the public minimum next bid and blitz ceiling, the 16:45 MSK release-day deadline, and the balance. It needs TTY confirmation or --force. " +
		"--over-limit lifts the caps; --allow-release-window lifts the guard window and the deadline.\n\n" +
		"The bid is sent once. REG.API has no idempotency key: an ambiguous result is reconciled through domain/get_user_rereg_bids and the balance, never resent, and otherwise ends as outcome_unknown (exit 10). " +
		"--contact profile sends the account contact profile (contact_profile_type/contact_profile_name); --contact credential sends the ESIA-verified contacts hash from the credential-process field rereg.contacts."
	command.Example = `  regru --account personal --dry-run domain rereg bid moonbot.ru --price 450 --max-price 1000 --contact profile --ns nic
  regru --account personal --force --no-input --json domain rereg bid moonbot.ru --price 1950 --max-price 2000 --contact credential --ns nic`
	return command
}

func reregNameArgs(command *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(command, args); err != nil {
		return err
	}
	name, err := domain.Parse(args[0])
	if err != nil {
		return UsageError(err.Error())
	}
	if !domain.ReregZone(name.Zone) {
		return UsageError("REG.RU backorders cover only .ru, .рф and .su names")
	}
	return nil
}

func reregAmount(value string) (int64, bool) {
	if !domain.ValidAmount(value) {
		return 0, false
	}
	kopecks, ok := domain.Kopecks(value)
	return kopecks, ok && kopecks > 0
}
