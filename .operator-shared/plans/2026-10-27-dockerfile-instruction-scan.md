# Dockerfile Instruction-Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the launcher's Dockerfile scanning see only real instruction starts, so user text that resembles a keyword — a lowercase `from … import …` inside a `RUN` heredoc, or a continued argument — is never mistaken for a stage header.

**Architecture:** A dependency-free line walker in `internal/sandbox/image` assembles logical lines (continuation-aware), skips comments/blanks, and — for the instructions BuildKit allows heredocs on — skips heredoc bodies using BuildKit's own heredoc operator regex. `scanFromStages` and `lastFromLine` consume the walker's instruction-start indexes; `splitFinalStage` keeps splitting raw text at the final-FROM index, so composition is unchanged.

**Tech Stack:** Go 1.27, `regexp`, table-driven tests. No new module dependency.

**Spec:** The "Instruction scanning" clause added to `.operator-shared/specs/image-composition.md` in Task 2; design agreed in this session (BuildKit-aligned hand-roll).

**Context:** This is the fourth review follow-up on `fix/greptile-369-372`. The first three are already implemented on the branch and must not regress: the per-agent project-VM lock (`ProjectVMLockDir` → `locks/<slug>/<agent>`), the `EWOULDBLOCK`/`EAGAIN` contention assertion, and the `parseFrom` trailing-whitespace/CR trim. This plan covers only the heredoc/continuation scanner.

## Global Constraints

- **No new dependency.** `go.mod`/`go.sum` are unchanged. Port the rules by hand from `github.com/moby/buildkit/frontend/dockerfile/parser`; do not import it (it adds ~4.7 MB stripped and the whole buildkit module graph).
- **Do not regress #346.** `parseFrom` stays case-insensitive with the `FROMAGE` word boundary and the trailing-whitespace/CR trim.
- **Do not regress #330.** `ProjectVMLockDir(k)` stays `filepath.Join(locksDir(), k.Slug, k.Agent)`.
- **Every added or changed production line is executed by a test** (`.operator-shared/specs/test-coverage.md`).
- **Use `make fmt`/`golangci-lint fmt` and `make lint`**; never `go fmt`/`go vet`. Run `make fmt && make lint && make test && make compile-all` before a task is done.
- **CHANGELOG:** fold the heredoc fix into the existing `[Unreleased]` #346 entry (it is a correction to an unreleased change, not a new user-visible feature).
- **Worktree:** `fix--greptile-369-372` (branch `fix/greptile-369-372`), based on `release/0.6`. The `git commit` steps apply there.

## Review Focus

Inputs/conditions the design implies that a reasonable person would expect to keep working; each is pinned by the task that owns it.

