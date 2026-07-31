package s3

import (
	"errors"
	"fmt"
	"time"
)

const (
	DefaultEndpoint      = "https://s3.regru.cloud"
	DefaultSigningRegion = "us-east-1"
)

type Bucket struct {
	Name                   string `json:"name"`
	Size                   int64  `json:"size,omitempty"`
	SizeUnit               string `json:"sizeUnit,omitempty"`
	QuotaGB                *int32 `json:"quotaGb,omitempty"`
	ObjectsCount           int64  `json:"objectsCount"`
	AccessType             string `json:"accessType,omitempty"`
	VersioningEnabled      bool   `json:"isVersioningEnabled"`
	PathStyleLink          string `json:"pathStyleLink,omitempty"`
	VirtualHostedStyleLink string `json:"virtualHostedStyleLink,omitempty"`
}

type KeyPair struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	InstanceID string    `json:"instanceId,omitempty"`
	Server     string    `json:"server,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
}

type ObjectStore struct {
	ServiceID   string    `json:"-"`
	ID          int64     `json:"id"`
	Name        string    `json:"name,omitempty"`
	Status      string    `json:"status,omitempty"`
	Locked      bool      `json:"locked"`
	BucketCount int       `json:"bucketCount"`
	BucketLimit int       `json:"bucketLimit"`
	Size        int64     `json:"size,omitempty"`
	SizeUnit    string    `json:"sizeUnit,omitempty"`
	MaxQuotaGB  int32     `json:"maxQuotaGb,omitempty"`
	QuotaGB     int32     `json:"quotaGb,omitempty"`
	Buckets     []Bucket  `json:"buckets"`
	KeyPairs    []KeyPair `json:"keyPairs"`
}

func (s ObjectStore) Bucket(name string) (Bucket, bool) {
	for _, bucket := range s.Buckets {
		if bucket.Name == name {
			return bucket, true
		}
	}
	return Bucket{}, false
}

type Credentials struct {
	Endpoint  string
	AccessKey []byte
	SecretKey []byte
}

func (c *Credentials) Close() {
	wipe(c.AccessKey)
	wipe(c.SecretKey)
	c.AccessKey = nil
	c.SecretKey = nil
}

type MutationAction string

const (
	MutationBucketCreate      MutationAction = "bucket.create"
	MutationBucketDelete      MutationAction = "bucket.delete"
	MutationBucketPrivacy     MutationAction = "bucket.privacy"
	MutationBucketQuota       MutationAction = "bucket.quota"
	MutationServiceQuota      MutationAction = "service.quota"
	MutationCredentialsCreate MutationAction = "credentials.create"
	MutationCredentialsRevoke MutationAction = "credentials.revoke"
)

type MutationRequest struct {
	Action        MutationAction
	ServiceID     string
	ObjectStoreID int64
	Name          string
	QuotaGB       *int32
	Public        *bool
	KeyPairID     int64
	KeyName       string
}

type MutationResult struct {
	TypeName string
	Bucket   *Bucket
	Store    *ObjectStore
	KeyPair  *KeyPair
}

type PortalErrorKind string

const (
	PortalUnavailable  PortalErrorKind = "unavailable"
	PortalUnauthorized PortalErrorKind = "unauthorized"
	PortalNetwork      PortalErrorKind = "network"
	PortalContract     PortalErrorKind = "contract"
	PortalAmbiguous    PortalErrorKind = "ambiguous"
	PortalDomain       PortalErrorKind = "domain"
)

type PortalError struct {
	Kind PortalErrorKind
	Code string
	Err  error
}

func (e *PortalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("REG.Cloud S3 returned %s", e.Code)
	}
	return "REG.Cloud S3 request failed"
}

func (e *PortalError) Unwrap() error { return e.Err }

type APIError struct {
	StatusCode int
	Code       string
	RequestID  string
	Retryable  bool
	Err        error
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("REG.RU S3 returned %s", e.Code)
	}
	return "REG.RU S3 request failed"
}

func (e *APIError) Unwrap() error { return e.Err }

type ContractError struct{ Err error }

func (e *ContractError) Error() string { return "REG.RU S3 response contract changed" }
func (e *ContractError) Unwrap() error { return e.Err }

type AmbiguousMutationError struct{ Err error }

func (e *AmbiguousMutationError) Error() string { return "S3 mutation outcome is unknown" }
func (e *AmbiguousMutationError) Unwrap() error { return e.Err }

type CORSConfiguration struct {
	Rules []CORSRule `json:"rules"`
}

type CORSRule struct {
	ID             string   `json:"id,omitempty"`
	AllowedMethods []string `json:"allowedMethods"`
	AllowedOrigins []string `json:"allowedOrigins"`
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`
	ExposeHeaders  []string `json:"exposeHeaders,omitempty"`
	MaxAgeSeconds  *int32   `json:"maxAgeSeconds,omitempty"`
}

type LifecycleConfiguration struct {
	Rules []LifecycleRule `json:"rules"`
}

type LifecycleRule struct {
	ID             string `json:"id,omitempty"`
	Status         string `json:"status"`
	Prefix         string `json:"prefix,omitempty"`
	ExpirationDays *int32 `json:"expirationDays,omitempty"`
}

type WebsiteConfiguration struct {
	IndexDocument         string           `json:"indexDocument,omitempty"`
	ErrorDocument         string           `json:"errorDocument,omitempty"`
	RedirectAllRequestsTo *WebsiteRedirect `json:"redirectAllRequestsTo,omitempty"`
}

type WebsiteRedirect struct {
	HostName string `json:"hostName"`
	Protocol string `json:"protocol,omitempty"`
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var errNotFound = errors.New("resource not found")
