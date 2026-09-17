package fundamentals

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// The model can read the filings but it does the arithmetic in its head, and
// that is where the errors are. A tag it cannot find is an absence the reader
// can see; a margin off by a factor of ten is a confident sentence that looks
// exactly like a correct one.
//
// So the ratios are computed here instead. This is a small calculator the model
// calls with an expression and gets an exact answer back. It is deliberately
// not a general language: no variables, no assignment, no loops, nothing that
// could run for an unbounded time or reach outside itself. Arithmetic over
// numbers the model has already read, and nothing else.
//
// It reads the notation the tables are written in -- 1.2bn, 43,500, 12% -- so
// that a figure can be copied from a column into a calculation without being
// rewritten first, which is itself a step where digits get lost.

// Compute evaluates an arithmetic expression and returns its value.
func Compute(expression string) (float64, error) {
	p := &parser{input: []rune(expression)}
	p.skipSpace()
	if p.done() {
		return 0, fmt.Errorf("there is nothing to calculate")
	}

	v, err := p.expression()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if !p.done() {
		return 0, fmt.Errorf("I could not read %q as part of the calculation", string(p.input[p.at:]))
	}
	if math.IsNaN(v) {
		return 0, fmt.Errorf("the calculation has no numeric answer")
	}
	if math.IsInf(v, 0) {
		return 0, fmt.Errorf("the calculation overflowed")
	}
	return v, nil
}

// functions are the operations worth having beyond the four rules. Each is one
// a reading of accounts actually needs, and each takes its arguments in the
// order the accounts present them.
var functions = map[string]struct {
	min, max int
	apply    func(args []float64) (float64, error)
}{
	"abs":  {1, 1, func(a []float64) (float64, error) { return math.Abs(a[0]), nil }},
	"sqrt": {1, 1, func(a []float64) (float64, error) { return sqrt(a[0]) }},
	"ln":   {1, 1, func(a []float64) (float64, error) { return ln(a[0]) }},
	"min":  {1, 32, func(a []float64) (float64, error) { return fold(a, math.Min), nil }},
	"max":  {1, 32, func(a []float64) (float64, error) { return fold(a, math.Max), nil }},
	"sum":  {1, 32, func(a []float64) (float64, error) { return fold(a, func(x, y float64) float64 { return x + y }), nil }},
	"avg": {1, 32, func(a []float64) (float64, error) {
		return fold(a, func(x, y float64) float64 { return x + y }) / float64(len(a)), nil
	}},
	"pow": {2, 2, func(a []float64) (float64, error) { return math.Pow(a[0], a[1]), nil }},

	// growth is a change as a percentage: growth(earlier, later). The order
	// matters and is the order a table reads in, oldest on the left.
	"growth": {2, 2, func(a []float64) (float64, error) { return growth(a[0], a[1]) }},

	// cagr is a compound annual rate over a number of years, as a percentage:
	// cagr(first, last, years). Four years of history span three years of
	// growth, and getting that wrong is the usual way this figure is overstated.
	"cagr": {3, 3, func(a []float64) (float64, error) { return cagr(a[0], a[1], a[2]) }},

	// round is for presentation only. Nothing here rounds on its own: a ratio
	// cut to two places and then multiplied is a different number.
	"round": {2, 2, func(a []float64) (float64, error) { return round(a[0], a[1]) }},
}

func fold(args []float64, f func(a, b float64) float64) float64 {
	out := args[0]
	for _, a := range args[1:] {
		out = f(out, a)
	}
	return out
}

func sqrt(x float64) (float64, error) {
	if x < 0 {
		return 0, fmt.Errorf("no square root of a negative number (%s)", Number(x))
	}
	return math.Sqrt(x), nil
}

func ln(x float64) (float64, error) {
	if x <= 0 {
		return 0, fmt.Errorf("no logarithm of %s", Number(x))
	}
	return math.Log(x), nil
}

