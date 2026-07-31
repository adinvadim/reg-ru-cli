package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type DataPlane interface {
	Close()
	GetPolicy(context.Context, string) (json.RawMessage, error)
	PutPolicy(context.Context, string, json.RawMessage) error
	DeletePolicy(context.Context, string) error
	GetCORS(context.Context, string) (CORSConfiguration, error)
	PutCORS(context.Context, string, CORSConfiguration) error
	DeleteCORS(context.Context, string) error
	GetVersioning(context.Context, string) (string, error)
	PutVersioning(context.Context, string, string) error
	GetLifecycle(context.Context, string) (LifecycleConfiguration, error)
	PutLifecycle(context.Context, string, LifecycleConfiguration) error
	DeleteLifecycle(context.Context, string) error
	GetWebsite(context.Context, string) (WebsiteConfiguration, error)
	PutWebsite(context.Context, string, WebsiteConfiguration) error
	DeleteWebsite(context.Context, string) error
}

type DataPlaneOptions struct {
	Endpoint      string
	SigningRegion string
	AccessKey     []byte
	SecretKey     []byte
	HTTPClient    HTTPDoer
}

type DataPlaneFactory interface {
	New(DataPlaneOptions) (DataPlane, error)
}

type AWSDataPlaneFactory struct{}

func (AWSDataPlaneFactory) New(options DataPlaneOptions) (DataPlane, error) {
	return NewAWSDataPlane(options)
}

type byteCredentialsProvider struct {
	access []byte
	secret []byte
}

func (p *byteCredentialsProvider) Retrieve(context.Context) (aws.Credentials, error) {
	if p == nil || len(p.access) == 0 || len(p.secret) == 0 {
		return aws.Credentials{}, errors.New("S3 credentials are unavailable")
	}
	return aws.Credentials{
		AccessKeyID: string(p.access), SecretAccessKey: string(p.secret),
		Source: "regru-in-memory",
	}, nil
}

func (p *byteCredentialsProvider) close() {
	if p == nil {
		return
	}
	wipe(p.access)
	wipe(p.secret)
	p.access = nil
	p.secret = nil
}

type AWSDataPlane struct {
	client   *awss3.Client
	provider *byteCredentialsProvider
}

func NewAWSDataPlane(options DataPlaneOptions) (*AWSDataPlane, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(options.Endpoint), "/")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("invalid S3 endpoint")
	}
	region := strings.TrimSpace(options.SigningRegion)
	if region == "" {
		region = DefaultSigningRegion
	}
	if len(options.AccessKey) == 0 || len(options.SecretKey) == 0 {
		return nil, errors.New("S3 credentials are empty")
	}
	provider := &byteCredentialsProvider{
		access: append([]byte(nil), options.AccessKey...),
		secret: append([]byte(nil), options.SecretKey...),
	}
	config := aws.Config{
		Region: region, Credentials: provider,
		Retryer: func() aws.Retryer { return aws.NopRetryer{} },
	}
	if options.HTTPClient != nil {
		config.HTTPClient = options.HTTPClient
	}
	client := awss3.NewFromConfig(config, func(clientOptions *awss3.Options) {
		clientOptions.BaseEndpoint = aws.String(endpoint)
		clientOptions.UsePathStyle = true
	})
	return &AWSDataPlane{client: client, provider: provider}, nil
}

func (c *AWSDataPlane) Close() {
	if c != nil && c.provider != nil {
		c.provider.close()
	}
}

func (c *AWSDataPlane) GetPolicy(ctx context.Context, bucket string) (json.RawMessage, error) {
	output, err := c.client.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil {
		return nil, translateAWSError(err)
	}
	if output.Policy == nil || !json.Valid([]byte(*output.Policy)) {
		return nil, &ContractError{Err: errors.New("bucket policy is not valid JSON")}
	}
	return json.RawMessage(append([]byte(nil), (*output.Policy)...)), nil
}

func (c *AWSDataPlane) PutPolicy(ctx context.Context, bucket string, policy json.RawMessage) error {
	if len(policy) == 0 || !json.Valid(policy) {
		return errors.New("bucket policy is not valid JSON")
	}
	value := string(policy)
	_, err := c.client.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{
		Bucket: aws.String(bucket), Policy: aws.String(value),
	})
	return translateAWSError(err)
}

func (c *AWSDataPlane) DeletePolicy(ctx context.Context, bucket string) error {
	_, err := c.client.DeleteBucketPolicy(ctx, &awss3.DeleteBucketPolicyInput{Bucket: aws.String(bucket)})
	return translateAWSError(err)
}

