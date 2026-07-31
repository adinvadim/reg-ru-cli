package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	storeSchemaVersion = 1
	metadataFileName   = "session.json"
	maxMetadataBytes   = 16 << 10
)

var (
	profileIDPattern  = regexp.MustCompile(`^p_[a-z2-7]{26}$`)
	sessionRefPattern = regexp.MustCompile(`^s_[a-z2-7]{26}$`)
	errProfileBusy    = errors.New("portal profile is busy")
)

type Record struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ProfileID      string `json:"profileId"`
	SessionRef     string `json:"sessionRef"`
	State          State  `json:"state"`
	IdentityKey    []byte `json:"identityKey"`
	IdentityDigest []byte `json:"identityDigest,omitempty"`
	ProfileDir     string `json:"-"`
}

type FileStore struct {
	root string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: root}
}

type profileLock struct {
	once sync.Once
	file *os.File
	err  error
}

func (s *FileStore) LockProfile(profileID string) (*profileLock, error) {
	if !profileIDPattern.MatchString(profileID) {
		return nil, errors.New("portal profile ID is invalid")
	}
	if err := secureDirectory(s.root); err != nil {
		return nil, err
	}
	lockRoot := filepath.Join(s.root, ".locks")
	if err := secureDirectory(lockRoot); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(
		filepath.Join(lockRoot, profileID+".lock"),
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open portal profile lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure portal profile lock: %w", err)
	}
	if err := tryLockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &profileLock{file: file}, nil
}

func (l *profileLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.once.Do(func() {
		if err := unlockFile(l.file); err != nil {
			l.err = err
		}
		if err := l.file.Close(); l.err == nil && err != nil {
			l.err = err
		}
	})
	return l.err
}

