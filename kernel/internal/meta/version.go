package meta

import (
	"regexp"
	"strconv"
	"strings"
)

// Version ranges between modules (contract 0001).

// versionRe matches a whole version: 1, 1.2 or 1.2.3 (ASCII digits, like rangka_validate.py parse_version).
var versionRe = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?$`)

func parseVersion(x any) ([3]int, bool) {
	s, ok := x.(string)
	if !ok {
		return [3]int{}, false
	}
	m := versionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func lessVersion(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Satisfies reports whether version meets a range: ^a.b (same major, at least a.b) or >=a.b. Other ranges
// (a || b, pre-releases) and unreadable versions are not checked (rangka_validate.py satisfies).
func Satisfies(version, rng any) bool {
	ver, ok := parseVersion(version)
	r, isStr := rng.(string)
	if !ok || !isStr {
		return true
	}
	r = strings.TrimSpace(r)
	switch {
	case strings.HasPrefix(r, "^"):
		b, ok := parseVersion(r[1:])
		return !ok || ver[0] == b[0] && !lessVersion(ver, b)
	case strings.HasPrefix(r, ">="):
		b, ok := parseVersion(r[2:])
		return !ok || !lessVersion(ver, b)
	}
	return true
}