func growth(from, to float64) (float64, error) {
	if from == 0 {
		return 0, fmt.Errorf("growth from zero has no percentage")
	}
	// From a negative base a percentage change is not meaningful: a loss that
	// halves and a loss that doubles produce the same sign of answer.
	if from < 0 {
		return 0, fmt.Errorf("growth from a negative figure (%s) cannot be stated as a percentage; give the movement itself", Number(from))
	}
	return (to - from) / from * 100, nil
}

func cagr(first, last, years float64) (float64, error) {
	if first <= 0 || last <= 0 {
		return 0, fmt.Errorf("a compound rate needs both figures positive")
	}
	if years <= 0 {
		return 0, fmt.Errorf("a compound rate needs a positive number of years")
	}
	return (math.Pow(last/first, 1/years) - 1) * 100, nil
}

func round(x, places float64) (float64, error) {
	if places < 0 || places > 12 {
		return 0, fmt.Errorf("round to between 0 and 12 places")
	}
	scale := math.Pow(10, places)
	return math.Round(x*scale) / scale, nil
}

// scales are the suffixes the tables are written with, so a figure can be
// copied across as it appears rather than expanded by hand.
var scales = []struct {
	suffix string
	factor float64
}{
	{"tn", 1e12},
	{"bn", 1e9},
	{"m", 1e6},
	{"k", 1e3},
}

type parser struct {
	input []rune
	at    int
}

func (p *parser) expression() (float64, error) {
	left, err := p.term()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		switch p.peek() {
		case '+':
			p.at++
			right, err := p.term()
			if err != nil {
				return 0, err
			}
			left += right
		case '-':
			p.at++
			right, err := p.term()
			if err != nil {
				return 0, err
			}
			left -= right
		default:
			return left, nil
		}
	}
}

func (p *parser) term() (float64, error) {
	left, err := p.power()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		switch p.peek() {
		case '*':
			p.at++
			right, err := p.power()
			if err != nil {
				return 0, err
			}
			left *= right
		case '/':
			p.at++
			right, err := p.power()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("division by zero: the figure on the right is zero, which usually means the company does not report it")
			}
			left /= right
		default:
			return left, nil
		}
	}
}

// power binds tighter than multiplication and associates to the right, so
// 2^3^2 is 2^9 as it is written on paper.
func (p *parser) power() (float64, error) {
	base, err := p.unary()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.peek() != '^' {
		return base, nil
	}
	p.at++
	exponent, err := p.power()
	if err != nil {
		return 0, err
	}
	return math.Pow(base, exponent), nil
}

func (p *parser) unary() (float64, error) {
	p.skipSpace()
	switch p.peek() {
	case '-':
		p.at++
		v, err := p.unary()
		return -v, err
	case '+':
		p.at++
		return p.unary()
	}
	return p.primary()
}

func (p *parser) primary() (float64, error) {
	p.skipSpace()
	if p.done() {
		return 0, fmt.Errorf("the calculation ends where a number was expected")
	}

	switch r := p.peek(); {
	case r == '(':
		p.at++
		v, err := p.expression()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.peek() != ')' {
			return 0, fmt.Errorf("a bracket was opened and never closed")
		}
		p.at++
		return p.suffix(v)

	case unicode.IsLetter(r):
		return p.call()

	case unicode.IsDigit(r) || r == '.':
		return p.number()
	}
	return 0, fmt.Errorf("I could not read %q as a number", string(p.peek()))
}

func (p *parser) call() (float64, error) {
	start := p.at
	for !p.done() && (unicode.IsLetter(p.peek()) || unicode.IsDigit(p.peek()) || p.peek() == '_') {
		p.at++
	}
	name := strings.ToLower(string(p.input[start:p.at]))

	fn, ok := functions[name]
	if !ok {
		return 0, fmt.Errorf("there is no %s here; available are %s", name, names())
	}
	p.skipSpace()
	if p.peek() != '(' {
		return 0, fmt.Errorf("%s needs brackets around its figures", name)
	}
	p.at++

	var args []float64
	for {
		v, err := p.expression()
		if err != nil {
			return 0, err
		}
		args = append(args, v)
		p.skipSpace()
		if p.peek() == ',' {
			p.at++
			continue
		}
		break
	}
	if p.peek() != ')' {
		return 0, fmt.Errorf("%s has a bracket left open", name)
	}
	p.at++

	if len(args) < fn.min || len(args) > fn.max {
		return 0, fmt.Errorf("%s takes %s, and was given %d", name, arity(fn.min, fn.max), len(args))
	}
	v, err := fn.apply(args)
	if err != nil {
		return 0, err
	}
	return p.suffix(v)
}

