package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const maxS3DocumentBytes = 1 << 20

var s3BucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func newS3Command(app *appRuntime) *cobra.Command {
	serviceQuota := 0
	quota := newOperationCommand(app, commandSpec{
		use: "set", short: "Set the REG.RU object-storage quota",
		capability: "s3.control_plane", action: "s3.service.quota.set",
		mutating: true, args: cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("quota-gb", serviceQuota)
		},
	})
	quota.Flags().IntVar(&serviceQuota, "quota-gb", 0, "storage quota in whole GB")
	_ = quota.MarkFlagRequired("quota-gb")
	quota.PreRunE = func(_ *cobra.Command, _ []string) error {
		if serviceQuota <= 0 {
			return UsageError("--quota-gb must be greater than zero")
		}
		return nil
	}
	service := newGroupCommand(
		"service", "Inspect REG.RU object-storage service metadata",
		newOperationCommand(app, readSpec(
			"show", "Show endpoint-independent S3 service metadata", "s3.control_plane",
			"s3.service.show", cobra.NoArgs,
		)),
		newGroupCommand("quota", "Manage the provider storage quota", quota),
	)

	bucket := newGroupCommand(
		"bucket", "Manage S3 bucket lifecycle and configuration",
		newOperationCommand(app, readSpec(
			"list", "List S3 buckets", "s3.control_plane", "s3.bucket.list", cobra.NoArgs,
		)),
		newOperationCommand(app, commandSpec{
			use: "show <name>", aliases: []string{"get"}, short: "Show an S3 bucket",
			capability: "s3.control_plane", action: "s3.bucket.show", args: s3BucketArgs,
		}),
		newS3BucketCreateCommand(app),
		newS3BucketUpdateCommand(app),
		newOperationCommand(app, commandSpec{
			use: "delete <name>", short: "Delete an empty S3 bucket",
			capability: "s3.control_plane", action: "s3.bucket.delete",
			mutating: true, args: s3BucketArgs,
		}),
		newS3ConfigurationGroup(app, "policy"),
		newS3ConfigurationGroup(app, "cors"),
		newS3VersioningGroup(app),
		newS3ConfigurationGroup(app, "lifecycle"),
		newS3ConfigurationGroup(app, "website"),
	)

	keyName := ""
	createKey := newOperationCommand(app, commandSpec{
		use: "create", short: "Create a named S3 key set without exporting its secret",
		capability: "s3.credentials", action: "s3.credentials.create", mutating: true,
		args:       cobra.NoArgs,
		parameters: func() map[string][]string { return operationParameters("name", keyName) },
	})
	createKey.Flags().StringVar(&keyName, "name", "", "provider key-set name")
	_ = createKey.MarkFlagRequired("name")
	createKey.PreRunE = func(_ *cobra.Command, _ []string) error {
		if strings.TrimSpace(keyName) == "" || len(keyName) > 128 {
			return UsageError("--name must contain 1 to 128 characters")
		}
		return nil
	}
	credentials := newGroupCommand(
		"credentials", "Manage S3 access-key lifecycle without printing secrets",
		newOperationCommand(app, readSpec(
			"list", "List S3 key-set metadata", "s3.credentials", "s3.credentials.list", cobra.NoArgs,
		)),
		createKey,
		newOperationCommand(app, mutationSpec(
			"revoke <id>", "Revoke an S3 key set", "s3.credentials",
			"s3.credentials.revoke", positiveDecimalArgs(1),
		)),
	)
	return newGroupCommand("s3", "Manage REG.RU S3 buckets and configuration", service, bucket, credentials)
}

func newS3BucketCreateCommand(app *appRuntime) *cobra.Command {
	quotaGB := 0
	access := "private"
	command := newOperationCommand(app, commandSpec{
		use: "create <name>", short: "Create an S3 bucket through REG.Cloud",
		capability: "s3.control_plane", action: "s3.bucket.create", mutating: true,
		args: s3BucketArgs,
		parameters: func() map[string][]string {
			return operationParameters("quota-gb", quotaGB, "access", access)
		},
	})
	command.Flags().IntVar(&quotaGB, "quota-gb", 0, "optional bucket quota in whole GB")
	command.Flags().StringVar(&access, "access", "private", "provider access mode: private or public")
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		if quotaGB < 0 {
			return UsageError("--quota-gb cannot be negative")
		}
		return validateS3Access(access)
	}
	return command
}