func (c *AWSDataPlane) GetCORS(ctx context.Context, bucket string) (CORSConfiguration, error) {
	output, err := c.client.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String(bucket)})
	if err != nil {
		return CORSConfiguration{}, translateAWSError(err)
	}
	configuration := CORSConfiguration{Rules: make([]CORSRule, 0, len(output.CORSRules))}
	for _, rule := range output.CORSRules {
		configuration.Rules = append(configuration.Rules, CORSRule{
			ID: aws.ToString(rule.ID), AllowedMethods: append([]string(nil), rule.AllowedMethods...),
			AllowedOrigins: append([]string(nil), rule.AllowedOrigins...),
			AllowedHeaders: append([]string(nil), rule.AllowedHeaders...),
			ExposeHeaders:  append([]string(nil), rule.ExposeHeaders...),
			MaxAgeSeconds:  rule.MaxAgeSeconds,
		})
	}
	return configuration, nil
}

func (c *AWSDataPlane) PutCORS(ctx context.Context, bucket string, configuration CORSConfiguration) error {
	rules := make([]awstypes.CORSRule, 0, len(configuration.Rules))
	for _, rule := range configuration.Rules {
		if len(rule.AllowedMethods) == 0 || len(rule.AllowedOrigins) == 0 {
			return errors.New("each CORS rule requires allowedMethods and allowedOrigins")
		}
		item := awstypes.CORSRule{
			AllowedMethods: append([]string(nil), rule.AllowedMethods...),
			AllowedOrigins: append([]string(nil), rule.AllowedOrigins...),
			AllowedHeaders: append([]string(nil), rule.AllowedHeaders...),
			ExposeHeaders:  append([]string(nil), rule.ExposeHeaders...),
			MaxAgeSeconds:  rule.MaxAgeSeconds,
		}
		if rule.ID != "" {
			item.ID = aws.String(rule.ID)
		}
		rules = append(rules, item)
	}
	if len(rules) == 0 {
		return errors.New("CORS configuration requires at least one rule")
	}
	_, err := c.client.PutBucketCors(ctx, &awss3.PutBucketCorsInput{
		Bucket: aws.String(bucket), CORSConfiguration: &awstypes.CORSConfiguration{CORSRules: rules},
	})
	return translateAWSError(err)
}

func (c *AWSDataPlane) DeleteCORS(ctx context.Context, bucket string) error {
	_, err := c.client.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{Bucket: aws.String(bucket)})
	return translateAWSError(err)
}

func (c *AWSDataPlane) GetVersioning(ctx context.Context, bucket string) (string, error) {
	output, err := c.client.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
	if err != nil {
		return "", translateAWSError(err)
	}
	status := string(output.Status)
	if status == "" {
		status = "Unversioned"
	}
	return status, nil
}

func (c *AWSDataPlane) PutVersioning(ctx context.Context, bucket, status string) error {
	var normalized awstypes.BucketVersioningStatus
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "enabled":
		normalized = awstypes.BucketVersioningStatusEnabled
	case "suspended":
		normalized = awstypes.BucketVersioningStatusSuspended
	default:
		return errors.New("versioning status must be Enabled or Suspended")
	}
	_, err := c.client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &awstypes.VersioningConfiguration{Status: normalized},
	})
	return translateAWSError(err)
}

func (c *AWSDataPlane) GetLifecycle(ctx context.Context, bucket string) (LifecycleConfiguration, error) {
	output, err := c.client.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return LifecycleConfiguration{}, translateAWSError(err)
	}
	configuration := LifecycleConfiguration{Rules: make([]LifecycleRule, 0, len(output.Rules))}
	for _, rule := range output.Rules {
		item := LifecycleRule{ID: aws.ToString(rule.ID), Status: string(rule.Status)}
		if rule.Filter != nil {
			item.Prefix = aws.ToString(rule.Filter.Prefix)
		} else {
			item.Prefix = aws.ToString(rule.Prefix)
		}
		if rule.Expiration != nil {
			item.ExpirationDays = rule.Expiration.Days
		}
		configuration.Rules = append(configuration.Rules, item)
	}
	return configuration, nil
}

