package imports

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

var (
	reID    = regexp.MustCompile(`^[1-9]\d{0,2}(\.\d{3})+(,\d+)?$`)
	reEN    = regexp.MustCompile(`^[1-9]\d{0,2}(,\d{3})+(\.\d+)?$`)
	rePlain = regexp.MustCompile(`^\d+([.,]\d+)?$`)

	reDMY    = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	reISO    = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	reSerial = regexp.MustCompile(`^(\d{1,7})(?:\.(\d+))?$`)
	reExp    = regexp.MustCompile(`^(\d+)(?:\.(\d+))?[eE]([+-]?\d+)$`)
	reTime   = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
)

const maxExcelSerial = 2958465

// Convert turns a spreadsheet cell into a value for the engine input. The second result is an
// Indonesian error message, "" when the cell is fine. An empty cell gives nil, "".
// Money and decimals never pass through float64: currency becomes int64, other numbers a decimal string.
func Convert(f *meta.Field, column, cell string) (any, string) {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return nil, ""
	}
	bad := func(format string) (any, string) {
		return nil, fmt.Sprintf(format, column, cell)
	}
	switch f.Type {
	case "int", "currency", "rating":
		i, frac, ok := parseNumber(cell)
		if !ok {
			return bad(`Kolom "%s" harus angka, isinya "%s"`)
		}
		if strings.Trim(frac, "0") != "" {
			if f.Type == "currency" {
				return bad(`Kolom "%s" harus rupiah bulat, isinya "%s"`)
			}
			return bad(`Kolom "%s" harus bilangan bulat, isinya "%s"`)
		}
		n, err := strconv.ParseInt(i, 10, 64)
		if err != nil {
			return bad(`Kolom "%s" harus angka, isinya "%s"`)
		}
		return n, ""
	case "float", "percent":
		i, frac, ok := parseNumber(cell)
		if !ok {
			return bad(`Kolom "%s" harus angka, isinya "%s"`)
		}
		if frac = strings.TrimRight(frac, "0"); frac != "" {
			return i + "." + frac, ""
		}
		return i, ""
	case "date":
		d, _, ok := parseDate(cell, false)
		if !ok {
			return bad(`Kolom "%s" harus tanggal (dd/mm/yyyy), isinya "%s"`)
		}
		return d, ""
	case "datetime":
		d, tm, ok := parseDate(cell, true)
		if !ok {
			return bad(`Kolom "%s" harus tanggal (dd/mm/yyyy), isinya "%s"`)
		}
		return d + "T" + tm, ""
	case "check":
		switch strings.ToLower(cell) {
		case "1", "ya", "true", "y":
			return true, ""
		case "0", "tidak", "false", "n":
			return false, ""
		}
		return bad(`Kolom "%s" harus ya atau tidak, isinya "%s"`)
	}
	return cell, ""
}

// parseNumber splits an Indonesian, English or plain number into a signed integer part and a
// fraction (digits only). Prefixes Rp/IDR, spaces and (…) negatives are accepted.
func parseNumber(s string) (intPart, frac string, ok bool) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	for _, p := range []string{"rp", "idr"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	if strings.HasPrefix(s, "-") {
		neg = !neg
		s = strings.TrimSpace(s[1:])
	}
	s = strings.ReplaceAll(s, " ", "")
	switch {
	case reExp.MatchString(s):
		m := reExp.FindStringSubmatch(s)
		e, err := strconv.Atoi(m[3])
		if err != nil || e > 30 || e < -30 {
			return "", "", false
		}
		digits := m[1] + m[2]
		point := len(m[1]) + e
		switch {
		case point <= 0:
			s = "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			s = digits + strings.Repeat("0", point-len(digits))
		default:
			s = digits[:point] + "." + digits[point:]
		}
	case reID.MatchString(s):
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	case reEN.MatchString(s):
		s = strings.ReplaceAll(s, ",", "")
	case rePlain.MatchString(s):
		s = strings.Replace(s, ",", ".", 1)
	default:
		return "", "", false
	}
	intPart, frac, _ = strings.Cut(s, ".")
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if neg && (intPart != "0" || strings.Trim(frac, "0") != "") {
		intPart = "-" + intPart
	}
	return intPart, frac, true
}

// excelDate converts an Excel serial day number (epoch 1899-12-30) to yyyy-mm-dd.
func excelDate(serial int) string {
	return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, serial).Format("2006-01-02")
}

// parseClock validates HH:MM[:SS] and returns HH:MM:SS.
func parseClock(s string) (string, bool) {
	m := reTime.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec := 0
	if m[3] != "" {
		sec, _ = strconv.Atoi(m[3])
	}
	if h > 23 || mi > 59 || sec > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, mi, sec), true
}

// parseDate returns the date (yyyy-mm-dd) and time (HH:MM:SS) of a cell. A time part is only
// accepted when withTime is set. Excel serials may carry a day fraction (the time of day).
func parseDate(s string, withTime bool) (date, clock string, ok bool) {
	clock = "00:00:00"
	datePart := s
	if withTime {
		if d, t, found := strings.Cut(s, " "); found {
			datePart = d
			if clock, ok = parseClock(strings.TrimSpace(t)); !ok {
				return "", "", false
			}
		} else if d, t, found := strings.Cut(s, "T"); found {
			datePart = d
			if clock, ok = parseClock(t); !ok {
				return "", "", false
			}
		}
	}
	var y, mo, d int
	if m := reDMY.FindStringSubmatch(datePart); m != nil {
		d, _ = strconv.Atoi(m[1])
		mo, _ = strconv.Atoi(m[2])
		y, _ = strconv.Atoi(m[3])
	} else if m := reISO.FindStringSubmatch(datePart); m != nil {
		y, _ = strconv.Atoi(m[1])
		mo, _ = strconv.Atoi(m[2])
		d, _ = strconv.Atoi(m[3])
	} else if m := reSerial.FindStringSubmatch(datePart); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > maxExcelSerial {
			return "", "", false
		}
		if withTime && m[2] != "" {
			// Day fraction to whole seconds with integer maths (no float64).
			fr := m[2]
			if len(fr) > 9 {
				fr = fr[:9]
			}
			num, _ := strconv.ParseInt(fr, 10, 64)
			den := int64(1)
			for range fr {
				den *= 10
			}
			secs := (num*86400*2 + den) / (2 * den)
			if secs >= 86400 {
				secs -= 86400
				n++
			}
			clock = fmt.Sprintf("%02d:%02d:%02d", secs/3600, secs%3600/60, secs%60)
		}
		return excelDate(n), clock, true
	} else {
		return "", "", false
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return "", "", false
	}
	return t.Format("2006-01-02"), clock, true
}