// number reads a figure as the tables write them: digits, an optional decimal
// part, thousands separators, and a scale suffix.
func (p *parser) number() (float64, error) {
	var b strings.Builder
	for !p.done() {
		r := p.peek()
		switch {
		case unicode.IsDigit(r) || r == '.':
			b.WriteRune(r)
			p.at++
		case r == '_':
			p.at++
		case r == ',' && p.thousands():
			p.at++
		default:
			r = 0
		}
		if r == 0 {
			break
		}
	}

	v, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		return 0, fmt.Errorf("I could not read %q as a number", b.String())
	}
	return p.suffix(v)
}

// thousands reports whether the comma at the cursor is a digit separator --
// exactly three digits follow it and no more -- rather than the comma between
// two arguments of a function.
func (p *parser) thousands() bool {
	for i := 1; i <= 3; i++ {
		if p.at+i >= len(p.input) || !unicode.IsDigit(p.input[p.at+i]) {
			return false
		}
	}
	return p.at+4 >= len(p.input) || !unicode.IsDigit(p.input[p.at+4])
}

// suffix applies a scale or a percent sign following a value.
func (p *parser) suffix(v float64) (float64, error) {
	for _, s := range scales {
		if p.hasWord(s.suffix) {
			p.at += len([]rune(s.suffix))
			return v * s.factor, nil
		}
	}
	if p.peek() == '%' {
		p.at++
		return v / 100, nil
	}
	return v, nil
}

// hasWord reports whether a scale suffix stands at the cursor and is not the
// start of a longer word, so 3m is three million and 3max is an error.
func (p *parser) hasWord(word string) bool {
	rs := []rune(word)
	if p.at+len(rs) > len(p.input) {
		return false
	}
	for i, r := range rs {
		if unicode.ToLower(p.input[p.at+i]) != r {
			return false
		}
	}
	next := p.at + len(rs)
	return next >= len(p.input) || !unicode.IsLetter(p.input[next])
}

func (p *parser) skipSpace() {
	for !p.done() && unicode.IsSpace(p.peek()) {
		p.at++
	}
}

func (p *parser) peek() rune {
	if p.done() {
		return 0
	}
	return p.input[p.at]
}

func (p *parser) done() bool { return p.at >= len(p.input) }

func arity(min, max int) string {
	if min == max {
		return fmt.Sprintf("%d figures", min)
	}
	return fmt.Sprintf("between %d and %d figures", min, max)
}

func names() string {
	out := make([]string, 0, len(functions))
	for name := range functions {
		out = append(out, name)
	}
	// Sorted so the error message reads the same way every time.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return strings.Join(out, ", ")
}

// Number formats a result for reading: grouped in thousands, with enough
// decimals to be exact on a ratio and not so many as to imply false precision
// on a total.
func Number(v float64) string {
	switch abs := math.Abs(v); {
	case v == 0:
		return "0"
	case abs >= 1e15 || abs < 1e-4:
		return strconv.FormatFloat(v, 'g', 6, 64)
	case abs >= 1000:
		return group(strconv.FormatFloat(v, 'f', 2, 64))
	case abs >= 1:
		return trim(strconv.FormatFloat(v, 'f', 4, 64))
	default:
		return trim(strconv.FormatFloat(v, 'f', 6, 64))
	}
}

// trim drops trailing zeros so 1.5000 reads as 1.5, while 1.0 keeps a decimal
// point to show it is a computed figure rather than a count.
func trim(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// group puts separators into the integer part, which is what makes the
// difference between millions and billions visible at a glance.
func group(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, fraction, _ := strings.Cut(s, ".")
	fraction = strings.TrimRight(fraction, "0")

	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if fraction != "" {
		b.WriteByte('.')
		b.WriteString(fraction)
	}
	return sign + b.String()
}
