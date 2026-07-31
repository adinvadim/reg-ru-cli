package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

const SchemaVersion = 1

type Repository interface {
	Load() (Config, error)
	Add(string, Account) (Account, error)
	SetCurrent(string) error
	SetPortalSession(string, string) error
	Remove(string) error
}

type Config struct {
	SchemaVersion  int                `toml:"schema_version"`
	DefaultAccount string             `toml:"default_account,omitempty"`
	ProjectAccount string             `toml:"-"`
	Accounts       map[string]Account `toml:"accounts,omitempty"`
}

type Account struct {
	ID                string            `toml:"id" json:"id"`
	Label             string            `toml:"label,omitempty" json:"label,omitempty"`
	Provider          string            `toml:"provider" json:"provider"`
	Portal            Portal            `toml:"portal,omitempty" json:"-"`
	Cloud             Cloud             `toml:"cloud,omitempty" json:"-"`
	Credentials       Credentials       `toml:"credentials,omitempty" json:"-"`
	CredentialProcess CredentialProcess `toml:"credential_process,omitempty" json:"-"`
}

type Portal struct {
	SessionRef string `toml:"session_ref,omitempty" json:"-"`
}

type Cloud struct {
	EnvironmentID   string `toml:"environment_id,omitempty" json:"-"`
	S3Endpoint      string `toml:"s3_endpoint,omitempty" json:"-"`
	S3SigningRegion string `toml:"s3_signing_region,omitempty" json:"-"`
	S3KeyPairID     string `toml:"s3_key_pair_id,omitempty" json:"-"`
}

type Credentials struct {
	CloudVPSRef string `toml:"cloudvps_ref,omitempty" json:"-"`
	REGAPIRef   string `toml:"regapi_ref,omitempty" json:"-"`
	S3Ref       string `toml:"s3_ref,omitempty" json:"-"`
}

type CredentialProcess struct {
	Command []string `toml:"command,omitempty" json:"-"`
}

type FileRepository struct {
	userPath    string
	projectPath string
}

func NewFileRepository(userPath, projectPath string) *FileRepository {
	return &FileRepository{userPath: userPath, projectPath: projectPath}
}

func NewDefaultRepository() *FileRepository {
	var userPath string
	if configDir, err := os.UserConfigDir(); err == nil {
		userPath = filepath.Join(configDir, "regru", "config.toml")
	}
	var projectPath string
	if workingDirectory, err := os.Getwd(); err == nil {
		projectPath = findProjectConfig(workingDirectory)
	}
	return NewFileRepository(userPath, projectPath)
}

func (r *FileRepository) Load() (Config, error) {
	config, err := loadUserFile(r.userPath)
	if err != nil {
		return Config{}, err
	}
	project, err := loadProjectFile(r.projectPath)
	if err != nil {
		return Config{}, err
	}
	config.ProjectAccount = project.Account
	return config, nil
}

var accountNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (r *FileRepository) Add(name string, account Account) (Account, error) {
	if !accountNamePattern.MatchString(name) {
		return Account{}, fmt.Errorf("invalid account name %q", name)
	}
	config, err := loadUserFile(r.userPath)
	if err != nil {
		return Account{}, err
	}
	if _, exists := config.Accounts[name]; exists {
		return Account{}, fmt.Errorf("account %q already exists", name)
	}
	if account.ID == "" {
		account.ID, err = newAccountID()
		if err != nil {
			return Account{}, err
		}
	}
	if account.Provider == "" {
		account.Provider = "reg.ru"
	}
	if err := validateAccount(account); err != nil {
		return Account{}, err
	}
	for _, existing := range config.Accounts {
		if existing.ID == account.ID {
			return Account{}, errors.New("account ID is already in use")
		}
	}
	config.Accounts[name] = account
	if err := r.writeUser(config); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (r *FileRepository) SetCurrent(name string) error {
	config, err := loadUserFile(r.userPath)
	if err != nil {
		return err
	}
	if _, exists := config.Accounts[name]; !exists {
		return fmt.Errorf("account %q does not exist", name)
	}
	config.DefaultAccount = name
	return r.writeUser(config)
}

func (r *FileRepository) SetPortalSession(name, sessionRef string) error {
	config, err := loadUserFile(r.userPath)
	if err != nil {
		return err
	}
	account, exists := config.Accounts[name]
	if !exists {
		return fmt.Errorf("account %q does not exist", name)
	}
	account.Portal.SessionRef = sessionRef
	if err := validateAccount(account); err != nil {
		return err
	}
	config.Accounts[name] = account
	return r.writeUser(config)
}

func (r *FileRepository) Remove(name string) error {
	config, err := loadUserFile(r.userPath)
	if err != nil {
		return err
	}
	if _, exists := config.Accounts[name]; !exists {
		return fmt.Errorf("account %q does not exist", name)
	}
	delete(config.Accounts, name)
	if config.DefaultAccount == name {
		config.DefaultAccount = ""
	}
	return r.writeUser(config)
}

type projectConfig struct {
	SchemaVersion int    `toml:"schema_version"`
	Account       string `toml:"account"`
}

func loadUserFile(path string) (Config, error) {
	config := Config{SchemaVersion: SchemaVersion, Accounts: map[string]Account{}}
	if path == "" {
		return config, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read user profile config: %w", err)
	}
	if err := strictDecode(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode user profile config: %w", err)
	}
	if config.SchemaVersion != SchemaVersion {
		return Config{}, fmt.Errorf("unsupported user profile config schema %d", config.SchemaVersion)
	}
	if config.Accounts == nil {
		config.Accounts = map[string]Account{}
	}
	for alias, account := range config.Accounts {
		if !accountNamePattern.MatchString(alias) {
			return Config{}, fmt.Errorf("invalid account name %q", alias)
		}
		if err := validateAccount(account); err != nil {
			return Config{}, fmt.Errorf("invalid account %q: %w", alias, err)
		}
	}
	if config.DefaultAccount != "" {
		if _, exists := config.Accounts[config.DefaultAccount]; !exists {
			return Config{}, errors.New("default account does not exist")
		}
	}
	return config, nil
}

func loadProjectFile(path string) (projectConfig, error) {
	if path == "" {
		return projectConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return projectConfig{}, nil
	}
	if err != nil {
		return projectConfig{}, fmt.Errorf("read project profile config: %w", err)
	}
	var config projectConfig
	if err := strictDecode(data, &config); err != nil {
		return projectConfig{}, fmt.Errorf("decode project profile config: %w", err)
	}
	if config.SchemaVersion != SchemaVersion {
		return projectConfig{}, fmt.Errorf("unsupported project profile config schema %d", config.SchemaVersion)
	}
	if config.Account != "" && !accountNamePattern.MatchString(config.Account) {
		return projectConfig{}, fmt.Errorf("invalid project account name %q", config.Account)
	}
	return config, nil
}

func strictDecode(data []byte, destination any) error {
	return toml.NewDecoder(bytes.NewReader(data)).
		DisallowUnknownFields().
		Decode(destination)
}

func (r *FileRepository) writeUser(config Config) error {
	if r.userPath == "" {
		return errors.New("user profile config path is not configured")
	}
	config.SchemaVersion = SchemaVersion
	config.ProjectAccount = ""
	if config.Accounts == nil {
		config.Accounts = map[string]Account{}
	}
	data, err := toml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode user profile config: %w", err)
	}

	parent := filepath.Dir(r.userPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create profile config directory: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("secure profile config directory: %w", err)
	}
	temp, err := os.CreateTemp(parent, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary profile config: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary profile config: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary profile config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary profile config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary profile config: %w", err)
	}
	if err := os.Rename(tempPath, r.userPath); err != nil {
		return fmt.Errorf("replace user profile config: %w", err)
	}
	return nil
}

func validateAccount(account Account) error {
	if !strings.HasPrefix(account.ID, "p_") || len(account.ID) != 28 {
		return errors.New("account ID is invalid")
	}
	if account.Provider != "reg.ru" {
		return errors.New("account provider is unsupported")
	}
	for name, value := range map[string]string{
		"portal session reference": account.Portal.SessionRef,
		"cloud environment ID":     account.Cloud.EnvironmentID,
		"S3 endpoint":              account.Cloud.S3Endpoint,
		"S3 signing region":        account.Cloud.S3SigningRegion,
		"S3 key-pair ID":           account.Cloud.S3KeyPairID,
		"CloudVPS reference":       account.Credentials.CloudVPSRef,
		"REG.API reference":        account.Credentials.REGAPIRef,
		"S3 reference":             account.Credentials.S3Ref,
	} {
		if err := validateOpaque(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := validateCredentialProcess(account.CredentialProcess); err != nil {
		return err
	}
	return nil
}

func validateCredentialProcess(process CredentialProcess) error {
	if len(process.Command) == 0 {
		return nil
	}
	if len(process.Command) > 64 {
		return errors.New("credential process has too many arguments")
	}
	total := 0
	for _, argument := range process.Command {
		total += len(argument)
		if argument == "" || len(argument) > 4096 || !utf8.ValidString(argument) {
			return errors.New("credential process argument is invalid")
		}
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("credential process argument contains a NUL byte")
		}
	}
	if total > 16<<10 {
		return errors.New("credential process command is too large")
	}
	return nil
}

func validateOpaque(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 1024 || !utf8.ValidString(value) {
		return errors.New("opaque value is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("opaque value contains a control character")
		}
	}
	return nil
}

func newAccountID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("generate account ID")
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString(random[:])
	return "p_" + strings.ToLower(encoded), nil
}

func findProjectConfig(start string) string {
	directory := start
	for {
		candidate := filepath.Join(directory, ".regru", "config.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return filepath.Join(start, ".regru", "config.toml")
		}
		directory = parent
	}
}
