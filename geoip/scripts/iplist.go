package main

// Tags are never merged with each other: that would change what a tag means.

import (
	"fmt"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"strings"

	"ehnax-geo-build/common"
)

const targetCountry = "RU"

// ipTargets is the shipped tag order; it must stay fixed for reproducible output.
var ipTargets = []string{"RU", "TELEGRAM", "PRIVATE"}

// privateRanges reads geoip/private.src.txt, the hand-written PRIVATE list.
func privateRanges(path string) ([]common.IPRange, error) {
	out, v6, err := common.ParseZoneFile(path)
	if err != nil {
		return nil, err
	}
	if v6 > 0 {
		return nil, fmt.Errorf("%s: holds %d IPv6 entries, and this build is IPv4 only", path, v6)
	}
	return out, nil
}

// geoipStat is one line of the build report.
type geoipStat struct {
	Tag       string
	Origin    string
	Src       int
	Emitted   int
	V6Skipped int
}

// ipSources is where each tag's addresses come from before they are reduced.
type ipSources struct {
	RU       []common.IPRange
	Upstream *routercommon.GeoIPList
}

// buildGeoIP returns per-tag ranges; the caller rebuilds the proto entries from the
// text files it has read back.
func buildGeoIP(src *ipSources, order []string, privatePath string) ([]geoipStat, map[string][]common.IPRange, error) {
	private, err := privateRanges(privatePath)
	if err != nil {
		return nil, nil, err
	}

	// Upstream holds hundreds of tags; parsing all of them to find three costs seconds.
	wanted := make(map[string]bool)
	for _, t := range order {
		if t != targetCountry && t != "PRIVATE" {
			wanted[t] = true
		}
	}
	up := make(map[string]*routercommon.GeoIP, len(wanted))
	if len(wanted) > 0 {
		for _, e := range src.Upstream.GetEntry() {
			name := strings.ToUpper(e.GetCountryCode())
			if wanted[name] {
				up[name] = e
			}
		}
	}

	fed := map[string][]common.IPRange{targetCountry: src.RU, "PRIVATE": private}
	var missing []string
	for _, tag := range order {
		if _, ok := fed[tag]; ok {
			continue
		}
		if _, ok := up[tag]; !ok {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("upstream geoip.dat is missing tags: %s", strings.Join(missing, ", "))
	}

	stats := make([]geoipStat, 0, len(order))
	merged := make(map[string][]common.IPRange, len(order))

	for _, tag := range order {
		var ranges []common.IPRange
		st := geoipStat{Tag: tag}

		if r, ok := fed[tag]; ok {
			ranges = r
			st.Origin = "private.txt"
			if tag == targetCountry {
				st.Origin = "ipdeny + db-ip"
			}
		} else {
			e := up[tag]
			st.Src, st.Origin = len(e.GetCidr()), "upstream"
			if e.GetInverseMatch() {
				return nil, nil, fmt.Errorf("upstream tag %s sets inverse_match, which this build does not emit", tag)
			}
			for _, c := range e.GetCidr() {
				n, isV4, err := common.V4FromProto(c.GetIp(), c.GetPrefix())
				if err != nil {
					return nil, nil, fmt.Errorf("geoip %s: %w", tag, err)
				}
				if !isV4 {
					st.V6Skipped++
					continue
				}
				ranges = append(ranges, common.CIDRRange(n))
			}
		}
		if st.Src == 0 {
			st.Src = len(ranges)
		}

		st.Emitted = len(common.CollapseRanges(ranges))
		stats = append(stats, st)
		merged[tag] = common.MergeRanges(ranges)
	}
	return stats, merged, nil
}
