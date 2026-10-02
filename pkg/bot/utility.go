package bot

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"

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
			expression, err := parser.ParseExpr(input)
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

func arithmetic(e ast.Expr, depth int) (float64, error) {
	if depth > 32 {
		return 0, fmt.Errorf("expression is too deeply nested")
	}
	var result float64
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.INT && e.Kind != token.FLOAT {
			return 0, fmt.Errorf("only numbers are supported")
		}
		n, err := strconv.ParseFloat(e.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number")
		}
		result = n
	case *ast.ParenExpr:
		return arithmetic(e.X, depth+1)
	case *ast.UnaryExpr:
		n, err := arithmetic(e.X, depth+1)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case token.ADD:
			result = n
		case token.SUB:
			result = -n
		default:
			return 0, fmt.Errorf("unsupported operator")
		}
	case *ast.BinaryExpr:
		a, err := arithmetic(e.X, depth+1)
		if err != nil {
			return 0, err
		}
		b, err := arithmetic(e.Y, depth+1)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case token.ADD:
			result = a + b
		case token.SUB:
			result = a - b
		case token.MUL:
			result = a * b
		case token.QUO:
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			result = a / b
		default:
			return 0, fmt.Errorf("unsupported operator")
		}
	default:
		return 0, fmt.Errorf("only arithmetic is supported")
	}
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, fmt.Errorf("non-finite result")
	}
	return result, nil
}
