package cloudvps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
)

type ExecutorOptions struct {
	BaseURL    string
	HTTPClient HTTPDoer
}

type Executor struct {
	options  ExecutorOptions
	fallback cli.Executor
}

func NewExecutor(options ExecutorOptions, fallback cli.Executor) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	return &Executor{options: options, fallback: fallback}
}

func (e *Executor) Execute(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	if !strings.HasPrefix(operation.Action, "vps.") {
		return e.fallback.Execute(ctx, operation)
	}
	if operation.Credentials == nil {
		return cli.Result{}, cli.ConfigurationError(
			"CloudVPS credentials are not configured",
		)
	}
	token, err := operation.Credentials.Resolve(ctx, "cloudvps.token")
	if err != nil {
		return cli.Result{}, err
	}
	defer wipe(token)

	client, err := New(token, ClientOptions{
		BaseURL:        e.options.BaseURL,
		HTTPClient:     e.options.HTTPClient,
		RequestTimeout: operation.RequestTimeout,
	})
	if err != nil {
		return cli.Result{}, cli.ConfigurationError(
			"CloudVPS client configuration is invalid",
		)
	}
	defer client.Close()

	result, err := e.execute(ctx, client, operation)
	if err != nil {
		return cli.Result{}, translateError(operation, err)
	}
	return result, nil
}

