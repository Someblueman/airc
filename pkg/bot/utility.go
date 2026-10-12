package bot

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/pkg/irc"
)

// UtilityCommands is a small working bot and an example of the extension API.
// Calculations accept arithmetic only; nothing executes shell commands or code.
func UtilityCommands() []Command {
	return []Command{
		{Name: "ping", Help: "check that the bot is listening", Handle: func(context.Context, *irc.MessageEvent, string) (string, error) { return "pong", nil }},
		{Name: "calc", Help: "arithmetic with + - * / and parentheses", Handle: func(_ context.Context, _ *irc.MessageEvent, input string) (string, error) {
			if len(input) == 0 || len(input) > 256 {
				return "", fmt.Errorf("expression must be 1-256 bytes")
			}
			expression, err := parseArithmetic(input)
			if err != nil {
				return "", fmt.Errorf("invalid arithmetic")
			}
			result, err := arithmetic(expression, 0)
			if err != nil {
				return "", err
			}
			return strconv.FormatFloat(result, 'g', -1, 64), nil
		}},
	}
}

// expr is an arithmetic expression tree: a number literal, a parenthesized
// expression, a unary sign or a binary operation. It has the shape Go's
// parser gives the same text, so nesting depth counts the same way.
type expr struct {
	op          byte // 'n' literal, '(' parentheses, or an operator
	literal     string
	left, right *expr // right is unset for parentheses and unary signs
}

// parseArithmetic parses + - * / with Go's precedence and associativity,
// unary + and -, parentheses and Go number literals.
func parseArithmetic(input string) (*expr, error) {
	p := arithmeticParser{input: input}
	e, err := p.binary(1)
	if p.next(); err == nil && p.pos != len(input) {
		err = fmt.Errorf("unexpected %q", input[p.pos:])
	}
	return e, err
}

type arithmeticParser struct {
	input string
	pos   int
}

// next skips spaces and returns the next byte without consuming it, or 0 at
// the end of input.
func (p *arithmeticParser) next() byte {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t' || p.input[p.pos] == '\r') {
		p.pos++
	}
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

// binary parses operators of at least precedence level: 1 for + and -,
// 2 for * and /. ++ and -- are Go tokens of their own, never two signs.
func (p *arithmeticParser) binary(level int) (*expr, error) {
	if level > 2 {
		return p.unary()
	}
	left, err := p.binary(level + 1)
	for err == nil {
		op := p.next()
		if level == 1 && op != '+' && op != '-' || level == 2 && op != '*' && op != '/' {
			break
		}
		if p.pos+1 < len(p.input) && (op == '+' || op == '-') && p.input[p.pos+1] == op {
			return nil, fmt.Errorf("unexpected %c%c", op, op)
		}
		p.pos++
		var right *expr
		if right, err = p.binary(level + 1); err == nil {
			left = &expr{op: op, left: left, right: right}
		}
	}
	return left, err
}

func (p *arithmeticParser) unary() (*expr, error) {
	switch c := p.next(); {
	case c == '+' || c == '-':
		if p.pos+1 < len(p.input) && p.input[p.pos+1] == c {
			return nil, fmt.Errorf("unexpected %c%c", c, c)
		}
		p.pos++
		operand, err := p.unary()
		return &expr{op: c, left: operand}, err
	case c == '(':
		p.pos++
		inner, err := p.binary(1)
		if err == nil && p.next() != ')' {
			err = fmt.Errorf("missing )")
		}
		if err == nil {
			p.pos++
		}
		return &expr{op: '(', left: inner}, err
	case c >= '0' && c <= '9' || c == '.' && p.pos+1 < len(p.input) && p.input[p.pos+1] >= '0' && p.input[p.pos+1] <= '9':
		start := p.pos
		if !p.number() {
			return nil, fmt.Errorf("invalid number literal %q", p.input[start:p.pos])
		}
		return &expr{op: 'n', literal: p.input[start:p.pos]}, nil
	}
	return nil, fmt.Errorf("expected a number")
}