1. A `RUN` heredoc whose body contains a lowercase `from … import …` line is not read as a stage header. → Task 1.
2. A continuation line (`… && \`) whose text starts with `FROM` is not a stage header. → Task 1.
3. Shell arithmetic `$((1<<2))` in a `RUN` is not a heredoc, so a later real `FROM` is still found. → Task 1.
4. A `<<-EOF` heredoc with a tab-indented terminator ends at the terminator. → Task 1.
5. A quoted delimiter containing whitespace (`<<'PY DOC'`) is a heredoc named `PY DOC`, so its body is skipped and not scanned. → Task 1.
6. A `# escape=` first line switches the continuation marker, so a `\`-terminated line is not joined and a following `FROM` is still found. → Task 1.
7. Existing case-insensitive (`from debian`), CRLF (`… AS base\r`), and `FROMAGE` behavior is unchanged. → Task 1 (existing tests stay green).

---

### Task 1: continuation- and heredoc-aware instruction walker

**Files:**
- Create: `internal/sandbox/image/dockerfile_lines.go`
- Create: `internal/sandbox/image/dockerfile_lines_test.go`
- Modify: `internal/sandbox/image/build.go` (`scanFromStages`, ~line 76)
- Modify: `internal/sandbox/image/dockerfile.go` (`lastFromLine`, ~line 609)
- Modify: `internal/sandbox/image/render_test.go` (end-to-end regression)

**Interfaces:**
- Consumes: `instruction(line string) (string, string)` and `parseFrom(line string) (string, string)` (both existing).
- Produces (package `image`, all in `dockerfile_lines.go`):
  - `dockerfileLines(dockerfile []byte) [][]byte` — `bytes.SplitAfter(dockerfile, []byte("\n"))`.
  - `instructionLineIndices(lines [][]byte) []int` — ascending indexes of lines that begin a Dockerfile instruction, skipping blanks, comments, continuation lines, and heredoc bodies.
  - `heredocDelimiter struct { name string; chomp bool }`.
  - `heredocDelimiters(instructionText string) []heredocDelimiter`.
  - `heredocBodyEnds(line string, d heredocDelimiter) bool`.
  - `instructionWords(text string) []string` — whitespace-split with quotes preserved (a quoted run, including spaces, stays one word).
  - `escapeToken(lines [][]byte) byte` — `\` unless the first line is `# escape=<char>` (key case-insensitive), else that char.
  - `lineContinues(line string, escape byte) bool`.

- [ ] **Step 1: Write the failing tests**

`dockerfile_lines_test.go` (new file, package `image`; use `slices.Equal` for the index comparisons):

```go
package image

import (
	"slices"
	"testing"
)

func instructionIndexes(t *testing.T, dockerfile string) []int {
	t.Helper()
	return instructionLineIndices(dockerfileLines([]byte(dockerfile)))
}

func TestInstructionLineIndicesSkipsHeredocBody(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN python3 - <<'PY'\n"+
			"from pathlib import Path\n"+
			"print(Path.cwd())\n"+
			"PY\n"+
			"RUN echo done\n")
	want := []int{0, 1, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesSkipsContinuationLines(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM not-a-stage\n"+
			"FROM alpine\n")
	want := []int{0, 1, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesArithmeticShiftIsNotHeredoc(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo $((1<<2))\n"+
			"FROM alpine\n")
	want := []int{0, 1, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesChompHeredocTabTerminator(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN <<-EOF\n"+
			"\techo hi\n"+
			"\tEOF\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesQuotedDelimiterWithSpace(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN python3 - <<'PY DOC'\n"+
			"from pathlib import Path\n"+
			"PY DOC\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapeDirectiveSwitchesContinuation(t *testing.T) {
	got := instructionIndexes(t,
		"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+ // a literal backslash under backtick escape
			"FROM alpine\n")
	want := []int{1, 2, 3} // line 0 is the (skipped) directive comment
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestHeredocDelimiters(t *testing.T) {
	cases := []struct {
		line string
		want []heredocDelimiter
	}{
		{"RUN <<EOF", []heredocDelimiter{{name: "EOF"}}},
		{"RUN <<-EOF", []heredocDelimiter{{name: "EOF", chomp: true}}},
		{"RUN python3 - <<'PY'", []heredocDelimiter{{name: "PY"}}},
		{"RUN python3 - <<'PY DOC'", []heredocDelimiter{{name: "PY DOC"}}},
		{"COPY <<\"FILE\" /dest", []heredocDelimiter{{name: "FILE"}}},
		{"RUN echo $((1<<2))", nil},
		{"RUN echo hi", nil},
		{"RUN cat<<EOF", nil},   // matches BuildKit: the operator must start the word
		{"RUN echo \"<<EOF\"", nil}, // a quoted operator is not a heredoc
		{"FROM <<EOF", nil},     // only RUN/COPY/ADD/ONBUILD may contain a heredoc
	}
	for _, tc := range cases {
		got := heredocDelimiters(tc.line)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("heredocDelimiters(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
```

Add to `render_test.go`:

```go
func TestRenderDockerfileHeredocBodyIsNotAStage(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM debian:trixie-slim\n" +
		"RUN python3 - <<'PY'\n" +
		"from pathlib import Path\n" +
		"print(Path.cwd())\n" +
		"PY\n")
	s := mustRender(t, a, project, false)
	if strings.Contains(s, "from pathlib import Path AS ") {
		t.Errorf("a heredoc body must not be rewritten as a stage header; got:\n%s", s)
	}
	if !strings.Contains(s, "RUN python3 - <<'PY'\nfrom pathlib import Path\n") {
		t.Errorf("the heredoc must be preserved verbatim; got:\n%s", s)
	}
	if !strings.Contains(s, "FROM debian:trixie-slim AS "+baseStageAlias) {
		t.Errorf("the real base FROM must be composed; got:\n%s", s)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'Heredoc|InstructionLineIndices|RenderDockerfileHeredoc'`
Expected: FAIL / compile error (`instructionLineIndices` undefined).

- [ ] **Step 3: Implement `dockerfile_lines.go`**

Port BuildKit's rules (`frontend/dockerfile/parser`), without its shell lexer:

- `dockerfileLines` = `bytes.SplitAfter(dockerfile, []byte("\n"))`.
- `lineText(raw []byte) string` = `strings.TrimRight(string(raw), "\r\n")`.
- `isBlankOrComment(line string) bool` = left-trim spaces/tabs, empty or `#`-prefixed.
- `escapeToken(lines)` — return `` ` `` when the first line matches `(?i)^#\s*escape=` followed by `` ` `` or `\`; else `\` (BuildKit honors only the literal first line, and only before any comment/blank/instruction).
- `lineContinues(line, escape)` — BuildKit's continuation regex built for the escape char: `([^X])X[ \t]*$|^X[ \t]*$` (with `X` the escaped char), compiled once per supported escape at package scope; a trailing escape not itself escaped continues the line.
- `instructionWords(text)` — split on unquoted whitespace, keeping quote characters and any spaces inside a quoted run, so `<<'PY DOC'` stays one word (BuildKit's `RawQuotes` lexing).
- `instructionLineIndices` walks with an index `i`: compute `escape = escapeToken(lines)` once; skip blank/comment lines; otherwise record `i` and jump `i = instructionEnd(lines, i, escape)`.
- `instructionEnd(lines, i, escape)`:
  - Accumulate a logical line: append `lineText(lines[i])`; advance; on the first line stop at `!lineContinues(line, escape)`; on later lines skip blanks/comments without stopping, and stop at `!lineContinues`.
  - For each `d := range heredocDelimiters(logical)`: advance until `heredocBodyEnds(lineText(lines[i]), d)` (or end of input).
- `heredocDelimiters(instructionText string)`: return nil unless `instruction(instructionText)`'s keyword is in `{"RUN", "COPY", "ADD", "ONBUILD"}`; else tokenize with `instructionWords`, match each token against `regexp.MustCompile("^(\\d*)<<(-?)\\s*([^<]*)$")`, take `match[3]` with `'`/`"` removed as `name` (skip empty), and `chomp = match[2] == "-"`. Starting the match at the token is what excludes `$((1<<2))`, `cat<<EOF`, and `"<<EOF"`, matching BuildKit.
- `heredocBodyEnds(line, d)`: trim trailing `\r\n`; when `d.chomp`, also left-trim tabs; report `line == d.name`.

Then rewire:

`scanFromStages` (build.go) — replace the `bufio.Scanner` loop with:

```go
func scanFromStages(dockerfile []byte) (string, map[string]string) {
	stageBase := make(map[string]string)
	var lastImage string
	lines := dockerfileLines(dockerfile)
	for _, i := range instructionLineIndices(lines) {
		fromImage, stageAlias := parseFrom(string(lines[i]))
		if fromImage == "" {
			continue
		}
		if stageAlias != "" {
			stageBase[stageAlias] = fromImage
		}
		lastImage = fromImage
	}
	return lastImage, stageBase
}
```

Drop the now-unused `bufio` import from `build.go` (run `make lint` to confirm nothing else uses it).

`lastFromLine` (dockerfile.go) — replace the loop with:

```go
func lastFromLine(lines [][]byte) int {
	lastFrom := -1
	for _, i := range instructionLineIndices(lines) {
		if keyword, _ := instruction(string(lines[i])); keyword == fromInstruction {
			lastFrom = i
		}
	}
	return lastFrom
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/... ./internal/sandbox/vm/...`
Expected: PASS. All pre-existing image tests (lowercase `from`, CRLF, `FROMAGE`, reserved aliases, `splitFinalStage`, fuzz seeds) stay green.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image
git commit -m "fix(image): skip heredoc bodies and continuations when scanning instructions"
```

---

### Task 2: spec clause, CHANGELOG, full check

**Files:**
- Modify: `.operator-shared/specs/image-composition.md`
- Modify: `CHANGELOG.md`
- Test: none (docs); `make check` enforces links/format.

**Interfaces:**
- Consumes: Task 1's walker and its documented rules.
- Produces: nothing new.

- [ ] **Step 1: Add the "Instruction scanning" clause to `image-composition.md`**

Under the existing composition contract, add a short clause (a few lines): the launcher scans the project Dockerfile for stage headers with a BuildKit-aligned, dependency-free walker — it matches instruction keywords case-insensitively (`FROMAGE` is not `FROM`), trims a line's trailing whitespace so CRLF is handled, ignores blank/comment/continuation lines (honoring a first-line `# escape=` directive), and skips heredoc bodies (only `RUN`/`COPY`/`ADD`/`ONBUILD` may declare one, detected with BuildKit's `^(\d*)<<(-?)\s*([^<]*)$` per whitespace/quoted token). It uses no shell lexer, but its observed behavior matches BuildKit for quoted delimiters with spaces, arithmetic (`$((1<<2))`), and quoted operators (`"<<EOF"`).

- [ ] **Step 2: Amend the CHANGELOG `[Unreleased]` #346 line**

Extend the existing #346 entry to say the instruction scan also skips heredoc bodies and continuation lines, so a lowercase `from … import …` inside a `RUN` heredoc is not mistaken for a stage header.

- [ ] **Step 3: Run the full check**

Run: `make fmt && make lint && make test && make compile-all && ci/check-docs.sh`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md .operator-shared
git commit -m "docs: record the instruction-scan heredoc/continuation rules"
```

---

## Self-review notes

- **Spec coverage:** instruction-start skipping (Task 1) covers heredocs (Review Focus 1, 4), continuations (2), arithmetic non-heredoc (3), quoted delimiters with spaces (5), and the escape directive (6); #346/#330 and case/CRLF/`FROMAGE` are protected by the Global Constraints and re-run in Task 1 Step 4. The durable contract lands in Task 2.
- **Deliberate non-changes:** `splitFinalStage`, `customBaseStage`, `managedBaseStage`, and `parseFrom` are untouched; the walker only changes which lines are considered instruction starts.
- **No shell lexer, no frozen divergences:** the two BuildKit behaviors we did not originally replicate (quoted delimiter with spaces, `# escape=`) are implemented rather than pinned by characterization tests; verified against BuildKit's real lexer for `<<'PY DOC'`, `cat<<EOF`, `$((1<<2))`, `<<-EOF`, `<<"FILE"`, `echo "<<EOF"`, and a first-line/after-comment/after-blank escape directive.
- **Left to the builder:** the exact comment wording and whether `instructionEnd` skips comment lines inside continuations (BuildKit does; keep that behavior).
- **Execution method:** not yet chosen — ask before starting.
