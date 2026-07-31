package cloudvps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "https://api.cloudvps.reg.ru"
	maxResponseSize = 8 << 20
	defaultPageSize = 100
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientOptions struct {
	BaseURL        string
	HTTPClient     HTTPDoer
	RequestTimeout time.Duration
	Sleep          func(context.Context, time.Duration) error
	Jitter         func(time.Duration) time.Duration
}

type Client struct {
	baseURL        *url.URL
	httpClient     HTTPDoer
	token          []byte
	requestTimeout time.Duration
	sleep          func(context.Context, time.Duration) error
	jitter         func(time.Duration) time.Duration
}

func New(token []byte, options ClientOptions) (*Client, error) {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid CloudVPS API base URL")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	jitter := options.Jitter
	if jitter == nil {
		jitter = defaultJitter
	}
	if len(token) == 0 {
		return nil, errors.New("CloudVPS token is empty")
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	return &Client{
		baseURL:        parsed,
		httpClient:     httpClient,
		token:          append([]byte(nil), token...),
		requestTimeout: requestTimeout,
		sleep:          sleep,
		jitter:         jitter,
	}, nil
}

func (c *Client) Close() {
	for index := range c.token {
		c.token[index] = 0
	}
	c.token = nil
}

func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	var envelope map[string]json.RawMessage
	if err := c.get(ctx, "/v1/reglets", nil, &envelope); err != nil {
		return nil, err
	}
	var servers []Server
	if err := decodeOneOf(envelope, []string{"reglets", "reglet"}, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

func (c *Client) GetServer(ctx context.Context, id string) (Server, error) {
	var envelope map[string]json.RawMessage
	if err := c.get(ctx, "/v1/reglets/"+url.PathEscape(id), nil, &envelope); err != nil {
		return Server{}, err
	}
	var server Server
	if err := decodeOneOf(envelope, []string{"reglet"}, &server); err != nil {
		return Server{}, err
	}
	return server, nil
}

func (c *Client) CreateServer(
	ctx context.Context,
	request CreateServerRequest,
) (Mutation[Server], error) {
	var envelope map[string]json.RawMessage
	if err := c.send(ctx, http.MethodPost, "/v1/reglets", request, &envelope); err != nil {
		return Mutation[Server]{}, err
	}
	var result Mutation[Server]
	if err := decodeOneOf(envelope, []string{"reglet"}, &result.Resource); err != nil {
		return Mutation[Server]{}, err
	}
	actions, err := decodeActions(envelope)
	result.Actions = actions
	return result, err
}

func (c *Client) RenameServer(
	ctx context.Context,
	id string,
	name string,
) (Mutation[Server], error) {
	var envelope map[string]json.RawMessage
	if err := c.send(
		ctx,
		http.MethodPut,
		"/v1/reglets/"+url.PathEscape(id),
		map[string]string{"name": name},
		&envelope,
	); err != nil {
		return Mutation[Server]{}, err
	}
	var result Mutation[Server]
	if err := decodeOneOf(envelope, []string{"reglet"}, &result.Resource); err != nil {
		return Mutation[Server]{}, err
	}
	actions, err := decodeActions(envelope)
	result.Actions = actions
	return result, err
}

func (c *Client) DeleteServer(ctx context.Context, id string) error {
	return c.send(
		ctx,
		http.MethodDelete,
		"/v1/reglets/"+url.PathEscape(id),
		nil,
		nil,
	)
}

func (c *Client) ServerAction(
	ctx context.Context,
	id string,
	request ServerActionRequest,
) (Action, error) {
	var raw json.RawMessage
	if err := c.send(
		ctx,
		http.MethodPost,
		"/v1/reglets/"+url.PathEscape(id)+"/actions",
		request,
		&raw,
	); err != nil {
		return Action{}, err
	}
	return decodeAction(raw)
}

func (c *Client) GetAction(ctx context.Context, id string) (Action, error) {
	var raw json.RawMessage
	if err := c.get(ctx, "/v1/actions/"+url.PathEscape(id), nil, &raw); err != nil {
		return Action{}, err
	}
	action, err := decodeAction(raw)
	if err == nil && action.ID == "" {
		action.ID = ID(id)
	}
	return action, err
}

func (c *Client) ListIPs(
	ctx context.Context,
	serverID string,
) ([]IPAddress, error) {
	query := url.Values{}
	if serverID != "" {
		query.Set("reglet_id", serverID)
	}
	var envelope map[string]json.RawMessage
	if err := c.get(ctx, "/v1/ips", query, &envelope); err != nil {
		return nil, err
	}
	var addresses []IPAddress
	if err := decodeOneOf(envelope, []string{"ips", "snapshots"}, &addresses); err != nil {
		return nil, err
	}
	return addresses, nil
}

func (c *Client) AddIPs(
	ctx context.Context,
	request AddIPsRequest,
) (Mutation[[]IPAddress], error) {
	var envelope map[string]json.RawMessage
	if err := c.send(ctx, http.MethodPost, "/v1/ips", request, &envelope); err != nil {
		return Mutation[[]IPAddress]{}, err
	}
	var result Mutation[[]IPAddress]
	if raw, exists := envelope["ips"]; exists && len(raw) > 0 {
		if err := decodeObjectOrList(raw, &result.Resource); err != nil {
			return Mutation[[]IPAddress]{}, err
		}
	}
	actions, err := decodeActions(envelope)
	result.Actions = actions
	return result, err
}

func (c *Client) UpdatePTR(
	ctx context.Context,
	address string,
	ptr string,
) (IPAddress, error) {
	var envelope map[string]json.RawMessage
	if err := c.send(
		ctx,
		http.MethodPut,
		"/v1/ips/"+url.PathEscape(address),
		map[string]string{"ptr": ptr},
		&envelope,
	); err != nil {
		return IPAddress{}, err
	}
	var result IPAddress
	if err := decodeOneOf(envelope, []string{"ip"}, &result); err != nil {
		return IPAddress{}, err
	}
	if result.Address == "" {
		result.Address = address
	}
	return result, nil
}

func (c *Client) DeleteIP(ctx context.Context, address string) ([]Action, error) {
	var envelope map[string]json.RawMessage
	if err := c.send(
		ctx,
		http.MethodDelete,
		"/v1/ips/"+url.PathEscape(address),
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return decodeActions(envelope)
}

func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	var envelope map[string]json.RawMessage
	if err := c.get(ctx, "/v1/account/keys", nil, &envelope); err != nil {
		return nil, err
	}
	var keys []SSHKey
	if err := decodeOneOf(envelope, []string{"ssh_keys", "ssh_key"}, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

func (c *Client) AddSSHKey(
	ctx context.Context,
	name string,
	publicKey string,
) (SSHKey, error) {
	var envelope map[string]json.RawMessage
	if err := c.send(
		ctx,
		http.MethodPost,
		"/v1/account/keys",
		map[string]string{"name": name, "public_key": publicKey},
		&envelope,
	); err != nil {
		return SSHKey{}, err
	}
	var key SSHKey
	if err := decodeOneOf(envelope, []string{"ssh_keys", "ssh_key"}, &key); err != nil {
		return SSHKey{}, err
	}
	return key, nil
}

func (c *Client) RenameSSHKey(
	ctx context.Context,
	id string,
	name string,
) (SSHKey, error) {
	var envelope map[string]json.RawMessage
	if err := c.send(
		ctx,
		http.MethodPut,
		"/v1/account/keys/"+url.PathEscape(id),
		map[string]string{"name": name},
		&envelope,
	); err != nil {
		return SSHKey{}, err
	}
	var key SSHKey
	if err := decodeOneOf(envelope, []string{"ssh_keys", "ssh_key"}, &key); err != nil {
		return SSHKey{}, err
	}
	return key, nil
}

func (c *Client) DeleteSSHKey(ctx context.Context, id string) error {
	return c.send(
		ctx,
		http.MethodDelete,
		"/v1/account/keys/"+url.PathEscape(id),
		nil,
		nil,
	)
}

func (c *Client) ListSnapshots(
	ctx context.Context,
	region string,
) ([]Snapshot, error) {
	query := url.Values{}
	if region != "" {
		query.Set("region", region)
	}
	var envelope map[string]json.RawMessage
	if err := c.get(ctx, "/v1/snapshots", query, &envelope); err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	if err := decodeOneOf(envelope, []string{"snapshots"}, &snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, id string) error {
	return c.send(
		ctx,
		http.MethodDelete,
		"/v1/snapshots/"+url.PathEscape(id),
		nil,
		nil,
	)
}

func (c *Client) ListPlans(ctx context.Context, query CatalogQuery) ([]Plan, error) {
	if query.Region == "" {
		return nil, errors.New("CloudVPS plan region is required")
	}
	pageSize := normalizedPageSize(query.PageSize)
	var result []Plan
	for page := 1; ; page++ {
		values := catalogValues(query, page, pageSize)
		var response struct {
			Plans    []Plan    `json:"plans"`
			Metadata paginator `json:"metadata"`
		}
		if err := c.get(ctx, "/v2/plans", values, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Plans...)
		more, err := response.Metadata.more(page, pageSize)
		if err != nil {
			return nil, err
		}
		if !more {
			return result, nil
		}
	}
}

func (c *Client) ListImages(ctx context.Context, query CatalogQuery) ([]Image, error) {
	if query.Region == "" {
		return nil, errors.New("CloudVPS image region is required")
	}
	pageSize := normalizedPageSize(query.PageSize)
	var result []Image
	for page := 1; ; page++ {
		values := catalogValues(query, page, pageSize)
		var response struct {
			Images   []Image   `json:"images"`
			Metadata paginator `json:"metadata"`
		}
		if err := c.get(ctx, "/v2/images", values, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Images...)
		more, err := response.Metadata.more(page, pageSize)
		if err != nil {
			return nil, err
		}
		if !more {
			return result, nil
		}
	}
}

type paginator struct {
	Pages struct {
		Current      int `json:"current"`
		ItemsPerPage int `json:"items_per_page"`
		Total        int `json:"total"`
	} `json:"pages"`
	Total int `json:"total"`
}

func (p paginator) more(page, pageSize int) (bool, error) {
	if p.Total == 0 &&
		p.Pages.Current == 1 &&
		p.Pages.ItemsPerPage == pageSize &&
		p.Pages.Total == 0 {
		return false, nil
	}
	if p.Pages.Current != page ||
		p.Pages.ItemsPerPage != pageSize ||
		p.Pages.Total < p.Pages.Current {
		return false, &ContractError{
			Message: "CloudVPS catalog pagination did not make valid forward progress",
		}
	}
	return p.Pages.Current < p.Pages.Total, nil
}

func normalizedPageSize(value int) int {
	if value < 10 || value > 100 {
		return defaultPageSize
	}
	return value
}

func catalogValues(query CatalogQuery, page, pageSize int) url.Values {
	values := url.Values{
		"region":         {query.Region},
		"page":           {strconv.Itoa(page)},
		"items_per_page": {strconv.Itoa(pageSize)},
	}
	if query.PlanLine != "" {
		values.Set("plan_line", query.PlanLine)
	}
	if query.Unit != "" {
		values.Set("unit", query.Unit)
	}
	if query.Memory > 0 {
		values.Set("memory", strconv.Itoa(query.Memory))
	}
	if query.CPUs > 0 {
		values.Set("vcpus", strconv.Itoa(query.CPUs))
	}
	if query.Disk > 0 {
		values.Set("disk", strconv.Itoa(query.Disk))
	}
	if query.ImageType != "" {
		values.Set("type", query.ImageType)
	}
	if query.PrivateImage != nil {
		values.Set("private", strconv.FormatBool(*query.PrivateImage))
	}
	return values
}

func (c *Client) get(
	ctx context.Context,
	path string,
	query url.Values,
	output any,
) error {
	return c.request(ctx, http.MethodGet, path, query, nil, output, true)
}

func (c *Client) send(
	ctx context.Context,
	method string,
	path string,
	input any,
	output any,
) error {
	return c.request(ctx, method, path, nil, input, output, false)
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	input any,
	output any,
	retryable bool,
) error {
	var encoded []byte
	var err error
	if input != nil {
		encoded, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode CloudVPS request: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
		requestURL := *c.baseURL
		requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + path
		requestURL.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(
			requestContext,
			method,
			requestURL.String(),
			bytes.NewReader(encoded),
		)
		if err != nil {
			cancel()
			return fmt.Errorf("build CloudVPS request: %w", err)
		}
		request.Header.Set("Accept", "application/json")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Authorization", "Bearer "+string(c.token))

		response, requestErr := c.httpClient.Do(request)
		if requestErr != nil {
			requestContextErr := requestContext.Err()
			cancel()
			if !retryable {
				return &AmbiguousMutationError{Cause: requestErr}
			}
			if attempt >= 2 || ctx.Err() != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if requestContextErr != nil {
					return requestContextErr
				}
				return requestErr
			}
			if err := c.sleep(ctx, c.jitter(retryDelay(attempt))); err != nil {
				return err
			}
			continue
		}

		body, readErr := readResponse(response.Body)
		response.Body.Close()
		cancel()
		if readErr != nil {
			if !retryable {
				return &AmbiguousMutationError{Cause: readErr}
			}
			return readErr
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if output == nil || len(bytes.TrimSpace(body)) == 0 {
				return nil
			}
			if raw, ok := output.(*json.RawMessage); ok {
				*raw = append((*raw)[:0], body...)
				return nil
			}
			if err := json.Unmarshal(body, output); err != nil {
				return &ContractError{
					Message: "decode CloudVPS documented success response",
				}
			}
			return nil
		}

		apiErr := decodeAPIError(response, body)
		if !retryable && apiErr.StatusCode >= 500 {
			return &AmbiguousMutationError{Cause: apiErr}
		}
		if retryable && attempt < 2 && apiErr.Retryable {
			delay := retryAfter(response.Header.Get("Retry-After"))
			if delay <= 0 {
				delay = c.jitter(retryDelay(attempt))
			}
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		return apiErr
	}
}

func readResponse(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read CloudVPS response: %w", err)
	}
	if len(body) > maxResponseSize {
		return nil, errors.New("CloudVPS response exceeds the safety limit")
	}
	return body, nil
}

func decodeAPIError(response *http.Response, body []byte) *APIError {
	var payload struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	_ = decoder.Decode(&payload)
	code := ""
	switch value := payload.Code.(type) {
	case string:
		code = value
	case json.Number:
		code = value.String()
	}
	message := payload.Message
	if message == "" {
		message = payload.Detail
	}
	return &APIError{
		StatusCode: response.StatusCode,
		Code:       code,
		Message:    message,
		RequestID:  response.Header.Get("X-Request-ID"),
		Retryable:  response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
	}
}

func decodeOneOf(
	envelope map[string]json.RawMessage,
	keys []string,
	output any,
) error {
	for _, key := range keys {
		raw, exists := envelope[key]
		if !exists || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		if err := json.Unmarshal(raw, output); err != nil {
			return &ContractError{
				Message: fmt.Sprintf("decode CloudVPS %s response", key),
			}
		}
		return nil
	}
	return &ContractError{Message: fmt.Sprintf(
		"CloudVPS response is missing %s",
		strings.Join(keys, " or "),
	)}
}

func decodeObjectOrList(raw json.RawMessage, output *[]IPAddress) error {
	if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' {
		return json.Unmarshal(raw, output)
	}
	var object IPAddress
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	if object.Address == "" {
		*output = []IPAddress{}
		return nil
	}
	*output = []IPAddress{object}
	return nil
}

func decodeActions(envelope map[string]json.RawMessage) ([]Action, error) {
	raw, exists := envelope["links"]
	if !exists {
		return nil, nil
	}
	var links struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal(raw, &links); err != nil {
		return nil, &ContractError{
			Message: "decode CloudVPS action links response",
		}
	}
	return links.Actions, nil
}

func decodeAction(raw json.RawMessage) (Action, error) {
	var direct Action
	if err := json.Unmarshal(raw, &direct); err == nil {
		terminal, _ := direct.Terminal()
		if direct.ID != "" || terminal || direct.Pending() {
			return direct, nil
		}
	}
	var envelope struct {
		Action Action `json:"action"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Action{}, &ContractError{Message: "decode CloudVPS action response"}
	}
	if envelope.Action.ID == "" {
		terminal, _ := envelope.Action.Terminal()
		if !terminal && !envelope.Action.Pending() {
			return Action{}, &ContractError{
				Message: "CloudVPS action has an unknown status and no identifier",
			}
		}
	}
	return envelope.Action, nil
}

func retryDelay(attempt int) time.Duration {
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
