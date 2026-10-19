package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v: %s", args, e, b)
	}
	return string(b)
}
func write(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0644); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, root, path string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, path))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func repo(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	run(t, r, "init", "-q")
	run(t, r, "config", "user.name", "Release Test")
	run(t, r, "config", "user.email", "release@example.invalid")
	write(t, r, "CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- Legacy pending.\n\n## 0.1.0 - 2026-01-01\n\n- History.\n")
	write(t, r, ".changes/first.md", "## Changes\n\n- First.\n")
	run(t, r, "add", ".")
	run(t, r, "commit", "-qm", "initial")
	return r
}

var date = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

func TestFrozenCommitAndConcurrentNotes(t *testing.T) {
	root := repo(t)
	r, source, e := capture(root)
	if e != nil {
		t.Fatal(e)
	}
	r.Version = "0.2.0"
	write(t, root, ".changes/first.md", "## Changes\n\n- Changed after snapshot.\n")
	write(t, root, ".changes/new.md", "## Changes\n\n- New after snapshot.\n")
	run(t, root, "add", ".changes")
	run(t, root, "commit", "-qm", "newer notes")
	if e := assemble(root, r, source, date); e != nil {
		t.Fatal(e)
	}
	got := read(t, root, "CHANGELOG.md")
	for _, s := range []string{"## 0.2.0 - 2026-10-19", "- Legacy pending.", "- First.", "## 0.1.0 - 2026-01-01", "- History."} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q: %s", s, got)
		}
	}
	if strings.Contains(got, "after snapshot") {
		t.Fatal("newer notes leaked")
	}
	if !strings.Contains(read(t, root, ".changes/first.md"), "Changed after") {
		t.Fatal("modified fragment lost")
	}
	read(t, root, ".changes/new.md")
}

func TestRetryAndMissedCleanup(t *testing.T) {
	root := repo(t)
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	first := read(t, root, "CHANGELOG.md")
	if _, e := os.Stat(filepath.Join(root, ".changes/first.md")); !os.IsNotExist(e) {
		t.Fatal("included fragment not consumed")
	}
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n") // Interrupted cleanup.
	write(t, root, ".changes/new.md", "## Changes\n\n- Next.\n")
	if e := prepare(root, "0.2.0", date.Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if read(t, root, "CHANGELOG.md") != first {
		t.Fatal("retry changed notes")
	}
	if _, e := os.Stat(filepath.Join(root, ".changes/first.md")); !os.IsNotExist(e) {
		t.Fatal("retry did not clean exact identity")
	}
	read(t, root, ".changes/new.md")
	// Even if cleanup was missed and the old fragment was committed, receipts exclude it.
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "release and next note")
	if e := prepare(root, "0.3.0", date); e != nil {
		t.Fatal(e)
	}
	got := read(t, root, "CHANGELOG.md")
	if strings.Count(got, "- First.") != 1 || strings.Count(got, "- Next.") != 1 {
		t.Fatal(got)
	}
}

func TestReadOnlyCheckAndInvalidFragment(t *testing.T) {
	root := repo(t)
	before := run(t, root, "status", "--porcelain")
	if e := check(root); e != nil {
		t.Fatal(e)
	}
	if run(t, root, "status", "--porcelain") != before {
		t.Fatal("check changed files")
	}
	write(t, root, ".changes/bad.md", "invalid\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "invalid fixture")
	original := read(t, root, "CHANGELOG.md")
	if e := prepare(root, "0.2.0", date); e == nil {
		t.Fatal("accepted invalid fragment")
	}
	if read(t, root, "CHANGELOG.md") != original {
		t.Fatal("invalid input mutated changelog")
	}
}

func TestEmptyAndUncommittedInputs(t *testing.T) {
	root := repo(t)
	write(t, root, "CHANGELOG.md", "# Changelog\n\n## Unreleased\n")
	if e := prepare(root, "0.2.0", date); e == nil {
		t.Fatal("accepted uncommitted changelog")
	}
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "empty legacy")
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "release")
	if e := prepare(root, "0.3.0", date); e == nil {
		t.Fatal("accepted empty release")
	}
	if e := prepare(root, "v0.3.0", date); e == nil {
		t.Fatal("accepted invalid version")
	}
}

func TestNativeExtractor(t *testing.T) {
	root := repo(t)
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	script, e := filepath.Abs("../release-notes.sh")
	if e != nil {
		t.Fatal(e)
	}
	c := exec.Command("sh", script, "0.2.0")
	c.Dir = root
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("extract: %v %s", e, b)
	}
	if !strings.Contains(string(b), "- First.") || strings.Contains(string(b), "- History.") {
		t.Fatal(string(b))
	}
}

func TestInterruptedWriteAndIncompleteSection(t *testing.T) {
	root := repo(t)
	original := read(t, root, "CHANGELOG.md")
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	prepared := read(t, root, "CHANGELOG.md")
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n")
	write(t, root, "CHANGELOG.md", original)
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	if read(t, root, "CHANGELOG.md") != prepared {
		t.Fatal("retry did not restore exact completed output")
	}
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n")
	write(t, root, "CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n## 0.2.0 - 2026-10-19\n")
	if e := prepare(root, "0.2.0", date); e == nil {
		t.Fatal("accepted incomplete section")
	}
	read(t, root, ".changes/first.md")
}

func TestOpenWriterKeepsRecoveryInode(t *testing.T) {
	root := repo(t)
	f, e := os.OpenFile(filepath.Join(root, ".changes/first.md"), os.O_RDWR, 0644)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	if e := f.Truncate(0); e != nil {
		t.Fatal(e)
	}
	if _, e := f.WriteAt([]byte("## Changes\n\n- Written during cleanup.\n"), 0); e != nil {
		t.Fatal(e)
	}
	backups, e := filepath.Glob(filepath.Join(root, ".changes/.recovery/fragment-*/*.md"))
	if e != nil {
		t.Fatal(e)
	}
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	b, e := os.ReadFile(backups[0])
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "Written during cleanup") {
		t.Fatal("open writer data lost")
	}
}

func TestIncompletePriorReceiptCannotSuppressNextRelease(t *testing.T) {
	root := repo(t)
	original := read(t, root, "CHANGELOG.md")
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	write(t, root, "CHANGELOG.md", original)
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n")
	write(t, root, ".changes/new.md", "## Changes\n\n- New.\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "interrupted preparation plus next note")
	if e := prepare(root, "0.3.0", date); e == nil {
		t.Fatal("new version silently excluded incomplete prior receipt")
	}
	if read(t, root, "CHANGELOG.md") != original {
		t.Fatal("changed notes despite incomplete prior receipt")
	}
	read(t, root, ".changes/first.md")
}

func TestRetryDoesNotParseUnrelatedNewFragments(t *testing.T) {
	root := repo(t)
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	write(t, root, ".changes/first.md", "## Changes\n\n- First.\n")
	write(t, root, ".changes/bad.md", "unrelated invalid pending note\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "release plus unrelated pending note")
	if e := prepare(root, "0.2.0", date); e != nil {
		t.Fatal(e)
	}
	read(t, root, ".changes/bad.md")
	if _, e := os.Stat(filepath.Join(root, ".changes/first.md")); !os.IsNotExist(e) {
		t.Fatal("retry did not finish exact cleanup")
	}
}