func (s *FileStore) Stage(profileID, currentRef string) (Record, error) {
	if !profileIDPattern.MatchString(profileID) {
		return Record{}, errors.New("portal profile ID is invalid")
	}
	if err := secureDirectory(s.root); err != nil {
		return Record{}, err
	}

	var identityKey []byte
	var identityDigest []byte
	if currentRef != "" {
		current, err := s.Load(profileID, currentRef)
		if err != nil {
			return Record{}, err
		}
		identityKey = append([]byte(nil), current.IdentityKey...)
		identityDigest = append([]byte(nil), current.IdentityDigest...)
	} else {
		identityKey = make([]byte, identityKeyBytes)
		if _, err := rand.Read(identityKey); err != nil {
			return Record{}, errors.New("generate portal identity key")
		}
	}

	for attempt := 0; attempt < 8; attempt++ {
		ref, err := newSessionRef()
		if err != nil {
			return Record{}, err
		}
		directory, err := s.directory(ref)
		if err != nil {
			return Record{}, err
		}
		if err := os.Mkdir(directory, 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return Record{}, fmt.Errorf("create portal session directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			_ = os.Remove(directory)
			return Record{}, fmt.Errorf("secure portal session directory: %w", err)
		}
		browserDir := filepath.Join(directory, "chrome")
		if err := os.Mkdir(browserDir, 0o700); err != nil {
			_ = os.RemoveAll(directory)
			return Record{}, fmt.Errorf("create browser profile directory: %w", err)
		}
		record := Record{
			SchemaVersion:  storeSchemaVersion,
			ProfileID:      profileID,
			SessionRef:     ref,
			State:          StateStaged,
			IdentityKey:    identityKey,
			IdentityDigest: identityDigest,
			ProfileDir:     browserDir,
		}
		if err := s.write(record); err != nil {
			_ = os.RemoveAll(directory)
			return Record{}, err
		}
		return record, nil
	}
	return Record{}, errors.New("could not allocate a portal session reference")
}

func (s *FileStore) Load(profileID, ref string) (Record, error) {
	if !profileIDPattern.MatchString(profileID) {
		return Record{}, errors.New("portal profile ID is invalid")
	}
	directory, err := s.directory(ref)
	if err != nil {
		return Record{}, err
	}
	file, err := os.Open(filepath.Join(directory, metadataFileName))
	if err != nil {
		return Record{}, fmt.Errorf("open portal session metadata: %w", err)
	}
	defer file.Close()

	var record Record
	decoder := json.NewDecoder(io.LimitReader(file, maxMetadataBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, errors.New("portal session metadata is invalid")
	}
	if err := ensureMetadataEOF(decoder); err != nil {
		return Record{}, errors.New("portal session metadata is invalid")
	}
	record.ProfileDir = filepath.Join(directory, "chrome")
	if err := validateRecord(record, profileID, ref); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *FileStore) Commit(record Record, digest []byte) (Record, error) {
	if len(digest) != IdentityDigestBytes {
		return Record{}, errors.New("portal identity digest is invalid")
	}
	current, err := s.Load(record.ProfileID, record.SessionRef)
	if err != nil {
		return Record{}, err
	}
	if current.State != StateStaged {
		return Record{}, errors.New("portal session is not staged")
	}
	current.State = StateActive
	current.IdentityDigest = append([]byte(nil), digest...)
	if err := s.write(current); err != nil {
		return Record{}, err
	}
	return current, nil
}

func (s *FileStore) SetState(
	profileID, ref string,
	state State,
) (Record, error) {
	record, err := s.Load(profileID, ref)
	if err != nil {
		return Record{}, err
	}
	record.State = state
	if err := s.write(record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *FileStore) Delete(ref string) error {
	directory, err := s.directory(ref)
	if err != nil {
		return err
	}
	return os.RemoveAll(directory)
}

func (s *FileStore) write(record Record) error {
	if err := validateRecord(record, record.ProfileID, record.SessionRef); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return errors.New("encode portal session metadata")
	}
	directory, err := s.directory(record.SessionRef)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create portal session metadata: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure portal session metadata: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write portal session metadata: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync portal session metadata: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close portal session metadata: %w", err)
	}
	if err := os.Rename(tempPath, filepath.Join(directory, metadataFileName)); err != nil {
		return fmt.Errorf("replace portal session metadata: %w", err)
	}
	return nil
}

func (s *FileStore) directory(ref string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("portal session state root is unavailable")
	}
	if !sessionRefPattern.MatchString(ref) {
		return "", errors.New("portal session reference is invalid")
	}
	return filepath.Join(s.root, ref), nil
}

func validateRecord(record Record, profileID, ref string) error {
	if record.SchemaVersion != storeSchemaVersion ||
		record.ProfileID != profileID ||
		record.SessionRef != ref ||
		!profileIDPattern.MatchString(record.ProfileID) ||
		!sessionRefPattern.MatchString(record.SessionRef) ||
		len(record.IdentityKey) != identityKeyBytes {
		return errors.New("portal session metadata is invalid")
	}
	switch record.State {
	case StateStaged:
		if len(record.IdentityDigest) != 0 &&
			len(record.IdentityDigest) != IdentityDigestBytes {
			return errors.New("portal session metadata is invalid")
		}
	case StateActive, StateUnknown, StateSessionLost, StateExplicitLogout:
		if len(record.IdentityDigest) != IdentityDigestBytes {
			return errors.New("portal session metadata is invalid")
		}
	default:
		return errors.New("portal session metadata is invalid")
	}
	return nil
}

func secureDirectory(path string) error {
	if path == "" {
		return errors.New("portal session state root is unavailable")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create portal session state root: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure portal session state root: %w", err)
	}
	return nil
}

func newSessionRef() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("generate portal session reference")
	}
	return "s_" + strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).
			EncodeToString(random[:]),
	), nil
}

func ensureMetadataEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else {
		return err
	}
}

func equalDigest(first, second []byte) bool {
	return len(first) == IdentityDigestBytes &&
		len(second) == IdentityDigestBytes &&
		subtle.ConstantTimeCompare(first, second) == 1
}
