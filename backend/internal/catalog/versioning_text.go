package catalog

import (
	"encoding/json"
	"sort"
	"strings"
)

type lineEdit struct {
	start, end int
	lines      []string
}

func versionTextPointer(parts []string) bool {
	return len(parts) == 3 && parts[0] == "translations" && parts[1] != "" && parts[2] == "body"
}

func versionLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Myers line diff, with a bounded search budget. Large ambiguous changes stay
// explicit conflicts; the bound never rejects an otherwise legal edit.
func versionLineEdits(base, next []string) ([]lineEdit, bool) {
	n, m := len(base), len(next)
	maxDistance := min(n+m, 1024)
	v := map[int]int{1: 0}
	trace := []map[int]int{}
	work := 0
	for distance := 0; distance <= maxDistance; distance++ {
		snapshot := map[int]int{}
		for key, value := range v {
			snapshot[key] = value
		}
		trace = append(trace, snapshot)
		for k := -distance; k <= distance; k += 2 {
			work++
			if work > 4_000_000 {
				return nil, false
			}
			x := v[k-1] + 1
			if k == -distance || k != distance && v[k-1] < v[k+1] {
				x = v[k+1]
			}
			y := x - k
			for x < n && y < m && base[x] == next[y] {
				x++
				y++
				work++
				if work > 4_000_000 {
					return nil, false
				}
			}
			v[k] = x
			if x >= n && y >= m {
				type step struct {
					kind byte
					line string
				}
				steps := []step{}
				x, y = n, m
				for d := distance; d >= 0; d-- {
					prev := trace[d]
					k = x - y
					prevK := k - 1
					if k == -d || k != d && prev[k-1] < prev[k+1] {
						prevK = k + 1
					}
					px := prev[prevK]
					py := px - prevK
					for x > px && y > py {
						x--
						y--
						steps = append(steps, step{'=', base[x]})
					}
					if d == 0 {
						break
					}
					if x == px {
						y--
						steps = append(steps, step{'+', next[y]})
					} else {
						x--
						steps = append(steps, step{'-', base[x]})
					}
				}
				for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
					steps[i], steps[j] = steps[j], steps[i]
				}
				edits := []lineEdit{}
				position := 0
				var edit *lineEdit
				flush := func() {
					if edit != nil {
						edits = append(edits, *edit)
						edit = nil
					}
				}
				for _, s := range steps {
					if s.kind == '=' {
						flush()
						position++
						continue
					}
					if edit == nil {
						edit = &lineEdit{start: position, end: position, lines: []string{}}
					}
					if s.kind == '-' {
						position++
						edit.end++
					} else {
						edit.lines = append(edit.lines, s.line)
					}
				}
				flush()
				return edits, true
			}
		}
	}
	return nil, false
}

func mergeVersionText(base, current, proposed string) (string, bool) {
	if current == proposed || base == proposed {
		return current, true
	}
	if base == current {
		return proposed, true
	}
	lines := versionLines(base)
	left, ok := versionLineEdits(lines, versionLines(current))
	if !ok {
		return "", false
	}
	right, ok := versionLineEdits(lines, versionLines(proposed))
	if !ok {
		return "", false
	}
	all := append([]lineEdit{}, left...)
	for _, r := range right {
		duplicate := false
		for _, l := range left {
			if l.start == r.start && l.end == r.end && strings.Join(l.lines, "") == strings.Join(r.lines, "") {
				duplicate = true
				break
			}
			overlap := l.start < r.end && r.start < l.end
			if l.start == l.end {
				overlap = overlap || l.start >= r.start && l.start < r.end || r.start == r.end && l.start == r.start
			}
			if r.start == r.end {
				overlap = overlap || r.start >= l.start && r.start < l.end
			}
			if overlap {
				return "", false
			}
		}
		if !duplicate {
			all = append(all, r)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start == all[j].start {
			return all[i].end < all[j].end
		}
		return all[i].start < all[j].start
	})
	var result strings.Builder
	position := 0
	for _, edit := range all {
		if edit.start < position {
			return "", false
		}
		result.WriteString(strings.Join(lines[position:edit.start], ""))
		result.WriteString(strings.Join(edit.lines, ""))
		position = edit.end
	}
	result.WriteString(strings.Join(lines[position:], ""))
	return result.String(), true
}

func mergeVersionTextValue(base, current, proposed json.RawMessage) (json.RawMessage, bool) {
	var b, c, p string
	if json.Unmarshal(base, &b) != nil || json.Unmarshal(current, &c) != nil || json.Unmarshal(proposed, &p) != nil {
		return nil, false
	}
	merged, ok := mergeVersionText(b, c, p)
	if !ok {
		return nil, false
	}
	return json.RawMessage(encode(merged)), true
}

func versionTextContribution(before, after, current, merged json.RawMessage, parts []string) bool {
	if !versionTextPointer(parts) {
		return false
	}
	texts := []string{}
	for _, raw := range []json.RawMessage{before, after, current, merged} {
		value, ok, err := pointerValue(commitMap(raw), parts)
		var text string
		if err != nil || ok && json.Unmarshal(value, &text) != nil {
			return false
		}
		texts = append(texts, text)
	}
	changes, ok := versionLineEdits(versionLines(texts[0]), versionLines(texts[1]))
	if !ok {
		return false
	}
	count := func(text string) map[string]int {
		out := map[string]int{}
		for _, line := range versionLines(text) {
			out[line]++
		}
		return out
	}
	old, final := count(texts[2]), count(texts[3])
	base := versionLines(texts[0])
	for _, change := range changes {
		for _, line := range change.lines {
			if final[line] > old[line] {
				return true
			}
		}
		for _, line := range base[change.start:change.end] {
			if old[line] > final[line] {
				return true
			}
		}
	}
	return false
}
