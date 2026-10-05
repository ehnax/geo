package main

import (
	"encoding/binary"
	"fmt"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"

	"ehnax-geo-build/common"
)

// auditIP checks the serialised bytes, never the in-memory structures a bug would corrupt.
func auditIP(blob []byte, lists map[string][]common.CIDR, src map[string][]common.IPRange) error {
	list := &routercommon.GeoIPList{}
	if err := proto.Unmarshal(blob, list); err != nil {
		return fmt.Errorf("geoip.dat: output does not parse back: %w", err)
	}

	var problems []string
	seenTag := map[string]bool{}

	for _, e := range list.GetEntry() {
		tag := e.GetCountryCode()
		seenTag[tag] = true
		expected, ok := lists[tag]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is in the file but was never built", tag))
			continue
		}
		if e.GetInverseMatch() {
			problems = append(problems, fmt.Sprintf("%s: inverse_match is set, which this build does not emit", tag))
		}

		got := make([]common.CIDR, 0, len(e.GetCidr()))
		bad := false
		for _, c := range e.GetCidr() {
			if len(c.GetIp()) != 4 {
				problems = append(problems, fmt.Sprintf("%s: ip field is %d bytes, this build is IPv4 only", tag, len(c.GetIp())))
				bad = true
				continue
			}
			if c.GetPrefix() > 32 {
				problems = append(problems, fmt.Sprintf("%s: prefix %d is out of range", tag, c.GetPrefix()))
				bad = true
				continue
			}
			raw := common.CIDR{Addr: binary.BigEndian.Uint32(c.GetIp()), Bits: uint8(c.GetPrefix())}
			if raw != raw.Mask() {
				problems = append(problems, fmt.Sprintf("%s: %s has host bits set", tag, raw))
				bad = true
				continue
			}
			got = append(got, raw)
		}
		if bad {
			continue
		}

		if len(got) != len(expected) {
			problems = append(problems, fmt.Sprintf("%s: %d blocks read back, %d written", tag, len(got), len(expected)))
			continue
		}
		for i := range got {
			if got[i] != expected[i] {
				problems = append(problems, fmt.Sprintf("%s: block %d reads back as %s, written %s",
					tag, i+1, got[i], expected[i]))
				break
			}
		}

		prevEnd := int64(-1)
		for _, c := range got {
			if int64(c.Addr) <= prevEnd {
				problems = append(problems, fmt.Sprintf("%s: %s overlaps or repeats the previous block", tag, c))
			}
			prevEnd = int64(c.End())
		}

		// Minimality: an independent Collapse() must return the same set.
		if again := common.Collapse(got); len(again) != len(got) {
			problems = append(problems, fmt.Sprintf("%s: %d blocks could be reduced to %d, so the list is not minimal",
				tag, len(got), len(again)))
		}

		// Address count is the one measure that catches a lost address.
		if want, ok := src[tag]; ok {
			if gotN, wantN := common.BlocksTotal(got), common.RangesTotal(want); gotN != wantN {
				problems = append(problems, fmt.Sprintf("%s: covers %d addresses, the sources hold %d",
					tag, gotN, wantN))
			}
		}
	}

	for tag := range lists {
		if !seenTag[tag] {
			problems = append(problems, fmt.Sprintf("tag %s vanished from the output", tag))
		}
	}
	return common.Report("geoip.dat", problems)
}
