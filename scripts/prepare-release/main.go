package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type fragment struct {
	Path   string
	Digest string
	Body   string
}
type receipt struct {
	Version      string
	Commit       string
	Fragments    []fragment
	Section      string
	SourceDigest string
	Changelog    string
}

func git(root string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...)
	c.Dir = root
	b, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return b, nil
}

func parse(path string, data []byte) (string, error) {
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "## Changes\n") {
		return "", fmt.Errorf("%s: expected ## Changes followed by English Markdown bullets", path)
	}
	body := strings.TrimSpace(strings.TrimPrefix(s, "## Changes\n"))
	if body == "" {
		return "", fmt.Errorf("%s: no changes", path)
	}
	for _, line := range strings.Split(body, "\n") {
		if line != "" && !strings.HasPrefix(line, "- ") {
			return "", fmt.Errorf("%s: each entry must be one Markdown bullet on one line", path)
		}
	}
	return body, nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func capture(root string) (receipt, []byte, error) {
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return receipt{}, nil, err
	}
	r := receipt{Commit: strings.TrimSpace(string(head))}
	changelog, err := git(root, "show", r.Commit+":CHANGELOG.md")
	if err != nil {
		return r, nil, err
	}
	paths, err := git(root, "ls-tree", "-r", "--name-only", "-z", r.Commit, "--", ".changes")
	if err != nil {
		return r, nil, err
	}
	for _, path := range strings.Split(string(paths), "\x00") {
		if filepath.Dir(path) != ".changes" || filepath.Ext(path) != ".md" || path == ".changes/README.md" {
			continue
		}
		data, err := git(root, "show", r.Commit+":"+path)
		if err != nil {
			return r, nil, err
		}
		body, err := parse(path, data)
		if err != nil {
			return r, nil, err
		}
		r.Fragments = append(r.Fragments, fragment{Path: path, Digest: digest(data), Body: body})
	}
	return r, changelog, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func mkdirDurable(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := mkdirDurable(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	return syncDirectory(parent)
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func section(text, version string) string {
	start := strings.Index(text, "\n## "+version+" - ")
	if start < 0 {
		return ""
	}
	tail := text[start+1:]
	if end := strings.Index(tail, "\n## "); end >= 0 {
		tail = tail[:end]
	}
	return strings.TrimSpace(tail)
}

func cleanup(root string, r receipt) error {
	recovery := filepath.Join(root, ".changes", ".recovery")
	if err := os.MkdirAll(recovery, 0700); err != nil {
		return err
	}
	for _, f := range r.Fragments {
		path := filepath.Join(root, f.Path)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if digest(data) != f.Digest {
			continue
		}
		dir, err := os.MkdirTemp(recovery, "fragment-")
		if err != nil {
			return err
		}
		backup := filepath.Join(dir, filepath.Base(f.Path))
		// Retain the renamed inode: even an already-open writer cannot lose its bytes.
		if err := os.Rename(path, backup); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		moved, err := os.ReadFile(backup)
		if err != nil {
			return err
		}
		if digest(moved) != f.Digest {
			// Link is exclusive: a new file at the original path is never overwritten.
			if err := os.Link(backup, path); err != nil && !os.IsExist(err) {
				return err
			}
			fmt.Fprintf(os.Stderr, "Preserved concurrently changed fragment at %s (recovery: %s)\n", path, backup)
		}
	}
	return nil
}

func prepare(root, version string, now time.Time) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return fmt.Errorf("version must be X.Y.Z without a v prefix")
	}
	recordPath := filepath.Join(root, ".changes", "releases", version+".json")
	if _, err := os.Stat(recordPath); err == nil {
		return assemble(root, receipt{Version: version}, nil, now)
	} else if !os.IsNotExist(err) {
		return err
	}
	r, source, err := capture(root)
	if err != nil {
		return err
	}
	r.Version = version
	return assemble(root, r, source, now)
}

func assemble(root string, r receipt, source []byte, now time.Time) error {
	recordPath := filepath.Join(root, ".changes", "releases", r.Version+".json")
	if data, err := os.ReadFile(recordPath); err == nil {
		var old receipt
		if err := json.Unmarshal(data, &old); err != nil {
			return err
		}
		current, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
		if err != nil {
			return err
		}
		if old.Section == "" || section(string(current), r.Version) != old.Section {
			if old.Changelog == "" || digest(current) != old.SourceDigest {
				return fmt.Errorf("release receipt does not match the complete changelog section; reconcile before cleanup")
			}
			if err := atomicWrite(filepath.Join(root, "CHANGELOG.md"), []byte(old.Changelog)); err != nil {
				return err
			}
		}
		return cleanup(root, old)
	} else if !os.IsNotExist(err) {
		return err
	}
	current, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return err
	}
	if !bytes.Equal(current, source) {
		return fmt.Errorf("CHANGELOG.md changed since %s; commit or reconcile it before preparing", r.Commit)
	}
	if regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(r.Version) + `(?: |$)`).Match(source) {
		return fmt.Errorf("release section already exists without a receipt")
	}
	used := map[string]bool{}
	records, err := filepath.Glob(filepath.Join(root, ".changes", "releases", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range records {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var previous receipt
		if err := json.Unmarshal(data, &previous); err != nil {
			return err
		}
		if previous.Section == "" || section(string(source), previous.Version) != previous.Section {
			return fmt.Errorf("release %s has an incomplete receipt; retry that version before preparing another", previous.Version)
		}
		for _, f := range previous.Fragments {
			used[f.Path+":"+f.Digest] = true
		}
	}
	var bodies []string
	var included []fragment
	for _, f := range r.Fragments {
		if !used[f.Path+":"+f.Digest] {
			bodies = append(bodies, f.Body)
			included = append(included, f)
		}
	}
	r.Fragments = included
	const pending = "\n## Unreleased\n"
	start := strings.Index(string(source), pending)
	if start < 0 {
		return fmt.Errorf("missing ## Unreleased section")
	}
	contentStart := start + len(pending)
	tail := string(source[contentStart:])
	next := strings.Index(tail, "\n## ")
	legacy := tail
	history := ""
	if next >= 0 {
		legacy = tail[:next]
		history = tail[next:]
	}
	if strings.TrimSpace(legacy) != "" {
		bodies = append([]string{strings.TrimSpace(legacy)}, bodies...)
	}
	if len(bodies) == 0 {
		return fmt.Errorf("no unreleased changes at %s", r.Commit)
	}
	assembled := string(source[:contentStart]) + "\n## " + r.Version + " - " + now.UTC().Format("2006-01-02") + "\n\n" + strings.Join(bodies, "\n") + "\n" + history
	r.Section = section(assembled, r.Version)
	r.SourceDigest = digest(source)
	r.Changelog = assembled
	// A receipt permits exact recovery if interrupted between the two atomic writes.
	if err := mkdirDurable(filepath.Dir(recordPath)); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(recordPath, append(encoded, '\n')); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(root, "CHANGELOG.md"), []byte(assembled)); err != nil {
		return err
	}
	return cleanup(root, r)
}

func check(root string) error {
	paths, err := filepath.Glob(filepath.Join(root, ".changes", "*.md"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if filepath.Base(path) == "README.md" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := parse(path, data); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--check" {
		err = check(".")
	} else if len(os.Args) == 2 {
		err = prepare(".", os.Args[1], time.Now())
	} else {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/prepare-release --check | X.Y.Z")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
