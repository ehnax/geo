package common

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Report turns a list of problems into one error, so a build explains every fault at once.
func Report(name string, problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	shown := problems
	if len(shown) > 25 {
		shown = shown[:25]
	}
	return fmt.Errorf("%s failed verification:\n  - %s\n  (%d problem(s) total, nothing was written)",
		name, strings.Join(shown, "\n  - "), len(problems))
}

// Sample renders a list for a log line, truncated with a count of what is left.
func Sample(v []string, n int) string {
	if len(v) <= n {
		return strings.Join(v, ", ")
	}
	return strings.Join(v[:n], ", ") + fmt.Sprintf(", ... (%d more)", len(v)-n)
}

// WriteLines writes LF lines, no BOM: the parity test compares these bytes against the
// Python reference, so the encoding is not negotiable.
func WriteLines(path string, lines []string) error {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return nil
}

// FirstDiff describes the first differing byte of two blobs, for a test failure message.
func FirstDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 24
			if lo < 0 {
				lo = 0
			}
			return "  first difference at byte " + itoa(i) +
				"\n    go:     " + safe(a[lo:min(len(a), i+24)]) +
				"\n    python: " + safe(b[lo:min(len(b), i+24)])
		}
	}
	if len(a) != len(b) {
		return "  one file ends at byte " + itoa(min(len(a), len(b)))
	}
	return "  the files are identical"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// safe renders bytes as text with the unprintable ones escaped.
func safe(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			fmt.Fprintf(&out, "\\x%02x", c)
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
