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

func TestInstructionLineIndicesEscapeDirectiveAfterSyntax(t *testing.T) {
	got := instructionIndexes(t,
		"# syntax=docker/dockerfile:1\n"+
			"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+
			"FROM alpine\n")
	want := []int{2, 3, 4} // the leading directive block is honored, not only line 0
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapeDirectiveSpacesAndIndent(t *testing.T) {
	got := instructionIndexes(t,
		"  # escape = `\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+
			"FROM alpine\n")
	want := []int{1, 2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesExplicitBackslashEscape(t *testing.T) {
	got := instructionIndexes(t,
		"# escape=\\\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM alpine\n")
	want := []int{1, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesNonDirectiveCommentStopsEscape(t *testing.T) {
	// A plain comment before the directive stops BuildKit from reading it, so the
	// default escape applies and the continuation swallows the following FROM.
	got := instructionIndexes(t,
		"# note\n"+
			"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM alpine\n")
	want := []int{2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesSkipsCommentInsideContinuation(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"# comment\n"+
			"echo two\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
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
		{"RUN cat<<EOF", nil},       // matches BuildKit: the operator must start the word
		{"RUN echo \"<<EOF\"", nil}, // a quoted operator is not a heredoc
		{"RUN echo << bar", nil},    // a spaced operator has an empty delimiter name
		{"FROM <<EOF", nil},         // only RUN/COPY/ADD/ONBUILD may contain a heredoc
	}
	for _, tc := range cases {
		got := heredocDelimiters(tc.line)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("heredocDelimiters(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
