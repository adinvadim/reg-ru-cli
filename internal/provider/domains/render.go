package domains

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/domain"
	"github.com/adinvadim/reg-ru-cli/internal/provider/regapi"
	"github.com/adinvadim/reg-ru-cli/internal/provider/registry"
)

var stateOrder = map[registry.State]int{
	registry.StateFree: 0, registry.StateLikelyFree: 1, registry.StateTaken: 2, registry.StateUnknown: 3,
}

func renderAvailability(operation cli.Operation, verdicts []registry.Verdict, status RegistrarStatus, ranked bool) (cli.Result, error) {
	ordered := append([]registry.Verdict(nil), verdicts...)
	if ranked {
		sort.SliceStable(ordered, func(i, j int) bool {
			return stateOrder[ordered[i].State] < stateOrder[ordered[j].State]
		})
	}
	counts := map[registry.State]int{}
	warnings := []cli.Warning{}
	plain := make([]string, 0, len(ordered))
	human := make([]string, 0, len(ordered)+2)
	width := 0
	for _, verdict := range ordered {
		if size := len([]rune(verdict.Name.Display())); size > width {
			width = size
		}
	}
	for _, verdict := range ordered {
		counts[verdict.State]++
		if verdict.Name.FromPunycode {
			warnings = append(warnings, cli.Warning{
				Code:    "punycode_input",
				Message: fmt.Sprintf("%s was typed as Punycode and decodes to %s; confirm this is the intended name", verdict.Name.ASCII, verdict.Name.Unicode),
			})
		}
		plain = append(plain, strings.Join([]string{
			string(verdict.State), escape(verdict.Name.Unicode), escape(verdict.Name.ASCII), escape(verdict.Source), escape(verdictReason(verdict)),
		}, "\t"))
		display := verdict.Name.Display()
		padding := strings.Repeat(" ", width-len([]rune(display)))
		human = append(human, fmt.Sprintf("%-11s %s%s  %s", verdict.State, display, padding, describe(verdict)))
	}
	if counts[registry.StateUnknown] > 0 {
		warnings = append(warnings, cli.Warning{
			Code:    "availability_incomplete",
			Message: fmt.Sprintf("%d name(s) have no definite verdict; unknown is never free", counts[registry.StateUnknown]),
		})
	}
	data := map[string]any{
		"results":   ordered,
		"complete":  counts[registry.StateUnknown] == 0,
		"registrar": status,
		"summary": map[string]int{
			"free": counts[registry.StateFree], "likely_free": counts[registry.StateLikelyFree],
			"taken": counts[registry.StateTaken], "unknown": counts[registry.StateUnknown],
		},
	}
	if ranked {
		data["words"] = operation.Arguments
	}
	if len(ordered) > 0 && counts[registry.StateUnknown] == len(ordered) {
		return cli.Result{}, cli.AvailabilityUnknown(map[string]any{"results": ordered})
	}
	header := fmt.Sprintf("%d free, %d likely free, %d taken, %d unknown",
		counts[registry.StateFree], counts[registry.StateLikelyFree], counts[registry.StateTaken], counts[registry.StateUnknown])
	if counts[registry.StateLikelyFree] > 0 {
		header += fmt.Sprintf(" (registrar confirmation: %s)", status.Reason)
	}
	return cli.Result{
		Human:    strings.Join(append([]string{header}, human...), "\n"),
		Plain:    plain,
		Data:     data,
		Warnings: warnings,
	}, nil
}

func verdictReason(verdict registry.Verdict) string {
	if verdict.Reason != "" {
		return verdict.Reason
	}
	if verdict.Record != nil && verdict.Record.PendingDelete {
		return "pending_delete"
	}
	if verdict.Registrar != nil && verdict.Registrar.Premium {
		return "premium"
	}
	if verdict.State == registry.StateLikelyFree && verdict.Registrar != nil {
		return verdict.Registrar.Reason
	}
	return ""
}

func describe(verdict registry.Verdict) string {
	parts := []string{verdict.Source}
	if verdict.Record != nil {
		if verdict.Record.PendingDelete {
			parts = append(parts, "pending delete")
		}
		if verdict.Record.FreeDate != "" {
			parts = append(parts, "free-date "+verdict.Record.FreeDate)
		}
	}
	if reason := verdictReason(verdict); reason != "" && reason != "pending_delete" {
		parts = append(parts, reason)
	}
	if verdict.Registrar != nil && verdict.Registrar.Premium {
		parts = append(parts, fmt.Sprintf("premium %s/%s %s", verdict.Registrar.Price, verdict.Registrar.RenewPrice, verdict.Registrar.Currency))
	}
	return strings.Join(parts, ", ")
}

