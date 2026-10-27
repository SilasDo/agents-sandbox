package image

import (
	"bytes"
	"regexp"
	"strings"
)

// heredocDirectives are the Dockerfile instructions that may declare a heredoc,
// mirroring BuildKit's parser; ONBUILD can wrap one of them.
var heredocDirectives = map[string]struct{}{
	"RUN":     {},
	"COPY":    {},
	"ADD":     {},
	"ONBUILD": {},
}

// heredocOperator matches a BuildKit heredoc word: an optional file descriptor,
// the operator (with the optional chomping "-"), and the delimiter. It is
// anchored, so a token only counts when the operator starts it — which is how a
// shell arithmetic shift such as $((1<<2)) and a quoted operator such as
// "<<EOF" are excluded, matching BuildKit.
var heredocOperator = regexp.MustCompile(`^(\d*)<<(-?)\s*([^<]*)$`)

// quoteStripper removes the quoting BuildKit's shell lexer would strip from a
// heredoc delimiter word.
var quoteStripper = strings.NewReplacer("'", "", `"`, "")

// lineContinuations maps a supported escape token to BuildKit's line
// continuation pattern: an escape at end of line that is not itself escaped, or
// a line that is only the escape token.
var lineContinuations = map[byte]*regexp.Regexp{
	'\\': regexp.MustCompile(`([^\\])\\[ \t]*$|^\\[ \t]*$`),
	'`':  regexp.MustCompile("([^`])`[ \t]*$|^`[ \t]*$"),
}

// escapeDirective matches a BuildKit parser directive that sets the escape token.
var escapeDirective = regexp.MustCompile(`(?i)^#\s*escape=(.)\s*$`)

// heredocDelimiter is a heredoc a Dockerfile instruction declares: its delimiter
// word and whether the leading tabs of its body are stripped (the "<<" vs "<<-"
// operator).
type heredocDelimiter struct {
	name  string
	chomp bool
}

// dockerfileLines splits a Dockerfile into lines, keeping each line's trailing
// newline so callers can reassemble the source unchanged.
func dockerfileLines(dockerfile []byte) [][]byte {
	return bytes.SplitAfter(dockerfile, []byte("\n"))
}

// instructionLineIndices returns, in order, the indexes of the lines that begin a
// Dockerfile instruction. Blank lines, comments, continuation lines, and heredoc
// bodies are skipped, so text that merely resembles a keyword — a Python
// "from ... import ..." inside a RUN heredoc, or a continued argument — is not
// read as a stage header (#346).
func instructionLineIndices(lines [][]byte) []int {
	escape := escapeToken(lines)
	var indices []int
	for i := 0; i < len(lines); {
		if isBlankOrComment(lineText(lines[i])) {
			i++
			continue
		}
		indices = append(indices, i)
		i = instructionEnd(lines, i, escape)
	}
	return indices
}

// instructionEnd returns the index of the first line after the instruction that
// begins at start: its continuation lines and any heredoc bodies it declares.
func instructionEnd(lines [][]byte, start int, escape byte) int {
	var logical strings.Builder
	i := start
	first := true
	for i < len(lines) {
		line := lineText(lines[i])
		i++
		if !first && isBlankOrComment(line) {
			// A comment or blank line inside a continuation is ignored.
			continue
		}
		first = false
		logical.WriteString(line)
		logical.WriteByte('\n')
		if !lineContinues(line, escape) {
			break
		}
	}
	for _, delimiter := range heredocDelimiters(logical.String()) {
		for i < len(lines) {
			line := lineText(lines[i])
			i++
			if heredocBodyEnds(line, delimiter) {
				break
			}
		}
	}
	return i
}

// heredocDelimiters returns the heredocs an instruction declares. Only the
// instructions BuildKit allows may declare one, and a delimiter is recognised
// only when the operator starts the token.
func heredocDelimiters(instructionText string) []heredocDelimiter {
	keyword, _ := instruction(instructionText)
	if _, ok := heredocDirectives[keyword]; !ok {
		return nil
	}
	var delimiters []heredocDelimiter
	for _, word := range instructionWords(instructionText) {
		match := heredocOperator.FindStringSubmatch(word)
		if match == nil {
			continue
		}
		name := quoteStripper.Replace(match[3])
		if name == "" {
			continue
		}
		delimiters = append(delimiters, heredocDelimiter{name: name, chomp: match[2] == "-"})
	}
	return delimiters
}

// heredocBodyEnds reports whether line terminates a heredoc body. A chomping
// heredoc ("<<-") allows its delimiter to be indented with tabs.
func heredocBodyEnds(line string, d heredocDelimiter) bool {
	if d.chomp {
		line = strings.TrimLeft(line, "\t")
	}
	return line == d.name
}

// instructionWords splits an instruction into whitespace-separated words,
// keeping quotes and any spaces inside a quoted run, so "<<'PY DOC'" stays one
// word.
func instructionWords(text string) []string {
	var words []string
	var current strings.Builder
	var quote byte
	for i := range len(text) {
		c := text[i]
		switch {
		case quote != 0:
			current.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
			current.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// escapeToken returns the escape and line-continuation token for the Dockerfile.
// BuildKit honors an "# escape=" directive only on the literal first line, so a
// directive after a blank line, a comment, or an instruction is not read.
func escapeToken(lines [][]byte) byte {
	if len(lines) == 0 {
		return '\\'
	}
	match := escapeDirective.FindStringSubmatch(lineText(lines[0]))
	if match != nil && match[1] == "`" {
		return '`'
	}
	return '\\'
}

// lineContinues reports whether line ends with the escape token as an unescaped
// line continuation.
func lineContinues(line string, escape byte) bool {
	pattern, ok := lineContinuations[escape]
	return ok && pattern.MatchString(line)
}

// lineText returns a line without its trailing newline or carriage return.
func lineText(raw []byte) string {
	return strings.TrimRight(string(raw), "\r\n")
}

// isBlankOrComment reports whether a line is empty or a comment.
func isBlankOrComment(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}
