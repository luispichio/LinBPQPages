package bpq

import (
	"fmt"
	"strconv"
	"strings"
)

type configTokenKind uint8

const (
	configAtom configTokenKind = iota
	configQuotedString
	configPunctuation
)

type configToken struct {
	kind configTokenKind
	text string
}

// parseLibconfig implementa el subconjunto de datos usado por linmail.cfg:
// grupos anidados, arrays, strings, escalares y comentarios de libconfig.
func parseLibconfig(input []byte) (map[string]any, error) {
	tokens, err := lexLibconfig(input)
	if err != nil {
		return nil, err
	}
	parser := configParser{tokens: tokens}
	result, err := parser.parseObject(false)
	if err != nil {
		return nil, err
	}
	if parser.position != len(parser.tokens) {
		return nil, fmt.Errorf("unexpected token %q after configuration", parser.tokens[parser.position].text)
	}
	return result, nil
}

func lexLibconfig(input []byte) ([]configToken, error) {
	tokens := make([]configToken, 0, len(input)/8)
	for i := 0; i < len(input); {
		if isConfigSpace(input[i]) {
			i++
			continue
		}
		if input[i] == '#' {
			for i < len(input) && input[i] != '\n' {
				i++
			}
			continue
		}
		if input[i] == '/' && i+1 < len(input) {
			switch input[i+1] {
			case '/':
				i += 2
				for i < len(input) && input[i] != '\n' {
					i++
				}
				continue
			case '*':
				start := i
				i += 2
				for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
					i++
				}
				if i+1 >= len(input) {
					return nil, fmt.Errorf("unterminated block comment at byte %d", start)
				}
				i += 2
				continue
			}
		}
		if input[i] == '"' {
			start := i
			i++
			for i < len(input) {
				if input[i] == '\\' {
					i += 2
					continue
				}
				if input[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(input) || input[i-1] != '"' {
				return nil, fmt.Errorf("unterminated string at byte %d", start)
			}
			value, err := strconv.Unquote(string(input[start:i]))
			if err != nil {
				return nil, fmt.Errorf("invalid string at byte %d: %w", start, err)
			}
			tokens = append(tokens, configToken{kind: configQuotedString, text: value})
			continue
		}
		if strings.ContainsRune("{}:=[]();,", rune(input[i])) {
			tokens = append(tokens, configToken{kind: configPunctuation, text: string(input[i])})
			i++
			continue
		}
		start := i
		for i < len(input) && !isConfigSpace(input[i]) && !strings.ContainsRune("{}:=[]();,\"", rune(input[i])) {
			if input[i] == '#' || input[i] == '/' && i+1 < len(input) && (input[i+1] == '/' || input[i+1] == '*') {
				break
			}
			i++
		}
		if start == i {
			return nil, fmt.Errorf("unexpected byte %q at offset %d", input[i], i)
		}
		tokens = append(tokens, configToken{kind: configAtom, text: string(input[start:i])})
	}
	return tokens, nil
}

func isConfigSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

type configParser struct {
	tokens   []configToken
	position int
}

func (p *configParser) parseObject(closed bool) (map[string]any, error) {
	result := make(map[string]any)
	for p.position < len(p.tokens) {
		if p.peek("}") {
			if !closed {
				return nil, fmt.Errorf("unexpected closing brace")
			}
			p.position++
			return result, nil
		}
		if p.peek(";") || p.peek(",") {
			p.position++
			continue
		}
		key := p.tokens[p.position]
		if key.kind == configPunctuation {
			return nil, fmt.Errorf("expected setting name, got %q", key.text)
		}
		p.position++
		if p.position >= len(p.tokens) || !(p.peek(":") || p.peek("=")) {
			return nil, fmt.Errorf("expected ':' or '=' after setting %q", key.text)
		}
		p.position++
		value, err := p.parseValue()
		if err != nil {
			return nil, fmt.Errorf("setting %q: %w", key.text, err)
		}
		result[key.text] = value
		if p.peek(";") || p.peek(",") {
			p.position++
		}
	}
	if closed {
		return nil, fmt.Errorf("unterminated group")
	}
	return result, nil
}

func (p *configParser) parseValue() (any, error) {
	if p.position >= len(p.tokens) {
		return nil, fmt.Errorf("missing value")
	}
	token := p.tokens[p.position]
	switch token.text {
	case "{":
		p.position++
		return p.parseObject(true)
	case "[", "(":
		p.position++
		closeToken := "]"
		if token.text == "(" {
			closeToken = ")"
		}
		values := make([]any, 0)
		for p.position < len(p.tokens) && !p.peek(closeToken) {
			if p.peek(",") || p.peek(";") {
				p.position++
				continue
			}
			value, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if !p.peek(closeToken) {
			return nil, fmt.Errorf("unterminated array")
		}
		p.position++
		return values, nil
	default:
		if token.kind == configPunctuation {
			return nil, fmt.Errorf("unexpected token %q as value", token.text)
		}
		p.position++
		return token.text, nil
	}
}

func (p *configParser) peek(value string) bool {
	return p.position < len(p.tokens) && p.tokens[p.position].text == value
}

func configObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func configString(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func configInt(object map[string]any, key string) int64 {
	value, ok := object[key].(string)
	if !ok {
		return 0
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