func renderQuote(name domain.Name, price regapi.DomainPrice, caps domain.SpendingCaps, evaluation domain.Evaluation) cli.Result {
	renewal := price.RenewPrice
	if renewal == "" {
		renewal = "unknown"
	}
	warnings := []cli.Warning{}
	if price.RenewPrice == "" {
		warnings = append(warnings, cli.Warning{Code: "renewal_price_unknown", Message: "REG.API returned no renewal price; domain buy will refuse this zone"})
	}
	return cli.Result{
		Human: fmt.Sprintf("%s: first year %s %s, renewal %s %s (caps %s/%s: %s)",
			name.Display(), price.RegPrice, price.Currency, renewal, price.Currency, caps.MaxPrice, caps.MaxRenewal, evaluation.Decision),
		Plain: []string{strings.Join([]string{
			escape(name.Unicode), escape(name.ASCII), escape(price.RegPrice), escape(price.RenewPrice), escape(price.Currency), string(evaluation.Decision),
		}, "\t")},
		Data: map[string]any{
			"name": name, "zone": string(name.Zone), "currency": price.Currency,
			"firstYear": price.RegPrice, "renewal": price.RenewPrice,
			"retailFirstYear": price.RetailRegPrice, "retailRenewal": price.RetailRenewPrice,
			"minPeriod": price.MinPeriod, "caps": caps, "policy": evaluation,
		},
		Warnings: warnings,
	}
}

type reconciled struct {
	evidence Evidence
	state    string
}

func renderPurchase(plan purchasePlan, created regapi.DomainCreateResult, recovered *reconciled) cli.Result {
	data := map[string]any{
		"name": plan.name, "outcome": "committed", "serviceId": created.ServiceID,
		"billId": created.BillID, "payment": created.Payment, "currency": "RUR",
		"quote":       map[string]string{"firstYear": plan.price.RegPrice, "renewal": plan.price.RenewPrice},
		"caps":        plan.caps,
		"policy":      plan.evaluation,
		"nameservers": plan.nameservers,
		"userServid":  plan.userServID,
		"reconciled":  recovered != nil,
		"next":        "registration is an application: the service moves from state N to A; check regru domain list",
	}
	warnings := []cli.Warning{}
	if recovered != nil {
		data["evidence"] = recovered.evidence
		data["serviceState"] = recovered.state
		warnings = append(warnings, cli.Warning{Code: "reconciled", Message: "the order outcome was confirmed by service/get_info after an ambiguous dispatch"})
	}
	if plan.evaluation.ForceOverridden {
		warnings = append(warnings, cli.Warning{Code: "spending_cap_overridden", Message: "--force bought above the configured spending caps"})
	}
	return cli.Result{
		Human: fmt.Sprintf("Ordered %s for %s RUB (renewal %s RUB), service %s, NS %s",
			plan.name.Display(), plan.price.RegPrice, plan.price.RenewPrice, created.ServiceID, strings.Join(plan.nameservers, ", ")),
		Plain: []string{strings.Join([]string{
			"committed", escape(plan.name.Unicode), escape(plan.name.ASCII), escape(created.ServiceID), escape(created.BillID), escape(plan.userServID),
		}, "\t")},
		Data:     data,
		Warnings: warnings,
	}
}

func renderList(services []regapi.ServiceInfo) cli.Result {
	plain := make([]string, 0, len(services))
	human := make([]string, 0, len(services)+1)
	human = append(human, fmt.Sprintf("%d domain(s)", len(services)))
	for _, service := range services {
		display := service.Name
		if name, err := domain.Parse(service.Name); err == nil {
			display = name.Display()
		}
		plain = append(plain, strings.Join([]string{
			escape(service.Name), escape(service.State), escape(service.ExpirationDate), escape(service.ServiceID),
		}, "\t"))
		human = append(human, fmt.Sprintf("%s  state %s  expires %s", display, service.State, service.ExpirationDate))
	}
	return cli.Result{Human: strings.Join(human, "\n"), Plain: plain, Data: map[string]any{"domains": services}}
}

func renderNameservers(name domain.Name, nameservers []string, update regapi.NameserverUpdate) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("Nameservers of %s set to %s; delegation can take up to 24 hours", name.Display(), strings.Join(nameservers, ", ")),
		Plain: []string{strings.Join([]string{escape(name.Unicode), escape(name.ASCII), escape(strings.Join(nameservers, ","))}, "\t")},
		Data:  map[string]any{"name": name, "nameservers": nameservers, "serviceId": update.ServiceID, "outcome": "committed"},
	}
}

func escape(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(value)
}

// confirmNameservers reads domain/get_nss after a successful mutation and
// records whether REG.API now holds exactly the requested set. The read is
// advisory: it never turns a committed mutation into a failure.
func confirmNameservers(ctx context.Context, client *regapi.Client, name domain.Name, requested []string, result *cli.Result) {
	data, ok := result.Data.(map[string]any)
	if !ok {
		return
	}
	actual, err := client.GetNameservers(ctx, name.ASCII)
	if err != nil {
		data["nameserversConfirmed"] = nil
		result.Warnings = append(result.Warnings, cli.Warning{
			Code: "nameservers_unverified", Message: "domain/get_nss could not confirm the nameservers; check them before relying on delegation",
		})
		return
	}
	data["nameserversReported"] = actual
	same := sameHostSet(requested, actual)
	data["nameserversConfirmed"] = same
	if !same {
		result.Warnings = append(result.Warnings, cli.Warning{
			Code: "nameservers_mismatch", Message: "REG.API reports a different nameserver set than requested; see nameserversReported",
		})
	}
}

func sameHostSet(first, second []string) bool {
	normalize := func(values []string) map[string]bool {
		set := make(map[string]bool, len(values))
		for _, value := range values {
			set[strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")] = true
		}
		return set
	}
	left, right := normalize(first), normalize(second)
	if len(left) != len(right) {
		return false
	}
	for host := range left {
		if !right[host] {
			return false
		}
	}
	return true
}