func (e *Executor) execute(
	ctx context.Context,
	client *Client,
	operation cli.Operation,
) (cli.Result, error) {
	switch operation.Action {
	case "vps.list":
		servers, err := client.ListServers(ctx)
		return renderServers(servers), err
	case "vps.show":
		server, err := client.GetServer(ctx, argument(operation, 0))
		return renderServer(server), err
	case "vps.create":
		floatingIP := boolParameter(operation, "floating-ip", true)
		mutation, err := client.CreateServer(ctx, CreateServerRequest{
			Size:       parameter(operation, "size"),
			Image:      scalar(parameter(operation, "image")),
			Name:       parameter(operation, "name"),
			SSHKeys:    scalarList(parameters(operation, "ssh-key")),
			Backups:    boolParameter(operation, "backups", false),
			Region:     parameter(operation, "region"),
			FloatingIP: &floatingIP,
		})
		if err != nil {
			return cli.Result{}, err
		}
		if len(mutation.Actions) == 0 &&
			(mutation.Resource.Status == "new" || mutation.Resource.Status == "ordered") {
			return cli.Result{}, &AmbiguousMutationError{
				Cause: &ContractError{
					Message: "CloudVPS create response is pending without an action identifier",
				},
			}
		}
		mutation.Actions, err = waitActions(
			ctx,
			client,
			mutation.Actions,
			operation.NoWait,
			operation.WaitTimeout,
		)
		return renderServerMutation("created", mutation), err
	case "vps.destroy":
		if err := client.DeleteServer(ctx, argument(operation, 0)); err != nil {
			return cli.Result{}, err
		}
		return renderAcknowledgement("destroyed", argument(operation, 0)), nil
	case "vps.rename":
		mutation, err := client.RenameServer(
			ctx,
			argument(operation, 0),
			parameter(operation, "name"),
		)
		if err != nil {
			return cli.Result{}, err
		}
		mutation.Actions, err = waitActions(
			ctx,
			client,
			mutation.Actions,
			operation.NoWait,
			operation.WaitTimeout,
		)
		return renderServerMutation("renamed", mutation), err
	case "vps.start", "vps.stop", "vps.reboot", "vps.password-reset",
		"vps.backup.enable", "vps.backup.disable":
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type: parameter(operation, "type"),
		})
	case "vps.rebuild":
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type:  "rebuild",
			Image: scalar(parameter(operation, "image")),
		})
	case "vps.resize":
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type: "resize",
			Size: parameter(operation, "size"),
		})
	case "vps.clone":
		offline := boolParameter(operation, "offline", false)
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type:    "clone",
			Name:    parameter(operation, "name"),
			Offline: &offline,
		})
	case "vps.action.show":
		action, err := client.GetAction(ctx, argument(operation, 0))
		return renderAction(action), err
	case "vps.action.wait":
		action, err := waitAction(ctx, client, operation, argument(operation, 0))
		return renderAction(action), err
	case "vps.ip.list", "vps.ips":
		serverID := parameter(operation, "server")
		if operation.Action == "vps.ips" {
			serverID = argument(operation, 0)
		}
		addresses, err := client.ListIPs(ctx, serverID)
		return renderIPs(addresses), err
	case "vps.ip.show":
		return showIP(ctx, client, argument(operation, 0))
	case "vps.ip.add":
		mutation, err := client.AddIPs(ctx, AddIPsRequest{
			ServerID: ID(argument(operation, 0)),
			IPv4:     intParameter(operation, "ipv4"),
			IPv6:     intParameter(operation, "ipv6"),
		})
		if err != nil {
			return cli.Result{}, err
		}
		if len(mutation.Actions) == 0 && len(mutation.Resource) == 0 {
			return cli.Result{}, &AmbiguousMutationError{
				Cause: &ContractError{
					Message: "CloudVPS IP allocation response cannot be reconciled",
				},
			}
		}
		mutation.Actions, err = waitActions(
			ctx,
			client,
			mutation.Actions,
			operation.NoWait,
			operation.WaitTimeout,
		)
		return renderIPMutation("allocated", mutation), err
	case "vps.ip.remove":
		actions, err := client.DeleteIP(ctx, argument(operation, 0))
		if err != nil {
			return cli.Result{}, err
		}
		actions, err = waitActions(
			ctx,
			client,
			actions,
			operation.NoWait,
			operation.WaitTimeout,
		)
		return renderActions("address release accepted", actions), err
	case "vps.ip.ptr":
		address, err := client.UpdatePTR(
			ctx,
			argument(operation, 0),
			parameter(operation, "ptr"),
		)
		return renderIP(address), err
	case "vps.ssh-key.list":
		keys, err := client.ListSSHKeys(ctx)
		return renderSSHKeys(keys), err
	case "vps.ssh-key.add":
		key, err := client.AddSSHKey(
			ctx,
			parameter(operation, "name"),
			parameter(operation, "public-key"),
		)
		return renderSSHKey(key), err
	case "vps.ssh-key.rename":
		key, err := client.RenameSSHKey(
			ctx,
			argument(operation, 0),
			parameter(operation, "name"),
		)
		return renderSSHKey(key), err
	case "vps.ssh-key.remove":
		if err := client.DeleteSSHKey(ctx, argument(operation, 0)); err != nil {
			return cli.Result{}, err
		}
		return renderAcknowledgement("removed", argument(operation, 0)), nil
	case "vps.snapshot.list":
		snapshots, err := client.ListSnapshots(ctx, parameter(operation, "region"))
		return renderSnapshots(snapshots), err
	case "vps.snapshot.create":
		offline := boolParameter(operation, "offline", false)
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type:    "snapshot",
			Name:    parameter(operation, "name"),
			Offline: &offline,
		})
	case "vps.snapshot.remove":
		if err := client.DeleteSnapshot(ctx, argument(operation, 0)); err != nil {
			return cli.Result{}, err
		}
		return renderAcknowledgement("removed", argument(operation, 0)), nil
	case "vps.backup.restore":
		return executeServerAction(ctx, client, operation, ServerActionRequest{
			Type:  "restore",
			Image: scalar(argument(operation, 1)),
		})
	case "vps.plan.list":
		plans, err := client.ListPlans(ctx, catalogQuery(operation))
		return renderPlans(plans), err
	case "vps.plan.show":
		plans, err := client.ListPlans(ctx, catalogQuery(operation))
		if err != nil {
			return cli.Result{}, err
		}
		for _, plan := range plans {
			if plan.Slug == argument(operation, 0) {
				return renderPlan(plan), nil
			}
		}
		return cli.Result{}, &APIError{StatusCode: http.StatusNotFound, Code: "PLAN_NOT_FOUND"}
	case "vps.image.list":
		images, err := client.ListImages(ctx, catalogQuery(operation))
		return renderImages(images), err
	case "vps.image.show":
		images, err := client.ListImages(ctx, catalogQuery(operation))
		if err != nil {
			return cli.Result{}, err
		}
		for _, image := range images {
			if image.Slug == argument(operation, 0) {
				return renderImage(image), nil
			}
		}
		return cli.Result{}, &APIError{StatusCode: http.StatusNotFound, Code: "IMAGE_NOT_FOUND"}
	default:
		return e.fallback.Execute(ctx, operation)
	}
}

