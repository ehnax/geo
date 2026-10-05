package common

import (
	"fmt"
	"math/bits"
	"os"
	"sort"
	"strings"
)

// IPRange is a closed interval of IPv4 addresses; both Russian feeds publish start/end pairs.
type IPRange struct{ Lo, Hi uint32 }

// Interval builds a closed interval. A constructor because go vet rejects an unkeyed
// IPRange literal across a package boundary.
func Interval(lo, hi uint32) IPRange { return IPRange{Lo: lo, Hi: hi} }

// Size is how many addresses the interval holds.
func (r IPRange) Size() uint64 { return uint64(r.Hi) - uint64(r.Lo) + 1 }

// CIDRRange turns a block into the interval it covers.
func CIDRRange(c CIDR) IPRange { return IPRange{Lo: c.Addr, Hi: c.End()} }

// MergeRanges sorts and joins ranges that overlap or touch; adjacent intervals are one
// continuous piece and splitting it buys nothing.
func MergeRanges(in []IPRange) []IPRange {
	if len(in) == 0 {
		return nil
	}
	ordered := make([]IPRange, len(in))
	copy(ordered, in)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Lo != ordered[j].Lo {
			return ordered[i].Lo < ordered[j].Lo
		}
		return ordered[i].Hi < ordered[j].Hi
	})

	out := []IPRange{ordered[0]}
	for _, r := range ordered[1:] {
		last := &out[len(out)-1]
		// Guard the +1: hi is a uint32, and 0xffffffff+1 wraps to 0 and would fuse the
		// last address to the first.
		touches := r.Lo <= last.Hi || (last.Hi != ^uint32(0) && r.Lo == last.Hi+1)
		if touches {
			if r.Hi > last.Hi {
				last.Hi = r.Hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// Summarize covers [lo,hi] with the fewest blocks, taking at each step the largest
// block aligned to lo inside the remainder. l and h are uint64: lo=0 aligns to 2**32.
func Summarize(lo, hi uint32) []CIDR {
	var out []CIDR
	l, h := uint64(lo), uint64(hi)
	for l <= h {
		align := uint64(1) << 32
		if l != 0 {
			align = l & (-l)
		}
		span := h - l + 1
		size := align
		for size > span {
			size >>= 1
		}
		out = append(out, CIDR{Addr: uint32(l), Bits: uint8(32 - (bits.Len64(size) - 1))})
		l += size
	}
	return out
}

// CollapseRanges joins the intervals first, so duplicates and overlaps cannot survive.
func CollapseRanges(in []IPRange) []CIDR {
	var out []CIDR
	for _, r := range MergeRanges(in) {
		out = append(out, Summarize(r.Lo, r.Hi)...)
	}
	SortCIDR(out)
	return out
}

// RangesTotal is how many addresses a set of intervals covers once joined.
func RangesTotal(in []IPRange) uint64 {
	var n uint64
	for _, r := range MergeRanges(in) {
		n += r.Size()
	}
	return n
}

// BlocksTotal is how many addresses a block set covers.
func BlocksTotal(in []CIDR) uint64 {
	var n uint64
	for _, c := range in {
		n += uint64(1) << (32 - c.Bits)
	}
	return n
}

// ParseZoneLines reads the ipdeny format: one CIDR or bare address per line, comments
// start with #, // or ;. An unparsable line is skipped, not fatal, and not counted as v6.
func ParseZoneLines(lines []string) (out []IPRange, v6 int) {
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == '/' || line[0] == ';' {
			continue
		}
		if strings.Contains(line, ":") {
			v6++
			continue
		}
		c, err := ParseCIDR(line)
		if err != nil {
			continue
		}
		out = append(out, CIDRRange(c))
	}
	return out, v6
}

// ParseZoneFile is ParseZoneLines over a file; a file that parses to nothing is a wrong
// URL, not an empty country.
func ParseZoneFile(path string) ([]IPRange, int, error) {
	lines, err := ReadLines(path)
	if err != nil {
		return nil, 0, err
	}
	out, v6 := ParseZoneLines(lines)
	if len(out) == 0 {
		return nil, v6, fmt.Errorf("%s: parsed, but holds no IPv4 ranges", path)
	}
	return out, v6, nil
}

// ReadLines reads a text file into lines, without the trailing newline.
func ReadLines(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), nil
}
