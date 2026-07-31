package profile

import (
	"fmt"
	"sync"
)

type MemoryRepository struct {
	mu     sync.RWMutex
	config Config
}

func NewMemoryRepository(config Config) *MemoryRepository {
	if config.SchemaVersion == 0 {
		config.SchemaVersion = SchemaVersion
	}
	accounts := make(map[string]Account, len(config.Accounts))
	for name, account := range config.Accounts {
		accounts[name] = account
	}
	config.Accounts = accounts
	return &MemoryRepository{config: config}
}

func (r *MemoryRepository) Load() (Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	config := r.config
	config.Accounts = make(map[string]Account, len(r.config.Accounts))
	for name, account := range r.config.Accounts {
		config.Accounts[name] = account
	}
	return config, nil
}

func (r *MemoryRepository) Add(name string, account Account) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !accountNamePattern.MatchString(name) {
		return Account{}, fmt.Errorf("invalid account name %q", name)
	}
	if _, exists := r.config.Accounts[name]; exists {
		return Account{}, fmt.Errorf("account %q already exists", name)
	}
	if account.ID == "" {
		var err error
		account.ID, err = newAccountID()
		if err != nil {
			return Account{}, err
		}
	}
	if account.Provider == "" {
		account.Provider = "reg.ru"
	}
	r.config.Accounts[name] = account
	return account, nil
}

func (r *MemoryRepository) SetCurrent(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.config.Accounts[name]; !exists {
		return fmt.Errorf("account %q does not exist", name)
	}
	r.config.DefaultAccount = name
	return nil
}

func (r *MemoryRepository) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.config.Accounts[name]; !exists {
		return fmt.Errorf("account %q does not exist", name)
	}
	delete(r.config.Accounts, name)
	if r.config.DefaultAccount == name {
		r.config.DefaultAccount = ""
	}
	return nil
}