func executeServerAction(
	ctx context.Context,
	client *Client,
	operation cli.Operation,
	request ServerActionRequest,
) (cli.Result, error) {
	action, err := client.ServerAction(ctx, argument(operation, 0), request)
	if err != nil {
		return cli.Result{}, err
	}
	terminal, _ := action.Terminal()
	if !terminal && !action.Pending() {
		return cli.Result{}, &ContractError{
			Message: "CloudVPS mutation returned an unknown action status",
		}
	}
	if action.Pending() && action.ID == "" {
		return cli.Result{}, &AmbiguousMutationError{
			Cause: &ContractError{Message: "CloudVPS pending action is missing an identifier"},
		}
	}
	if !operation.NoWait && !terminal {
		action, err = waitAction(ctx, client, operation, string(action.ID))
	}
	return renderAction(action), err
}

func waitActions(
	ctx context.Context,
	client *Client,
	actions []Action,
	noWait bool,
	timeout time.Duration,
) ([]Action, error) {
	if noWait {
		for _, action := range actions {
			if action.Pending() && action.ID == "" {
				return actions, &AmbiguousMutationError{
					Cause: &ContractError{Message: "CloudVPS pending action is missing an identifier"},
				}
			}
		}
		return actions, nil
	}
	waitContext := ctx
	cancel := func() {}
	if len(actions) > 0 {
		waitContext, cancel = waitContextWithTimeout(ctx, timeout)
	}
	defer cancel()
	result := append([]Action(nil), actions...)
	for index, action := range result {
		if action.ID == "" {
			terminal, succeeded := action.Terminal()
			if terminal && succeeded {
				continue
			}
			if terminal {
				return result, &ActionFailedError{Action: action}
			}
			return result, &AmbiguousMutationError{
				Cause: &ContractError{Message: "CloudVPS pending action is missing an identifier"},
			}
		}
		terminal, succeeded := action.Terminal()
		if terminal && succeeded {
			continue
		}
		if terminal {
			return result, &ActionFailedError{Action: action}
		}
		waited, err := client.WaitAction(waitContext, string(action.ID), WaitOptions{})
		result[index] = waited
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func waitAction(
	ctx context.Context,
	client *Client,
	operation cli.Operation,
	id string,
) (Action, error) {
	waitContext := ctx
	cancel := func() {}
	if operation.WaitTimeout > 0 {
		waitContext, cancel = context.WithTimeout(ctx, operation.WaitTimeout)
	}
	defer cancel()
	return client.WaitAction(waitContext, id, WaitOptions{})
}

func waitContextWithTimeout(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func showIP(
	ctx context.Context,
	client *Client,
	address string,
) (cli.Result, error) {
	addresses, err := client.ListIPs(ctx, "")
	if err != nil {
		return cli.Result{}, err
	}
	for _, candidate := range addresses {
		if candidate.Address == address {
			return renderIP(candidate), nil
		}
	}
	return cli.Result{}, &APIError{
		StatusCode: http.StatusNotFound,
		Code:       "IP_NOT_FOUND",
	}
}

func catalogQuery(operation cli.Operation) CatalogQuery {
	query := CatalogQuery{
		Region:    parameter(operation, "region"),
		PageSize:  100,
		PlanLine:  parameter(operation, "plan-line"),
		Unit:      parameter(operation, "unit"),
		Memory:    intParameter(operation, "memory"),
		CPUs:      intParameter(operation, "cpus"),
		Disk:      intParameter(operation, "disk"),
		ImageType: parameter(operation, "type"),
	}
	if boolParameter(operation, "private", false) {
		value := true
		query.PrivateImage = &value
	}
	return query
}

func argument(operation cli.Operation, index int) string {
	if index < 0 || index >= len(operation.Arguments) {
		return ""
	}
	return operation.Arguments[index]
}

func parameter(operation cli.Operation, name string) string {
	values := parameters(operation, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func parameters(operation cli.Operation, name string) []string {
	return append([]string(nil), operation.Parameters[name]...)
}

func intParameter(operation cli.Operation, name string) int {
	value, _ := strconv.Atoi(parameter(operation, name))
	return value
}

func boolParameter(operation cli.Operation, name string, fallback bool) bool {
	value := parameter(operation, name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func translateError(operation cli.Operation, err error) error {
	var cliErr *cli.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	var ambiguousErr *AmbiguousMutationError
	if errors.As(err, &ambiguousErr) {
		return cli.OutcomeUnknown("cloudvps.mutation")
	}
	var contractErr *ContractError
	if errors.As(err, &contractErr) {
		return cli.ProviderContractDrift("CloudVPS")
	}
	var waitErr *WaitError
	if errors.As(err, &waitErr) {
		return cli.ProviderWaitStopped(
			"CloudVPS",
			string(waitErr.Action.ID),
			waitErr.Action.Status,
			waitErr.Cause,
			operation.Action == "vps.action.wait",
		)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusUnauthorized {
			return cli.ProviderAuthenticationError("CloudVPS")
		}
		return cli.ProviderError(
			"CloudVPS",
			apiErr.Code,
			apiErr.StatusCode,
			apiErr.Retryable,
			apiErr.RequestID,
		)
	}
	var actionErr *ActionFailedError
	if errors.As(err, &actionErr) {
		return cli.ProviderActionFailed(
			"CloudVPS",
			string(actionErr.Action.ID),
			actionErr.Action.Status,
		)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return cli.NetworkError("CloudVPS", networkErr.Timeout() || networkErr.Temporary())
	}
	return cli.NetworkError("CloudVPS", false)
}

func renderServers(servers []Server) cli.Result {
	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		lines = append(lines, fmt.Sprintf(
			"%s\t%s\t%s\t%s\t%s",
			plain(string(server.ID)),
			plain(server.Name),
			plain(server.Status),
			plain(server.Region),
			plain(value(server.IP)),
		))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d CloudVPS server(s)", len(servers)),
		Plain: lines,
		Data:  map[string]any{"servers": servers},
	}
}

func renderServer(server Server) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS %s: %s (%s)", server.ID, server.Name, server.Status),
		Plain: []string{fmt.Sprintf(
			"%s\t%s\t%s\t%s\t%s",
			plain(string(server.ID)),
			plain(server.Name),
			plain(server.Status),
			plain(server.Region),
			plain(value(server.IP)),
		)},
		Data: map[string]any{"server": server},
	}
}

func renderServerMutation(status string, mutation Mutation[Server]) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS %s: %s", mutation.Resource.ID, status),
		Plain: []string{fmt.Sprintf(
			"%s\t%s\t%s",
			plain(string(mutation.Resource.ID)),
			plain(status),
			actionState(mutation.Actions),
		)},
		Data: map[string]any{
			"server":  mutation.Resource,
			"actions": nonNilActions(mutation.Actions),
		},
	}
}

func renderAction(action Action) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS action %s: %s", action.ID, action.Status),
		Plain: []string{fmt.Sprintf(
			"%s\t%s\t%s\t%s",
			plain(string(action.ID)),
			plain(action.Status),
			plain(action.Type),
			plain(string(action.ResourceID)),
		)},
		Data: map[string]any{"action": action},
	}
}