// number scans a number literal as Go's scanner does and reports whether Go
// would accept it as an integer or floating-point literal.
func (p *arithmeticParser) number() bool {
	start := p.pos
	at := func() byte {
		if p.pos < len(p.input) {
			return p.input[p.pos]
		}
		return 0
	}
	lower := func(c byte) byte { return c | 0x20 }
	valid, float := true, false
	base, prefix := 10, byte(0)
	digits, separators, invalidDigit := false, false, false
	scan := func(base int) {
		for c := at(); c == '_' || c >= '0' && c <= '9' || base == 16 && lower(c) >= 'a' && lower(c) <= 'f'; c = at() {
			if c == '_' {
				separators = true
			} else {
				digits = true
				if base < 10 && c >= byte('0'+base) {
					invalidDigit = true
				}
			}
			p.pos++
		}
	}
	if at() != '.' {
		if at() == '0' {
			p.pos++
			switch lower(at()) {
			case 'x':
				p.pos++
				base, prefix = 16, 'x'
			case 'o':
				p.pos++
				base, prefix = 8, 'o'
			case 'b':
				p.pos++
				base, prefix = 2, 'b'
			default:
				base, prefix, digits = 8, '0', true
			}
		}
		scan(base)
	}
	if at() == '.' {
		float = true
		valid = valid && prefix != 'o' && prefix != 'b'
		p.pos++
		scan(base)
	}
	valid = valid && digits
	if e := lower(at()); e == 'e' || e == 'p' {
		valid = valid && (e == 'e' && (prefix == 0 || prefix == '0') || e == 'p' && prefix == 'x')
		p.pos++
		float = true
		if at() == '+' || at() == '-' {
			p.pos++
		}
		digits = false
		scan(10)
		valid = valid && digits
	} else if prefix == 'x' && float {
		valid = false
	}
	if at() == 'i' {
		p.pos++
		valid = false // an imaginary literal, which is not arithmetic here
	}
	if !float && invalidDigit {
		valid = false
	}
	return valid && (!separators || separatedDigits(p.input[start:p.pos]))
}

// separatedDigits reports whether every '_' in a literal sits between two
// digits, or between a base prefix and a digit.
func separatedDigits(literal string) bool {
	hex := false
	previous := byte('.') // '_', '0' for a digit, or '.' for anything else
	i := 0
	if len(literal) >= 2 && literal[0] == '0' && strings.IndexByte("xXoObB", literal[1]) >= 0 {
		hex = literal[1]|0x20 == 'x'
		previous, i = '0', 2
	}
	for ; i < len(literal); i++ {
		c := literal[i]
		switch {
		case c == '_':
			if previous != '0' {
				return false
			}
			previous = '_'
		case c >= '0' && c <= '9' || hex && c|0x20 >= 'a' && c|0x20 <= 'f':
			previous = '0'
		default:
			if previous == '_' {
				return false
			}
			previous = '.'
		}
	}
	return previous != '_'
}

func arithmetic(e *expr, depth int) (float64, error) {
	if depth > 32 {
		return 0, fmt.Errorf("expression is too deeply nested")
	}
	var result float64
	switch {
	case e.op == 'n':
		n, err := strconv.ParseFloat(e.literal, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number")
		}
		result = n
	case e.op == '(':
		return arithmetic(e.left, depth+1)
	case e.right == nil:
		n, err := arithmetic(e.left, depth+1)
		if err != nil {
			return 0, err
		}
		result = n
		if e.op == '-' {
			result = -n
		}
	default:
		a, err := arithmetic(e.left, depth+1)
		if err != nil {
			return 0, err
		}
		b, err := arithmetic(e.right, depth+1)
		if err != nil {
			return 0, err
		}
		switch e.op {
		case '+':
			result = a + b
		case '-':
			result = a - b
		case '*':
			result = a * b
		case '/':
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			result = a / b
		}
	}
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, fmt.Errorf("non-finite result")
	}
	return result, nil
}
