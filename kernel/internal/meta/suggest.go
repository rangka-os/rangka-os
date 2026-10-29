package meta

import "sort"

// closeMatch ports Python's difflib.get_close_matches(word, choices, n=1, cutoff=0.6)[0].
func closeMatch(word string, choices []string) (string, bool) {
	best, bestScore := "", -1.0
	b := []rune(word)
	for _, c := range choices {
		a := []rune(c)
		r := ratio(a, b)
		if r < 0.6 {
			continue
		}
		if r > bestScore || (r == bestScore && c > best) {
			best, bestScore = c, r
		}
	}
	return best, bestScore >= 0
}

// suggest returns `; did you mean "x"?` like rangka_validate.py suggest().
func suggest(name string, choices []string) string {
	if m, ok := closeMatch(name, choices); ok {
		return `; did you mean "` + m + `"?`
	}
	return ""
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ratio is difflib.SequenceMatcher(None, a, b).ratio() without junk heuristics (strings < 200 chars).
func ratio(a, b []rune) float64 {
	la, lb := len(a), len(b)
	if la+lb == 0 {
		return 1
	}
	b2j := map[rune][]int{}
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	longest := func(alo, ahi, blo, bhi int) (int, int, int) {
		besti, bestj, bestsize := alo, blo, 0
		j2len := map[int]int{}
		for i := alo; i < ahi; i++ {
			newj2len := map[int]int{}
			for _, j := range b2j[a[i]] {
				if j < blo {
					continue
				}
				if j >= bhi {
					break
				}
				k := j2len[j-1] + 1
				newj2len[j] = k
				if k > bestsize {
					besti, bestj, bestsize = i-k+1, j-k+1, k
				}
			}
			j2len = newj2len
		}
		return besti, bestj, bestsize
	}
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, la, 0, lb}}
	matches := 0
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := longest(q.alo, q.ahi, q.blo, q.bhi)
		if k > 0 {
			matches += k
			if q.alo < i && q.blo < j {
				queue = append(queue, span{q.alo, i, q.blo, j})
			}
			if i+k < q.ahi && j+k < q.bhi {
				queue = append(queue, span{i + k, q.ahi, j + k, q.bhi})
			}
		}
	}
	return 2.0 * float64(matches) / float64(la+lb)
}
