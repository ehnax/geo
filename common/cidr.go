// Package common is the IPv4 arithmetic and container reading shared by the geoip and
// geosite builds. IPv4 only: a CIDR is a uint32 plus a prefix length.
package common

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// CIDR is an IPv4 network: a masked network address and a prefix length.
type CIDR struct {
	Addr uint32
	Bits uint8
}

// Mask clears the host bits.
func (c CIDR) Mask() CIDR {
	if c.Bits == 0 {
		c.Addr = 0
		return c
	}
	c.Addr &= ^uint32(0) << (32 - c.Bits)
	return c
}

// End is the highest address inside c.
func (c CIDR) End() uint32 {
	return c.Addr | ^uint32(0)>>c.Bits
}

// Parent is one level up; it assumes the dropped bits are already zero.
func (c CIDR) Parent() CIDR {
	if c.Bits == 0 {
		return c
	}
	return CIDR{Addr: c.Addr, Bits: c.Bits - 1}
}

// Sibling is the other half of the parent block, the only block c can merge with.
func (c CIDR) Sibling() CIDR {
	if c.Bits == 0 {
		return c
	}
	return CIDR{Addr: c.Addr ^ uint32(1)<<(31-(c.Bits-1)), Bits: c.Bits}
}

// Contains reports whether other lies entirely inside c.
func (c CIDR) Contains(other CIDR) bool {
	return c.Bits <= other.Bits && c.Addr <= other.Addr && other.Addr <= c.End()
}

func (c CIDR) String() string {
	return fmt.Sprintf("%d.%d.%d.%d/%d", c.Addr>>24, (c.Addr>>16)&0xFF, (c.Addr>>8)&0xFF, c.Addr&0xFF, c.Bits)
}

// ProtoBytes is the 4 byte wire form the GeoIP message stores.
func (c CIDR) ProtoBytes() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, c.Addr)
	return b
}

// SortCIDR orders by address, then by prefix length.
func SortCIDR(c []CIDR) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Addr != c[j].Addr {
			return c[i].Addr < c[j].Addr
		}
		return c[i].Bits < c[j].Bits
	})
}

// CIDRStrings renders a block set, in the order given.
func CIDRStrings(c []CIDR) []string {
	out := make([]string, 0, len(c))
	for _, x := range c {
		out = append(out, x.String())
	}
	return out
}

// mergeSiblings folds sibling halves into parents, marking both as consumed so a
// merged pair is never emitted twice.
func mergeSiblings(set map[CIDR]struct{}) map[CIDR]struct{} {
	work := set
	for {
		keys := make([]CIDR, 0, len(work))
		for c := range work {
			keys = append(keys, c)
		}
		SortCIDR(keys)

		next := make(map[CIDR]struct{}, len(work))
		consumed := make(map[CIDR]struct{}, len(work))
		merged := false
		for _, c := range keys {
			if _, done := consumed[c]; done {
				continue
			}
			if c.Bits == 0 {
				next[c] = struct{}{}
				continue
			}
			if _, ok := work[c.Sibling()]; ok {
				next[c.Parent()] = struct{}{}
				consumed[c] = struct{}{}
				consumed[c.Sibling()] = struct{}{}
				merged = true
				continue
			}
			next[c] = struct{}{}
		}
		work = next
		if !merged {
			return work
		}
	}
}

// dropContained removes blocks nested in another; sorted input keeps the stack linear.
func dropContained(set map[CIDR]struct{}) ([]CIDR, bool) {
	cur := make([]CIDR, 0, len(set))
	for c := range set {
		cur = append(cur, c)
	}
	SortCIDR(cur)

	var kept, live []CIDR
	removed := false
	for _, c := range cur {
		for len(live) > 0 && live[len(live)-1].End() < c.Addr {
			live = live[:len(live)-1]
		}
		contained := false
		for _, k := range live {
			if k.Contains(c) {
				contained = true
				break
			}
		}
		if contained {
			removed = true
			continue
		}
		kept = append(kept, c)
		live = append(live, c)
	}
	return kept, removed
}

// Collapse reduces a block set to a minimal one; a drop cannot create a merge, so
// alternating the two terminates.
func Collapse(in []CIDR) []CIDR {
	set := make(map[CIDR]struct{}, len(in))
	for _, c := range in {
		set[c] = struct{}{}
	}
	for {
		set = mergeSiblings(set)
		kept, removed := dropContained(set)
		set = make(map[CIDR]struct{}, len(kept))
		for _, c := range kept {
			set[c] = struct{}{}
		}
		if !removed {
			break
		}
	}
	out := make([]CIDR, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	SortCIDR(out)
	return out
}

// ParseCIDR takes "10.0.0.0/8" or a bare address; a bad or IPv6 line is an error, not a skip.
func ParseCIDR(s string) (CIDR, error) {
	host, bitsStr, hasSlash := strings.Cut(s, "/")
	ip := net.ParseIP(host)
	if ip == nil {
		return CIDR{}, fmt.Errorf("%q is not an IP address", s)
	}
	v4 := ip.To4()
	if v4 == nil {
		return CIDR{}, fmt.Errorf("%q is IPv6, and this build is IPv4 only", s)
	}
	bits := 32
	if hasSlash {
		n, err := strconv.Atoi(bitsStr)
		if err != nil || n < 0 || n > 32 {
			return CIDR{}, fmt.Errorf("%q has a bad prefix length", s)
		}
		bits = n
	}
	return CIDR{Addr: binary.BigEndian.Uint32(v4), Bits: uint8(bits)}.Mask(), nil
}

// V4FromProto converts one upstream CIDR; a 16 byte address may be ::ffff:a.b.c.d.
func V4FromProto(ip []byte, prefix uint32) (c CIDR, isV4 bool, err error) {
	switch len(ip) {
	case 4:
		if prefix > 32 {
			return CIDR{}, false, fmt.Errorf("prefix %d is too long for a 4 byte address", prefix)
		}
		return CIDR{Addr: binary.BigEndian.Uint32(ip), Bits: uint8(prefix)}.Mask(), true, nil
	case 16:
		for _, b := range ip[:10] {
			if b != 0 {
				return CIDR{}, false, nil
			}
		}
		if ip[10] != 0xFF || ip[11] != 0xFF {
			return CIDR{}, false, nil
		}
		if prefix < 96 || prefix > 128 {
			return CIDR{}, false, fmt.Errorf("prefix %d is invalid for a v4 mapped address", prefix)
		}
		return CIDR{Addr: binary.BigEndian.Uint32(ip[12:16]), Bits: uint8(prefix - 96)}.Mask(), true, nil
	default:
		return CIDR{}, false, fmt.Errorf("unexpected address length %d", len(ip))
	}
}