func renderActions(message string, actions []Action) cli.Result {
	lines := make([]string, 0, len(actions))
	for _, action := range actions {
		lines = append(lines, fmt.Sprintf(
			"%s\t%s\t%s",
			plain(string(action.ID)),
			plain(action.Status),
			plain(action.Type),
		))
	}
	return cli.Result{
		Human: message,
		Plain: lines,
		Data:  map[string]any{"actions": nonNilActions(actions)},
	}
}

func renderIPs(addresses []IPAddress) cli.Result {
	lines := make([]string, 0, len(addresses))
	for _, address := range addresses {
		lines = append(lines, ipLine(address))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d additional address(es)", len(addresses)),
		Plain: lines,
		Data:  map[string]any{"addresses": addresses},
	}
}

func renderIP(address IPAddress) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS address %s: %s", address.Address, address.Status),
		Plain: []string{ipLine(address)},
		Data:  map[string]any{"address": address},
	}
}

func renderIPMutation(status string, mutation Mutation[[]IPAddress]) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("%d address(es) %s", len(mutation.Resource), status),
		Plain: []string{fmt.Sprintf(
			"%d\t%s\t%s",
			len(mutation.Resource),
			plain(status),
			actionState(mutation.Actions),
		)},
		Data: map[string]any{
			"addresses": mutation.Resource,
			"actions":   nonNilActions(mutation.Actions),
		},
	}
}

