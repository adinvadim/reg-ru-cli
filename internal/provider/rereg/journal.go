package rereg

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

// Journal states. A dispatched bid whose last state is intent or
// outcome_unknown counts toward the daily cap like a confirmed one.
const (
	StateIntent    = "intent"
	StateConfirmed = "confirmed"
	StateRejected  = "rejected"
	StateUnknown   = "outcome_unknown"
)

// ErrJournalBusy means another regru process holds the bid lock.
var ErrJournalBusy = errors.New("another backorder bid is in progress for this account")

var journalIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Entry is one append-only journal line. The journal stores no contacts,
// credentials, or provider responses.
type Entry struct {
	ID            string `json:"id"`
	Domain        string `json:"domain"`
	AmountKopecks int64  `json:"amountKopecks"`
	State         string `json:"state"`
	Day           string `json:"day"`
	At            string `json:"at"`
	BillID        string `json:"billId,omitempty"`
}

// Journal records every dispatched bid of one account profile so the daily
// cap can be evaluated locally across runs.
type Journal struct {
	Dir       string
	ProfileID string
}

// Lock is an exclusive per-account bid lock held from the cap check until the
// outcome is journaled.
type Lock struct{ file *os.File }

// Release drops the lock.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockFile(l.file)
	_ = l.file.Close()
	l.file = nil
}

func (j Journal) path(suffix string) (string, error) {
	if j.Dir == "" || j.ProfileID == "" {
		return "", errors.New("bid journal is not configured")
	}
	if err := os.MkdirAll(j.Dir, 0o700); err != nil {
		return "", fmt.Errorf("create bid journal directory: %w", err)
	}
	return filepath.Join(j.Dir, j.ProfileID+suffix), nil
}

// Lock takes the per-account bid lock without waiting.
func (j Journal) Lock() (*Lock, error) {
	path, err := j.path(".lock")
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open bid lock: %w", err)
	}
	if err := tryLockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

// NewID returns a random journal ID.
func NewID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// Append durably writes one entry.
func (j Journal) Append(entry Entry) error {
	if !journalIDPattern.MatchString(entry.ID) || entry.State == "" || entry.Day == "" {
		return errors.New("invalid bid journal entry")
	}
	path, err := j.path(".jsonl")
	if err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open bid journal: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write bid journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync bid journal: %w", err)
	}
	return file.Close()
}

// Spend is the money committed on one Moscow day according to the journal.
type Spend struct {
	Day           string `json:"day"`
	AmountKopecks int64  `json:"amountKopecks"`
	Bids          int    `json:"bids"`
}

// DailySpend sums the latest state of every bid journaled on day (MSK):
// intent, confirmed, and outcome_unknown count; rejected does not.
func (j Journal) DailySpend(day string) (Spend, error) {
	spend := Spend{Day: day}
	path, err := j.path(".jsonl")
	if err != nil {
		return spend, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return spend, nil
	}
	if err != nil {
		return spend, fmt.Errorf("read bid journal: %w", err)
	}
	defer file.Close()
	latest := map[string]Entry{}
	order := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || !journalIDPattern.MatchString(entry.ID) {
			return spend, errors.New("the bid journal is unreadable; it is never rewritten automatically")
		}
		if _, seen := latest[entry.ID]; !seen {
			order = append(order, entry.ID)
		} else {
			entry.Day = latest[entry.ID].Day
		}
		latest[entry.ID] = entry
	}
	if err := scanner.Err(); err != nil {
		return spend, fmt.Errorf("read bid journal: %w", err)
	}
	for _, id := range order {
		entry := latest[id]
		if entry.Day != day || entry.State == StateRejected {
			continue
		}
		spend.AmountKopecks += entry.AmountKopecks
		spend.Bids++
	}
	return spend, nil
}

// Today is the journal day key for now.
func Today(now time.Time) string { return domain.MSKDate(now) }