func (c *AWSDataPlane) PutLifecycle(ctx context.Context, bucket string, configuration LifecycleConfiguration) error {
	rules := make([]awstypes.LifecycleRule, 0, len(configuration.Rules))
	for _, rule := range configuration.Rules {
		var status awstypes.ExpirationStatus
		switch strings.ToLower(strings.TrimSpace(rule.Status)) {
		case "enabled":
			status = awstypes.ExpirationStatusEnabled
		case "disabled":
			status = awstypes.ExpirationStatusDisabled
		default:
			return errors.New("lifecycle status must be Enabled or Disabled")
		}
		item := awstypes.LifecycleRule{
			Status: status,
			Filter: &awstypes.LifecycleRuleFilter{Prefix: aws.String(rule.Prefix)},
		}
		if rule.ID != "" {
			item.ID = aws.String(rule.ID)
		}
		if rule.ExpirationDays != nil {
			if *rule.ExpirationDays <= 0 {
				return errors.New("lifecycle expirationDays must be greater than zero")
			}
			item.Expiration = &awstypes.LifecycleExpiration{Days: rule.ExpirationDays}
		}
		rules = append(rules, item)
	}
	if len(rules) == 0 {
		return errors.New("lifecycle configuration requires at least one rule")
	}
	_, err := c.client.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{
		Bucket:                 aws.String(bucket),
		LifecycleConfiguration: &awstypes.BucketLifecycleConfiguration{Rules: rules},
	})
	return translateAWSError(err)
}

func (c *AWSDataPlane) DeleteLifecycle(ctx context.Context, bucket string) error {
	_, err := c.client.DeleteBucketLifecycle(ctx, &awss3.DeleteBucketLifecycleInput{Bucket: aws.String(bucket)})
	return translateAWSError(err)
}

func (c *AWSDataPlane) GetWebsite(ctx context.Context, bucket string) (WebsiteConfiguration, error) {
	output, err := c.client.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{Bucket: aws.String(bucket)})
	if err != nil {
		return WebsiteConfiguration{}, translateAWSError(err)
	}
	configuration := WebsiteConfiguration{}
	if output.IndexDocument != nil {
		configuration.IndexDocument = aws.ToString(output.IndexDocument.Suffix)
	}
	if output.ErrorDocument != nil {
		configuration.ErrorDocument = aws.ToString(output.ErrorDocument.Key)
	}
	if output.RedirectAllRequestsTo != nil {
		configuration.RedirectAllRequestsTo = &WebsiteRedirect{
			HostName: aws.ToString(output.RedirectAllRequestsTo.HostName),
			Protocol: string(output.RedirectAllRequestsTo.Protocol),
		}
	}
	return configuration, nil
}

func (c *AWSDataPlane) PutWebsite(ctx context.Context, bucket string, configuration WebsiteConfiguration) error {
	website := &awstypes.WebsiteConfiguration{}
	if configuration.RedirectAllRequestsTo != nil {
		if configuration.RedirectAllRequestsTo.HostName == "" {
			return errors.New("website redirect requires hostName")
		}
		protocol := awstypes.Protocol(configuration.RedirectAllRequestsTo.Protocol)
		website.RedirectAllRequestsTo = &awstypes.RedirectAllRequestsTo{
			HostName: aws.String(configuration.RedirectAllRequestsTo.HostName), Protocol: protocol,
		}
	} else {
		if configuration.IndexDocument == "" {
			return errors.New("website configuration requires indexDocument")
		}
		website.IndexDocument = &awstypes.IndexDocument{Suffix: aws.String(configuration.IndexDocument)}
		if configuration.ErrorDocument != "" {
			website.ErrorDocument = &awstypes.ErrorDocument{Key: aws.String(configuration.ErrorDocument)}
		}
	}
	_, err := c.client.PutBucketWebsite(ctx, &awss3.PutBucketWebsiteInput{
		Bucket: aws.String(bucket), WebsiteConfiguration: website,
	})
	return translateAWSError(err)
}

func (c *AWSDataPlane) DeleteWebsite(ctx context.Context, bucket string) error {
	_, err := c.client.DeleteBucketWebsite(ctx, &awss3.DeleteBucketWebsiteInput{Bucket: aws.String(bucket)})
	return translateAWSError(err)
}

func translateAWSError(err error) error {
	if err == nil {
		return nil
	}
	var responseErr *smithyhttp.ResponseError
	status := 0
	requestID := ""
	if errors.As(err, &responseErr) {
		status = responseErr.HTTPStatusCode()
		if response := responseErr.HTTPResponse(); response != nil && response.Response != nil {
			requestID = response.Header.Get("x-amz-request-id")
		}
	}
	var apiErr smithy.APIError
	code := ""
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode()
	}
	for _, notFoundCode := range []string{
		"NoSuchBucketPolicy", "NoSuchCORSConfiguration", "NoSuchLifecycleConfiguration", "NoSuchWebsiteConfiguration",
	} {
		if code == notFoundCode {
			return &APIError{
				StatusCode: status, Code: code, RequestID: requestID,
				Err: fmt.Errorf("%w: %s", errNotFound, code),
			}
		}
	}
	return &APIError{
		StatusCode: status, Code: code, RequestID: requestID,
		Retryable: status == http.StatusTooManyRequests || status >= 500,
		Err:       err,
	}
}
