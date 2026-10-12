package bot

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"strings"
	"testing"
)

// goArithmetic is the calculator as it was when it used Go's parser. It is the
// reference the hand-written parser is checked against.
func goArithmetic(input string) (string, error) {
	expression, err := parser.ParseExpr(input)
	if err != nil {
		return "", fmt.Errorf("invalid arithmetic")
	}
	var eval func(ast.Expr, int) (float64, error)
	eval = func(e ast.Expr, depth int) (float64, error) {
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
			return eval(e.X, depth+1)
		case *ast.UnaryExpr:
			n, err := eval(e.X, depth+1)
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
			a, err := eval(e.X, depth+1)
			if err != nil {
				return 0, err
			}
			b, err := eval(e.Y, depth+1)
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
	result, err := eval(expression, 0)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(result, 'g', -1, 64), nil
}

// A failure the reference reports only because Go's parser accepts more
// than arithmetic (identifiers, other operators, other literals) may have
// another message; the hand-written parser calls such text invalid.
func compareCalculators(t *testing.T, input string) {
	t.Helper()
	if len(input) == 0 || len(input) > 256 || strings.Contains(input, "\n") || strings.Contains(input, "//") || strings.Contains(input, "/*") {
		return // line breaks and comments: Go syntax a chat line does not carry
	}
	want, wantErr := goArithmetic(input)
	got, gotErr := UtilityCommands()[1].Handle(context.Background(), nil, input)
	if (gotErr != nil) != (wantErr != nil) || got != want {
		t.Fatalf("%q: got %q, %v; Go parser %q, %v", input, got, gotErr, want, wantErr)
	}
	if gotErr != nil && gotErr.Error() != wantErr.Error() && gotErr.Error() != "invalid arithmetic" {
		t.Fatalf("%q: error %v, Go parser %v", input, gotErr, wantErr)
	}
}

func TestCalculatorMatchesGoParser(t *testing.T) {
	for _, input := range []string{
		"(2+3)*4", "1/0", "1e308*1e308", "2-3-4", "2/4/8", "-2*-3", "+5", "- -5", "--5", "5--5", "5++5",
		"1.5e3", "1e-3", ".5", "5.", "0x1p-2", "0x10", "1_000", "017", "0b101", "1.2.3", "1e", "3 * (4 - (2))",
		"((((1))))", strings.Repeat("(", 33) + "1" + strings.Repeat(")", 33), strings.Repeat("1+", 33) + "1",
		strings.Repeat("-", 1) + strings.Repeat(" -", 32) + "1", "1 +", "* 2", "()", "(1", "1)", "2 3", "\t7\t",
		"1%2", "a", "os.Exit(1)", "'a'", `"1"`, "1e400", "0/0", "2*(3+",
	} {
		compareCalculators(t, input)
	}
}

func FuzzCalculatorMatchesGoParser(f *testing.F) {
	for _, seed := range []string{"(2+3)*4", "1e-3/7", "-(0x1p4)", "1_0.5e+2*-.25", "2--2"} {
		f.Add(seed)
	}
	f.Fuzz(compareCalculators)
}