func newS3BucketUpdateCommand(app *appRuntime) *cobra.Command {
	quotaGB := 0
	unlimited := false
	access := ""
	command := newOperationCommand(app, commandSpec{
		use: "update <name>", aliases: []string{"configure"},
		short:      "Update provider quota or public/private mode",
		capability: "s3.control_plane", action: "s3.bucket.update", mutating: true,
		args: s3BucketArgs,
		parameters: func() map[string][]string {
			parameters := operationParameters("quota-gb", quotaGB, "access", access)
			if unlimited {
				parameters["quota-unlimited"] = []string{"true"}
			}
			return parameters
		},
	})
	command.Flags().IntVar(&quotaGB, "quota-gb", 0, "new bucket quota in whole GB")
	command.Flags().BoolVar(&unlimited, "unlimited", false, "remove the per-bucket quota")
	command.Flags().StringVar(&access, "access", "", "new provider access mode: private or public")
	command.PreRunE = func(cmd *cobra.Command, _ []string) error {
		quotaChanged := cmd.Flags().Changed("quota-gb") || unlimited
		accessChanged := cmd.Flags().Changed("access")
		if !quotaChanged && !accessChanged {
			return UsageError("bucket update requires --quota-gb, --unlimited, or --access")
		}
		if unlimited && cmd.Flags().Changed("quota-gb") {
			return UsageError("--quota-gb and --unlimited are mutually exclusive")
		}
		if cmd.Flags().Changed("quota-gb") && quotaGB <= 0 {
			return UsageError("--quota-gb must be greater than zero")
		}
		if accessChanged {
			return validateS3Access(access)
		}
		return nil
	}
	return command
}

func newS3ConfigurationGroup(app *appRuntime, kind string) *cobra.Command {
	capability := "s3.configuration"
	get := newOperationCommand(app, commandSpec{
		use: "get <name>", short: fmt.Sprintf("Show bucket %s configuration", kind),
		capability: capability, action: "s3.bucket." + kind + ".get", args: s3BucketArgs,
	})
	documentPath := ""
	document := ""
	set := newOperationCommand(app, commandSpec{
		use: "set <name>", short: fmt.Sprintf("Replace bucket %s configuration", kind),
		capability: capability, action: "s3.bucket." + kind + ".set",
		mutating: true, args: s3BucketArgs,
		parameters: func() map[string][]string { return operationParameters("document", document) },
	})
	set.Flags().StringVar(&documentPath, "file", "", "JSON configuration file")
	_ = set.MarkFlagRequired("file")
	set.PreRunE = func(_ *cobra.Command, _ []string) error {
		value, err := loadS3Document(documentPath)
		if err != nil {
			return err
		}
		document = value
		return nil
	}
	remove := newOperationCommand(app, commandSpec{
		use: "delete <name>", short: fmt.Sprintf("Delete bucket %s configuration", kind),
		capability: capability, action: "s3.bucket." + kind + ".delete",
		mutating: true, args: s3BucketArgs,
	})
	return newGroupCommand(kind, fmt.Sprintf("Manage bucket %s configuration", kind), get, set, remove)
}

func newS3VersioningGroup(app *appRuntime) *cobra.Command {
	get := newOperationCommand(app, commandSpec{
		use: "get <name>", short: "Show bucket versioning state",
		capability: "s3.configuration", action: "s3.bucket.versioning.get", args: s3BucketArgs,
	})
	status := ""
	set := newOperationCommand(app, commandSpec{
		use: "set <name>", short: "Enable or suspend bucket versioning",
		capability: "s3.configuration", action: "s3.bucket.versioning.set",
		mutating: true, args: s3BucketArgs,
		parameters: func() map[string][]string { return operationParameters("status", status) },
	})
	set.Flags().StringVar(&status, "status", "", "versioning state: enabled or suspended")
	_ = set.MarkFlagRequired("status")
	set.PreRunE = func(_ *cobra.Command, _ []string) error {
		switch strings.ToLower(status) {
		case "enabled", "suspended":
			return nil
		default:
			return UsageError("--status must be enabled or suspended")
		}
	}
	return newGroupCommand("versioning", "Manage bucket versioning", get, set)
}

func s3BucketArgs(command *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(command, args); err != nil {
		return err
	}
	name := args[0]
	if !s3BucketNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return UsageError("bucket name must be 3-63 lowercase letters, digits, dots, or hyphens")
	}
	return nil
}

func validateS3Access(value string) error {
	switch strings.ToLower(value) {
	case "private", "public":
		return nil
	default:
		return UsageError("--access must be private or public")
	}
}

func loadS3Document(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", UsageError("S3 configuration file cannot be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxS3DocumentBytes+1))
	if err != nil || len(data) > maxS3DocumentBytes {
		return "", UsageError("S3 configuration file exceeds 1 MiB or cannot be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return "", UsageError("S3 configuration file must contain one JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", UsageError("S3 configuration file must contain one JSON object")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", UsageError("S3 configuration file is invalid")
	}
	return string(encoded), nil
}
