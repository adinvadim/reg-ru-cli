package credentialprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion  = "regru.credential-process/v1"
	MaxBundleBytes = 64 << 10
	maxCredentials = 128
	maxValueBytes  = 16 << 10
	defaultTimeout = 30 * time.Second
)

var credentialKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

var allowedFields = map[string]struct{}{
	"portal.login":         {},
	"portal.password":      {},
	"regapi.username":      {},
	"regapi.password":      {},
	"cloudvps.token":       {},
	"s3.access_key_id":     {},
	"s3.secret_access_key": {},
}

type Resolver struct {
	mu      sync.RWMutex
	command []string
	timeout time.Duration
	loaded  bool
	loadErr error
	values  map[string][]byte
}

type bundle struct {
	SchemaVersion string            `json:"schemaVersion"`
	Fields        map[string]string `json:"fields"`
}

type ProcessError struct {
	Code string
}

type readResult struct {
	data []byte
	err  error
}

func (e *ProcessError) Error() string {
	return e.Code
}

func New(command []string, timeout time.Duration) *Resolver {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Resolver{
		command: append([]string(nil), command...),
		timeout: timeout,
	}
}

func parseEnvelope(reader io.Reader) (map[string][]byte, error) {
	if reader == nil {
		return nil, errors.New("credential process output is unavailable")
	}
	limited := io.LimitReader(reader, MaxBundleBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("could not read credential process output")
	}
	if len(data) > MaxBundleBytes {
		zero(data)
		return nil, fmt.Errorf("credential process output exceeds %d bytes", MaxBundleBytes)
	}
	defer zero(data)
	if !utf8.Valid(data) {
		return nil, errors.New("credential process output is not valid UTF-8")
	}

	if err := rejectDuplicateKeys(data); err != nil {
		return nil, errors.New("credential process output contains a duplicate JSON field")
	}
	var input bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, errors.New("credential process output is not a valid regru credential bundle")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("credential process output must contain exactly one JSON object")
	}
	if input.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported credential process schema %q", input.SchemaVersion)
	}
	if len(input.Fields) > maxCredentials {
		return nil, fmt.Errorf("credential process output contains more than %d values", maxCredentials)
	}
	if len(input.Fields) == 0 {
		return nil, errors.New("credential process output contains no fields")
	}

	values := make(map[string][]byte, len(input.Fields))
	for key, value := range input.Fields {
		if !credentialKeyPattern.MatchString(key) {
			wipe(values)
			return nil, fmt.Errorf("credential process output contains invalid key %q", key)
		}
		if _, allowed := allowedFields[key]; !allowed {
			wipe(values)
			return nil, fmt.Errorf("credential process output contains unsupported key %q", key)
		}
		if value == "" {
			wipe(values)
			return nil, fmt.Errorf("credential process value for %q is empty", key)
		}
		if len(value) > maxValueBytes {
			wipe(values)
			return nil, fmt.Errorf("credential process value for %q exceeds %d bytes", key, maxValueBytes)
		}
		if strings.IndexByte(value, 0) >= 0 {
			wipe(values)
			return nil, fmt.Errorf("credential process value for %q contains a NUL byte", key)
		}
		values[key] = []byte(value)
	}
	for _, pair := range [][2]string{
		{"portal.login", "portal.password"},
		{"regapi.username", "regapi.password"},
		{"s3.access_key_id", "s3.secret_access_key"},
	} {
		_, first := values[pair[0]]
		_, second := values[pair[1]]
		if first != second {
			wipe(values)
			return nil, fmt.Errorf(
				"credential fields %q and %q must be provided together",
				pair[0],
				pair[1],
			)
		}
	}
	return values, nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func (r *Resolver) Resolve(ctx context.Context, key string) ([]byte, error) {
	if r == nil {
		return nil, &ProcessError{Code: "credential_process_not_configured"}
	}
	if err := r.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return nil, &ProcessError{Code: "credential_field_unavailable"}
	}
	return append([]byte(nil), value...), nil
}

func (r *Resolver) ensureLoaded(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.loadErr
	}
	r.loaded = true
	if len(r.command) == 0 {
		r.loadErr = &ProcessError{Code: "credential_process_not_configured"}
		return r.loadErr
	}

	timeoutContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	command := exec.CommandContext(
		timeoutContext,
		r.command[0],
		r.command[1:]...,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		r.loadErr = &ProcessError{Code: "credential_process_failed"}
		return r.loadErr
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		switch timeoutContext.Err() {
		case context.Canceled:
			r.loadErr = &ProcessError{Code: "credential_process_cancelled"}
		case context.DeadlineExceeded:
			r.loadErr = &ProcessError{Code: "credential_process_timeout"}
		default:
			r.loadErr = &ProcessError{Code: "credential_process_failed"}
		}
		return r.loadErr
	}
	readDone := make(chan readResult, 1)
	go func() {
		data, readErr := io.ReadAll(io.LimitReader(stdout, MaxBundleBytes+1))
		readDone <- readResult{data: data, err: readErr}
	}()

	var output readResult
	select {
	case output = <-readDone:
	case <-timeoutContext.Done():
		_ = stdout.Close()
		_ = command.Process.Kill()
		output = <-readDone
		_ = command.Wait()
		zero(output.data)
		if errors.Is(timeoutContext.Err(), context.DeadlineExceeded) {
			r.loadErr = &ProcessError{Code: "credential_process_timeout"}
		} else {
			r.loadErr = &ProcessError{Code: "credential_process_cancelled"}
		}
		return r.loadErr
	}

	data := output.data
	if len(data) > MaxBundleBytes {
		_ = stdout.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		zero(data)
		r.loadErr = &ProcessError{Code: "credential_process_output_too_large"}
		return r.loadErr
	}
	waitErr := command.Wait()
	if output.err != nil || waitErr != nil {
		zero(data)
		switch timeoutContext.Err() {
		case context.Canceled:
			r.loadErr = &ProcessError{Code: "credential_process_cancelled"}
		case context.DeadlineExceeded:
			r.loadErr = &ProcessError{Code: "credential_process_timeout"}
		default:
			r.loadErr = &ProcessError{Code: "credential_process_failed"}
		}
		return r.loadErr
	}
	values, err := parseEnvelope(bytes.NewReader(data))
	zero(data)
	if err != nil {
		r.loadErr = &ProcessError{Code: "credential_process_invalid_output"}
		return r.loadErr
	}
	r.values = values
	return nil
}

func (r *Resolver) Contains(value string) bool {
	if r == nil || value == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.values {
		if len(secret) > 0 && strings.Contains(value, string(secret)) {
			return true
		}
	}
	return false
}

func (r *Resolver) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wipe(r.values)
	r.values = nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}

func wipe(values map[string][]byte) {
	for _, value := range values {
		zero(value)
	}
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
