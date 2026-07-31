package secretinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	SchemaVersion  = "regru.secret-input/v1"
	MaxBundleBytes = 64 << 10
	maxCredentials = 128
	maxValueBytes  = 16 << 10
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
	mu     sync.RWMutex
	values map[string][]byte
}

type bundle struct {
	SchemaVersion string            `json:"schemaVersion"`
	Fields        map[string]string `json:"fields"`
}

func Load(reader io.Reader) (*Resolver, error) {
	if reader == nil {
		return nil, errors.New("credential input is unavailable")
	}
	limited := io.LimitReader(reader, MaxBundleBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("could not read credential input")
	}
	if len(data) > MaxBundleBytes {
		zero(data)
		return nil, fmt.Errorf("credential input exceeds %d bytes", MaxBundleBytes)
	}
	defer zero(data)
	if !utf8.Valid(data) {
		return nil, errors.New("credential input is not valid UTF-8")
	}

	if err := rejectDuplicateKeys(data); err != nil {
		return nil, errors.New("credential input contains a duplicate JSON field")
	}
	var input bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, errors.New("credential input is not a valid regru credential bundle")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("credential input must contain exactly one JSON object")
	}
	if input.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported credential input schema %q", input.SchemaVersion)
	}
	if len(input.Fields) > maxCredentials {
		return nil, fmt.Errorf("credential input contains more than %d values", maxCredentials)
	}
	if len(input.Fields) == 0 {
		return nil, errors.New("credential input contains no fields")
	}

	values := make(map[string][]byte, len(input.Fields))
	for key, value := range input.Fields {
		if !credentialKeyPattern.MatchString(key) {
			wipe(values)
			return nil, fmt.Errorf("credential input contains invalid key %q", key)
		}
		if _, allowed := allowedFields[key]; !allowed {
			wipe(values)
			return nil, fmt.Errorf("credential input contains unsupported key %q", key)
		}
		if value == "" {
			wipe(values)
			return nil, fmt.Errorf("credential input value for %q is empty", key)
		}
		if len(value) > maxValueBytes {
			wipe(values)
			return nil, fmt.Errorf("credential input value for %q exceeds %d bytes", key, maxValueBytes)
		}
		if strings.IndexByte(value, 0) >= 0 {
			wipe(values)
			return nil, fmt.Errorf("credential input value for %q contains a NUL byte", key)
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
	return &Resolver{values: values}, nil
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

func (r *Resolver) Resolve(key string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	return append([]byte(nil), value...), ok
}

func (r *Resolver) Keys() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.values))
	for key := range r.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func (r *Resolver) Redact(value string) string {
	if r == nil || value == "" {
		return value
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.values {
		if len(secret) > 0 {
			value = strings.ReplaceAll(value, string(secret), "[REDACTED]")
		}
	}
	return value
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