func ipLine(address IPAddress) string {
	return fmt.Sprintf(
		"%s\t%s\t%s\t%s\t%s",
		plain(address.Address),
		plain(address.Type),
		plain(address.Status),
		plain(string(address.ServerID)),
		plain(address.PTR),
	)
}

func renderSSHKeys(keys []SSHKey) cli.Result {
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, sshKeyLine(key))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d CloudVPS SSH key(s)", len(keys)),
		Plain: lines,
		Data:  map[string]any{"ssh_keys": keys},
	}
}

func renderSSHKey(key SSHKey) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS SSH key %s: %s", key.ID, key.Name),
		Plain: []string{sshKeyLine(key)},
		Data:  map[string]any{"ssh_key": key},
	}
}

func sshKeyLine(key SSHKey) string {
	return fmt.Sprintf(
		"%s\t%s\t%s",
		plain(string(key.ID)),
		plain(key.Name),
		plain(key.Fingerprint),
	)
}

func renderSnapshots(snapshots []Snapshot) cli.Result {
	lines := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		lines = append(lines, fmt.Sprintf(
			"%s\t%s\t%s\t%s",
			plain(string(snapshot.ID)),
			plain(snapshot.Name),
			plain(snapshot.Region),
			plain(snapshot.CreatedAt),
		))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d CloudVPS snapshot(s)", len(snapshots)),
		Plain: lines,
		Data:  map[string]any{"snapshots": snapshots},
	}
}

func renderPlans(plans []Plan) cli.Result {
	lines := make([]string, 0, len(plans))
	for _, plan := range plans {
		lines = append(lines, planLine(plan))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d CloudVPS plan(s)", len(plans)),
		Plain: lines,
		Data:  map[string]any{"plans": plans},
	}
}

func renderPlan(plan Plan) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS plan %s: %s", plan.Slug, plan.Name),
		Plain: []string{planLine(plan)},
		Data:  map[string]any{"plan": plan},
	}
}

func planLine(plan Plan) string {
	return fmt.Sprintf(
		"%s\t%s\t%d\t%d\t%d\t%d",
		plain(plan.Slug),
		plain(plan.Name),
		plan.CPUs,
		plan.Memory,
		plan.Disk,
		plan.PricePerMonth,
	)
}

func renderImages(images []Image) cli.Result {
	lines := make([]string, 0, len(images))
	for _, image := range images {
		lines = append(lines, imageLine(image))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d CloudVPS image(s)", len(images)),
		Plain: lines,
		Data:  map[string]any{"images": images},
	}
}

func renderImage(image Image) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS image %s: %s", image.Slug, image.Name),
		Plain: []string{imageLine(image)},
		Data:  map[string]any{"image": image},
	}
}

func imageLine(image Image) string {
	return fmt.Sprintf(
		"%s\t%s\t%s\t%s\t%s",
		plain(image.Slug),
		plain(image.Name),
		plain(image.Type),
		plain(image.Region),
		plain(image.Distribution),
	)
}

func renderAcknowledgement(status, id string) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("CloudVPS resource %s: %s", id, status),
		Plain: []string{fmt.Sprintf("%s\t%s", plain(id), plain(status))},
		Data:  map[string]any{"id": id, "status": status},
	}
}

func actionState(actions []Action) string {
	if len(actions) == 0 {
		return ""
	}
	return plain(actions[len(actions)-1].Status)
}

func nonNilActions(actions []Action) []Action {
	if actions == nil {
		return []Action{}
	}
	return actions
}

func plain(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"\t", `\t`,
		"\n", `\n`,
		"\r", `\r`,
	)
	return replacer.Replace(value)
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
