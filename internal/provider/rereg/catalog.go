package rereg

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/domain"
)

// catalogHeader is the observed header of rereg_extlist.csv
// (UTF-8, ";"-separated, CRLF line endings).
var catalogHeader = []string{"Домен", "Ставки", "Текущ.блиц,руб.", "Перв.регистрация", "Дата удаления", "Регистратор"}

const (
	catalogFile  = "rereg_extlist.csv"
	metadataFile = "rereg_extlist.json"
	// minCatalogRows guards against saving a truncated or placeholder file.
	minCatalogRows = 1000
)

// Row is one releasable name from the public catalogue. The file reveals only
// whether bids exist, never their amounts.
type Row struct {
	Name              domain.Name `json:"name"`
	HasBids           bool        `json:"hasBids"`
	BlitzKopecks      int64       `json:"blitzKopecks"`
	FirstRegistration string      `json:"firstRegistration,omitempty"`
	ReleaseDate       string      `json:"releaseDate"`
	Registrar         string      `json:"registrar"`
}

// Metadata describes the stored snapshot.
type Metadata struct {
	FetchedAt    time.Time      `json:"fetchedAt"`
	LastModified string         `json:"lastModified,omitempty"`
	Rows         int            `json:"rows"`
	Skipped      int            `json:"skipped"`
	Bytes        int            `json:"bytes"`
	SHA256       string         `json:"sha256"`
	Zones        map[string]int `json:"zones"`
	ReleaseDates map[string]int `json:"releaseDates"`
	WithBids     int            `json:"withBids"`
}

// ParseCatalog validates the header and decodes every row. Rows whose name is
// not a valid .ru/.рф/.su domain are counted as skipped, never guessed.
func ParseCatalog(body string) ([]Row, int, error) {
	body = strings.TrimPrefix(body, "\ufeff")
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !scanner.Scan() {
		return nil, 0, &Failure{Reason: ReasonDrift}
	}
	header := strings.Split(strings.TrimRight(scanner.Text(), "\r"), ";")
	if len(header) != len(catalogHeader) {
		return nil, 0, &Failure{Reason: ReasonDrift}
	}
	for index, field := range header {
		if strings.TrimSpace(field) != catalogHeader[index] {
			return nil, 0, &Failure{Reason: ReasonDrift}
		}
	}
	rows := []Row{}
	skipped := 0
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		row, ok := parseRow(line)
		if !ok {
			skipped++
			continue
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, &Failure{Reason: ReasonDrift, Cause: err}
	}
	return rows, skipped, nil
}

func parseRow(line string) (Row, bool) {
	fields := strings.Split(line, ";")
	if len(fields) != len(catalogHeader) {
		return Row{}, false
	}
	name, err := domain.Parse(strings.TrimSpace(fields[0]))
	if err != nil || !domain.ReregZone(name.Zone) {
		return Row{}, false
	}
	var hasBids bool
	switch strings.TrimSpace(fields[1]) {
	case "есть":
		hasBids = true
	case "нет":
	default:
		return Row{}, false
	}
	blitz, ok := domain.Kopecks(fields[2])
	if !ok {
		return Row{}, false
	}
	first := strings.TrimSpace(fields[3])
	release := strings.TrimSpace(fields[4])
	if first != "" && !isDate(first) || !isDate(release) {
		return Row{}, false
	}
	return Row{
		Name: name, HasBids: hasBids, BlitzKopecks: blitz, FirstRegistration: first,
		ReleaseDate: release, Registrar: strings.TrimSpace(fields[5]),
	}, true
}

func isDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

// Store keeps the latest catalogue snapshot in a private local directory.
type Store struct{ Dir string }

// ErrNoSnapshot means catalog fetch has not stored a snapshot yet.
var ErrNoSnapshot = errors.New("no backorder catalogue snapshot")

// Save validates and atomically replaces the snapshot.
func (s Store) Save(body string, lastModified string, now time.Time) (Metadata, error) {
	rows, skipped, err := ParseCatalog(body)
	if err != nil {
		return Metadata{}, err
	}
	if len(rows) < minCatalogRows {
		return Metadata{}, &Failure{Reason: ReasonDrift}
	}
	sum := sha256.Sum256([]byte(body))
	meta := Metadata{
		FetchedAt: now.UTC(), LastModified: lastModified, Rows: len(rows), Skipped: skipped,
		Bytes: len(body), SHA256: hex.EncodeToString(sum[:]),
		Zones: map[string]int{}, ReleaseDates: map[string]int{},
	}
	for _, row := range rows {
		meta.Zones[string(row.Name.Zone)]++
		meta.ReleaseDates[row.ReleaseDate]++
		if row.HasBids {
			meta.WithBids++
		}
	}
	if s.Dir == "" {
		return Metadata{}, errors.New("catalogue directory is not configured")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Metadata{}, fmt.Errorf("create catalogue directory: %w", err)
	}
	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Metadata{}, err
	}
	if err := writeAtomic(filepath.Join(s.Dir, catalogFile), []byte(body)); err != nil {
		return Metadata{}, err
	}
	if err := writeAtomic(filepath.Join(s.Dir, metadataFile), encoded); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

// Path is the stored CSV path.
func (s Store) Path() string { return filepath.Join(s.Dir, catalogFile) }

// Load returns the stored snapshot and its metadata.
func (s Store) Load() ([]Row, Metadata, error) {
	if s.Dir == "" {
		return nil, Metadata{}, ErrNoSnapshot
	}
	rawMeta, err := os.ReadFile(filepath.Join(s.Dir, metadataFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, Metadata{}, ErrNoSnapshot
	}
	if err != nil {
		return nil, Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return nil, Metadata{}, ErrNoSnapshot
	}
	body, err := os.ReadFile(filepath.Join(s.Dir, catalogFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, Metadata{}, ErrNoSnapshot
	}
	if err != nil {
		return nil, Metadata{}, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		return nil, Metadata{}, ErrNoSnapshot
	}
	rows, _, err := ParseCatalog(string(body))
	if err != nil {
		return nil, Metadata{}, ErrNoSnapshot
	}
	return rows, meta, nil
}

// Lookup finds one name in the stored snapshot.
func (s Store) Lookup(name domain.Name) (Row, Metadata, bool, error) {
	rows, meta, err := s.Load()
	if err != nil {
		return Row{}, Metadata{}, false, err
	}
	for _, row := range rows {
		if row.Name.ASCII == name.ASCII {
			return row, meta, true, nil
		}
	}
	return Row{}, meta, false, nil
}

func writeAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
