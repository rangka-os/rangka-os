// Package money is exact 4-decimal arithmetic for amounts (numeric(20,4)); never float.
package money

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Scale is the number of stored decimal places.
const Scale = 4

var (
	ten4   = big.NewInt(10000)
	maxRaw = func() *big.Int { n, _ := new(big.Int).SetString("99999999999999999999", 10); return n }() // 9999999999999999.9999 * 10^4
)

// Decimal is u / 10^Scale; the zero value is 0.
type Decimal struct{ u *big.Int }

func (d Decimal) raw() *big.Int {
	if d.u == nil {
		return new(big.Int)
	}
	return d.u
}

// Parse reads "-"? digits ("." 1-4 digits)? with at most 16 integer digits.
func Parse(s string) (Decimal, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	body := strings.TrimPrefix(s, "-")
	ip, fp, hasDot := strings.Cut(body, ".")
	if ip == "" || len(ip) > 16 || !digits(ip) || (hasDot && (len(fp) < 1 || len(fp) > Scale || !digits(fp))) {
		return Decimal{}, errors.New("money: invalid decimal " + strconv.Quote(s))
	}
	u, _ := new(big.Int).SetString(ip+fp+strings.Repeat("0", Scale-len(fp)), 10)
	if neg {
		u.Neg(u)
	}
	return Decimal{u}, nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// FromInt converts a whole number.
func FromInt(n int64) Decimal {
	return Decimal{new(big.Int).Mul(big.NewInt(n), ten4)}
}

// FromFloat converts via the shortest decimal form; more than 4 dp, NaN and Inf error.
func FromFloat(f float64) (Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, errors.New("money: not a finite number")
	}
	return Parse(strconv.FormatFloat(f, 'f', -1, 64))
}

// Rat returns the exact rational of a Decimal, int64, int, float64 or decimal string.
func Rat(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case Decimal:
		return x.Rat(), true
	case int64:
		return new(big.Rat).SetInt64(x), true
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return new(big.Rat).SetString(strconv.FormatFloat(x, 'f', -1, 64))
	case string:
		d, err := Parse(x)
		if err != nil {
			return nil, false
		}
		return d.Rat(), true
	}
	return nil, false
}

// divRound returns round(n/d) half away from zero (d > 0).
func divRound(n, d *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(new(big.Int).Abs(n), d, new(big.Int))
	if r.Lsh(r, 1).Cmp(d) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if n.Sign() < 0 {
		q.Neg(q)
	}
	return q
}

// FromRat rounds half away from zero to 4 dp; errors when out of range.
func FromRat(r *big.Rat) (Decimal, error) {
	n := new(big.Int).Mul(r.Num(), ten4)
	d := Decimal{divRound(n, r.Denom())}
	if !InRange(d) {
		return Decimal{}, errors.New("money: amount out of range")
	}
	return d, nil
}

// String is canonical: no exponent, no trailing zeros, "0" for zero.
func (d Decimal) String() string {
	u := d.raw()
	s := new(big.Int).Abs(u).String()
	if len(s) <= Scale {
		s = strings.Repeat("0", Scale-len(s)+1) + s
	}
	ip, fp := s[:len(s)-Scale], strings.TrimRight(s[len(s)-Scale:], "0")
	if fp != "" {
		ip += "." + fp
	}
	if u.Sign() < 0 {
		ip = "-" + ip
	}
	return ip
}

// MarshalJSON encodes as a quoted decimal string.
func (d Decimal) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(d.String())), nil }

// Add returns d + o.
func (d Decimal) Add(o Decimal) Decimal { return Decimal{new(big.Int).Add(d.raw(), o.raw())} }

// Sub returns d - o.
func (d Decimal) Sub(o Decimal) Decimal { return Decimal{new(big.Int).Sub(d.raw(), o.raw())} }

// Neg returns -d.
func (d Decimal) Neg() Decimal { return Decimal{new(big.Int).Neg(d.raw())} }

// Cmp compares d and o (-1, 0, 1).
func (d Decimal) Cmp(o Decimal) int { return d.raw().Cmp(o.raw()) }

// Sign is -1, 0 or 1.
func (d Decimal) Sign() int { return d.raw().Sign() }

// IsZero reports d == 0.
func (d Decimal) IsZero() bool { return d.Sign() == 0 }

// IsWhole reports whether d has no fractional part.
func (d Decimal) IsWhole() bool { return new(big.Int).Rem(d.raw(), ten4).Sign() == 0 }

// Round rounds to places (0-4) decimals, half away from zero.
func (d Decimal) Round(places int) Decimal {
	if places < 0 {
		places = 0
	}
	if places >= Scale {
		return d
	}
	f := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(Scale-places)), nil)
	return Decimal{new(big.Int).Mul(divRound(d.raw(), f), f)}
}

// Rat returns the exact rational value.
func (d Decimal) Rat() *big.Rat { return new(big.Rat).SetFrac(new(big.Int).Set(d.raw()), ten4) }

// InRange reports |d| <= 9999999999999999.9999 (numeric(20,4)).
func InRange(d Decimal) bool { return new(big.Int).Abs(d.raw()).Cmp(maxRaw) <= 0 }

// ToIDR returns round(amount x rate) half away from zero; errors when it overflows int64.
func ToIDR(amount Decimal, rate *big.Rat) (int64, error) {
	p := new(big.Rat).Mul(amount.Rat(), rate)
	n := divRound(p.Num(), p.Denom())
	if !n.IsInt64() {
		return 0, errors.New("money: IDR amount overflows int64")
	}
	return n.Int64(), nil
}

// IsIDR reports whether code means rupiah (empty defaults to IDR).
func IsIDR(code string) bool { return code == "" || code == "IDR" }

// ValidCode reports "" or three capital letters.
func ValidCode(code string) bool {
	if code == "" {
		return true
	}
	if len(code) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}
	return true
}
