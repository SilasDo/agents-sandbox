package termio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestIsInteractive(t *testing.T) {
	t.Run("returns false when stdin is not a terminal", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }
		if p.IsInteractive() {
			t.Fatal("expected false when stdin is not a terminal")
		}
	})

	t.Run("returns false when yes flag is set", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, true)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if p.IsInteractive() {
			t.Fatal("expected false when yes flag is set")
		}
	})

	t.Run("returns true when stdin is a terminal and yes flag is not set", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if !p.IsInteractive() {
			t.Fatal("expected true when stdin is a terminal and yes flag is not set")
		}
	})

	t.Run("SetAssumeYes makes it non-interactive", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if !p.IsInteractive() {
			t.Fatal("expected interactive before SetAssumeYes")
		}
		ui.SetAssumeYes(true)
		if p.IsInteractive() {
			t.Fatal("expected non-interactive after SetAssumeYes")
		}
	})
}

func TestSelect(t *testing.T) {
	choices := []Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}

	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("r\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
		if !strings.Contains(stderr.String(), "using default 'k'") {
			t.Errorf("expected default message on stderr, got %q", stderr.String())
		}
	})

	t.Run("returns matched key in interactive mode", func(t *testing.T) {
		ui := New(strings.NewReader("r\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("matches keys case-insensitively", func(t *testing.T) {
		ui := New(strings.NewReader("R\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("marks the default choice inline", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "k) Keep (default) - Keep the worktree") {
			t.Errorf("expected default-marked listing, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "r) Remove (default)") {
			t.Errorf("non-default choice should not be marked default: %q", stderr.String())
		}
	})

	t.Run("uses default when user presses enter", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		ui := New(strings.NewReader("x\nfoo\nr\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r after retries, got %q", got)
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		ui := New(
			strings.NewReader("x\nx\nx\nx\nx\nx\n"),
			&bytes.Buffer{},
			&bytes.Buffer{},
			false,
			LevelInfo,
			false,
			false,
		)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		ui := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})

	t.Run("placeholder for a choice without a description", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		bare := []Choice{{Label: "Keep", Key: "k"}}
		if _, err := ui.Select("What to do?", bare, "k"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "k) Keep (default) - -") {
			t.Errorf("expected a '-' placeholder for a missing description, got %q", stderr.String())
		}
	})
}

func TestInput(t *testing.T) {
	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns user input", func(t *testing.T) {
		ui := New(strings.NewReader("feature\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Fatalf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty input", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		ui := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Input("Branch name:", "main")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

func TestConfirm(t *testing.T) {
	t.Run("assume-yes returns true without reading stdin", func(t *testing.T) {
		var stdout bytes.Buffer
		ui := New(strings.NewReader("never read\n"), &stdout, &bytes.Buffer{}, false, LevelInfo, false, true)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Confirm("Remove it?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true when assume-yes is set")
		}
		if stdout.Len() != 0 {
			t.Errorf("assume-yes should not write any output, got %q", stdout.String())
		}
	})

	t.Run("returns true for y", func(t *testing.T) {
		ui := New(strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Confirm("Remove it?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true for y")
		}
	})

	t.Run("returns true for YES case-insensitively", func(t *testing.T) {
		ui := New(strings.NewReader("YES\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Confirm("Remove it?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true for YES")
		}
	})

	t.Run("returns false for n", func(t *testing.T) {
		ui := New(strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Confirm("Remove it?", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false for n")
		}
	})

	t.Run("empty input returns defaultYes", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Confirm("Remove it?", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected default true on empty input")
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		ui := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Confirm("Remove it?", false)
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})

	t.Run("non-interactive without assume-yes requires confirmation", func(t *testing.T) {
		ui := New(strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := ui.Confirm("Remove it?", false)
		if !errors.Is(err, ErrConfirmationRequired) {
			t.Fatalf("expected ErrConfirmationRequired, got %v", err)
		}
		if got {
			t.Fatal("expected false when confirmation is required")
		}
	})
}

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
