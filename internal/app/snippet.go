package app

import (
	"strconv"
	"unicode"

	"github.com/brohd11/bubblestack/components"
)

// parseLSPSnippet expands the practical completion subset Gote advertises: numbered
// tab stops, placeholders, choices, escapes, and the final $0 stop. Variables,
// transforms, nested placeholders, and linked/repeated stops are rejected so their
// source syntax can never leak into the editor.
func parseLSPSnippet(source string) (string, []components.EditorCompletionStop, bool) {
	p := snippetParser{source: []rune(normalizeCompletionText(source)), seen: make(map[int]bool)}
	if !p.parseText() {
		return "", nil, false
	}
	return string(p.output), p.stops, true
}

type snippetParser struct {
	source []rune
	pos    int
	output []rune
	stops  []components.EditorCompletionStop
	seen   map[int]bool
}

func normalizeCompletionText(source string) string {
	runes := []rune(source)
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\r':
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
			out = append(out, '\n')
		case r == '\n' || r == '\t' || !unicode.IsControl(r):
			out = append(out, r)
		}
	}
	return string(out)
}

func (p *snippetParser) parseText() bool {
	for p.pos < len(p.source) {
		switch p.source[p.pos] {
		case '\\':
			if p.pos+1 >= len(p.source) {
				return false
			}
			next := p.source[p.pos+1]
			if next != '\\' && next != '$' && next != '}' {
				return false
			}
			p.output = append(p.output, next)
			p.pos += 2
		case '$':
			if !p.parseDollar() {
				return false
			}
		default:
			p.output = append(p.output, p.source[p.pos])
			p.pos++
		}
	}
	return true
}

func (p *snippetParser) parseDollar() bool {
	p.pos++
	if p.pos >= len(p.source) {
		return false
	}
	if isASCIIDigit(p.source[p.pos]) {
		index, ok := p.parseIndex()
		return ok && p.addStop(index, len(p.output), len(p.output))
	}
	if p.source[p.pos] != '{' {
		return false // named variables and malformed dollar expressions are unsupported
	}
	p.pos++
	if p.pos >= len(p.source) || !isASCIIDigit(p.source[p.pos]) {
		return false
	}
	index, ok := p.parseIndex()
	if !ok || p.pos >= len(p.source) {
		return false
	}
	switch p.source[p.pos] {
	case '}':
		p.pos++
		return p.addStop(index, len(p.output), len(p.output))
	case ':':
		p.pos++
		start := len(p.output)
		if !p.parsePlaceholderText() {
			return false
		}
		return p.addStop(index, start, len(p.output))
	case '|':
		p.pos++
		start := len(p.output)
		if !p.parseChoice() {
			return false
		}
		return p.addStop(index, start, len(p.output))
	default:
		return false // includes transform syntax
	}
}

func (p *snippetParser) parseIndex() (int, bool) {
	start := p.pos
	for p.pos < len(p.source) && isASCIIDigit(p.source[p.pos]) {
		p.pos++
	}
	value, err := strconv.Atoi(string(p.source[start:p.pos]))
	return value, err == nil
}

func (p *snippetParser) parsePlaceholderText() bool {
	for p.pos < len(p.source) {
		switch p.source[p.pos] {
		case '}':
			p.pos++
			return true
		case '$':
			return false // nested stops and variables are intentionally outside v1
		case '\\':
			if p.pos+1 >= len(p.source) {
				return false
			}
			next := p.source[p.pos+1]
			if next != '\\' && next != '$' && next != '}' {
				return false
			}
			p.output = append(p.output, next)
			p.pos += 2
		default:
			p.output = append(p.output, p.source[p.pos])
			p.pos++
		}
	}
	return false
}

func (p *snippetParser) parseChoice() bool {
	first := true
	for p.pos < len(p.source) {
		r := p.source[p.pos]
		switch r {
		case '\\':
			if p.pos+1 >= len(p.source) {
				return false
			}
			next := p.source[p.pos+1]
			if next != '\\' && next != ',' && next != '|' {
				return false
			}
			if first {
				p.output = append(p.output, next)
			}
			p.pos += 2
		case ',':
			first = false
			p.pos++
		case '|':
			if p.pos+1 >= len(p.source) || p.source[p.pos+1] != '}' {
				return false
			}
			p.pos += 2
			return true
		case '$', '}':
			return false
		default:
			if first {
				p.output = append(p.output, r)
			}
			p.pos++
		}
	}
	return false
}

func (p *snippetParser) addStop(index, start, end int) bool {
	if p.seen[index] || index == 0 && start != end {
		return false
	}
	p.seen[index] = true
	p.stops = append(p.stops, components.EditorCompletionStop{Index: index, Start: start, End: end})
	return true
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }
