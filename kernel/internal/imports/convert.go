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
	reID    = regexp.MustCompile(`^\d{1,3}(\.\d{3})+(,\d+)?$`)
	reEN    = regexp.MustCompile(`^\d{1,3}(,\d{3})+(\.\d+)?$`)
	rePlain = regexp.MustCompile(`^\d+([.,]\d+)?$`)

	reDMY    = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	reISO    = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	reSerial = regexp.MustCompile(`^\d{1,7}$`)
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
	case "float", "percent", "decimal":
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

// parseDate returns the date (yyyy-mm-dd) and time (HH:MM:SS) of a cell. A time part is only
// accepted when withTime is set.
func parseDate(s string, withTime bool) (date, clock string, ok bool) {
	clock = "00:00:00"
	datePart := s
	if withTime {
		if d, t, found := strings.Cut(s, " "); found {
			datePart, s = d, strings.TrimSpace(t)
			m := reTime.FindStringSubmatch(s)
			if m == nil {
				return "", "", false
			}
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			sec := 0
			if m[3] != "" {
				sec, _ = strconv.Atoi(m[3])
			}
			if h > 23 || mi > 59 || sec > 59 {
				return "", "", false
			}
			clock = fmt.Sprintf("%02d:%02d:%02d", h, mi, sec)
		} else if d, t, found := strings.Cut(s, "T"); found {
			datePart, s = d, t
			m := reTime.FindStringSubmatch(s)
			if m == nil {
				return "", "", false
			}
			if m[3] == "" {
				m[3] = "00"
			}
			clock = fmt.Sprintf("%02s:%s:%s", m[1], m[2], m[3])
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
	} else if reSerial.MatchString(datePart) {
		n, _ := strconv.Atoi(datePart)
		if n < 1 || n > maxExcelSerial {
			return "", "", false
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
