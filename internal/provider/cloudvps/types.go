package cloudvps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ID preserves provider identifiers without assuming that they remain numeric.
// CloudVPS currently returns both integer IDs and string task IDs.
type ID string

func (id *ID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		*id = ""
		return nil
	}
	var value string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	} else {
		var number json.Number
		if err := json.Unmarshal(data, &number); err != nil {
			return fmt.Errorf("decode provider identifier: %w", err)
		}
		value = number.String()
	}
	*id = ID(value)
	return nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(id))
}

type Boolish bool

func (value *Boolish) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true", "1", `"1"`:
		*value = true
		return nil
	case "false", "0", `"0"`:
		*value = false
		return nil
	default:
		return fmt.Errorf("decode provider boolean")
	}
}

func (value Boolish) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(value))
}

type Server struct {
	ID              ID      `json:"id"`
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	SubStatus       *string `json:"sub_status,omitempty"`
	Region          string  `json:"region_slug"`
	SizeSlug        string  `json:"size_slug"`
	ImageID         ID      `json:"image_id"`
	IP              *string `json:"ip,omitempty"`
	IPv6            *string `json:"ipv6,omitempty"`
	PTR             *string `json:"ptr,omitempty"`
	BackupsEnabled  Boolish `json:"backups_enabled"`
	LastBackupDate  *string `json:"last_backup_date,omitempty"`
	CreatedAt       string  `json:"created_at"`
	CPUs            int     `json:"vcpus"`
	Memory          int     `json:"memory"`
	Disk            int     `json:"disk"`
	DiskUsage       float64 `json:"disk_usage,omitempty"`
	ServiceID       ID      `json:"service_id"`
	PrivateNetworks []ID    `json:"vpcs,omitempty"`
}

type CreateServerRequest struct {
	Size       string `json:"size"`
	Image      any    `json:"image"`
	Name       string `json:"name,omitempty"`
	SSHKeys    []any  `json:"ssh_keys,omitempty"`
	Backups    bool   `json:"backups,omitempty"`
	Region     string `json:"region_slug,omitempty"`
	FloatingIP *bool  `json:"floating_ip,omitempty"`
	PromoCode  string `json:"promocode,omitempty"`
}

type ServerActionRequest struct {
	Type    string `json:"type"`
	Image   any    `json:"image,omitempty"`
	Size    string `json:"size,omitempty"`
	Name    string `json:"name,omitempty"`
	Offline *bool  `json:"offline,omitempty"`
	SSHKeys []any  `json:"ssh_keys,omitempty"`
}

type Action struct {
	ID           ID      `json:"id"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	ResourceID   ID      `json:"resource_id"`
	ResourceType string  `json:"resource_type"`
	Region       string  `json:"region_slug"`
	CreatedAt    string  `json:"created_at"`
	CompletedAt  *string `json:"completed_at,omitempty"`
}

func (action *Action) UnmarshalJSON(data []byte) error {
	type wireAction Action
	var wire struct {
		wireAction
		ActionID ID `json:"action_id"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*action = Action(wire.wireAction)
	if action.ID == "" {
		action.ID = wire.ActionID
	}
	action.Status = NormalizeActionStatus(action.Status)
	return nil
}

func NormalizeActionStatus(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
}

func (action Action) Terminal() (terminal bool, succeeded bool) {
	switch NormalizeActionStatus(action.Status) {
	case "completed":
		return true, true
	case "failed", "errored":
		return true, false
	default:
		return false, false
	}
}

func (action Action) Pending() bool {
	switch NormalizeActionStatus(action.Status) {
	case "wait", "new", "in-progress":
		return true
	default:
		return false
	}
}

type IPAddress struct {
	ID        ID     `json:"id"`
	Address   string `json:"ip"`
	PTR       string `json:"ptr"`
	Region    string `json:"region_slug"`
	ServerID  ID     `json:"reglet_id"`
	Status    string `json:"status"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
}

type AddIPsRequest struct {
	ServerID ID  `json:"reglet_id"`
	IPv4     int `json:"ipv4_count,omitempty"`
	IPv6     int `json:"ipv6_count,omitempty"`
}

type SSHKey struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key,omitempty"`
	ServiceID   ID     `json:"service_id"`
}

type Snapshot struct {
	ID           ID      `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	Type         string  `json:"type"`
	Region       string  `json:"region_slug"`
	Distribution string  `json:"distribution"`
	Private      Boolish `json:"private"`
	CreatedAt    string  `json:"created_at"`
	MinDiskSize  string  `json:"min_disk_size"`
	SizeGB       string  `json:"size_gigabytes"`
}

type Plan struct {
	ID            ID     `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	PlanLine      string `json:"plan_line"`
	Unit          string `json:"unit"`
	CPUs          int    `json:"vcpus"`
	Memory        int    `json:"memory"`
	Disk          int    `json:"disk"`
	VideoCards    int    `json:"videocards"`
	PricePerHour  string `json:"price_per_hour"`
	PricePerMonth int    `json:"price_per_month"`
}

type Image struct {
	ID             ID      `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Region         string  `json:"region_slug"`
	Distribution   string  `json:"distribution"`
	Private        Boolish `json:"private"`
	CreatedAt      string  `json:"created_at"`
	MinDiskSize    string  `json:"min_disk_size"`
	SizeGB         string  `json:"size_gigabytes"`
	ISPLicenseSize *string `json:"isp_license_size,omitempty"`
}

type CatalogQuery struct {
	Region       string
	PageSize     int
	PlanLine     string
	Unit         string
	Memory       int
	CPUs         int
	Disk         int
	ImageType    string
	PrivateImage *bool
}

type Mutation[T any] struct {
	Resource T        `json:"resource"`
	Actions  []Action `json:"actions"`
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	Retryable  bool
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return fmt.Sprintf("CloudVPS API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("CloudVPS API returned %s (HTTP %d)", e.Code, e.StatusCode)
}

type ActionFailedError struct {
	Action Action
}

type ContractError struct {
	Message string
}

func (e *ContractError) Error() string {
	if e == nil || e.Message == "" {
		return "CloudVPS response does not match the documented contract"
	}
	return e.Message
}

type AmbiguousMutationError struct {
	Cause error
}

type WaitError struct {
	Action Action
	Cause  error
}

func (e *WaitError) Error() string {
	return "CloudVPS action wait stopped before a terminal result"
}

func (e *WaitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AmbiguousMutationError) Error() string {
	return "CloudVPS mutation result is ambiguous"
}

func (e *AmbiguousMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ActionFailedError) Error() string {
	return fmt.Sprintf("CloudVPS action %s ended with status %s", e.Action.ID, e.Action.Status)
}

func scalar(value string) any {
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return number
	}
	return value
}

func scalarList(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, scalar(value))
		}
	}
	return result
}
