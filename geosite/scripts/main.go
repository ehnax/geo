// Package main builds geosite.dat for a router running Xray: one upstream file
// reduced into category-ru (every -ru tag) and telegram, plus a private list
// written locally.
//
// Xray matches a tag name case-sensitively, so these are lower case even though
// upstream spells them upper case.
//
// A domain: rule covers a domain and everything under it; a bare word is a
// keyword, Xray's substring form, which is what upstream writes as plain:. A
// keyword is kept only when it collapses two or more rules and does not route
// meaningfully more than it replaced.
//
// Invariants: the .dat is assembled from the text lists read back off disk, the
// bytes are audited before anything is written, and every source rule is checked
// to still be covered. Port of the Python reference
// C:\protobuf\__GeoSite\_bin\geosite_optimize.py.
//
// Usage: go run ./geosite/scripts -out geosite/output [flags]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"

	"ehnax-geo-build/common"
)

const defaultGeoSiteURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat"

type options struct {
	geoSiteURL string
	cache      string
	out        string
	private    string
}

func main() {
	o := &options{}
	flag.StringVar(&o.geoSiteURL, "geosite-url", defaultGeoSiteURL, "upstream geosite.dat URL")
	flag.StringVar(&o.cache, "cache", "cache", "directory holding the downloaded sources")
	flag.StringVar(&o.out, "out", ".", "output directory, default the current one; pass geosite/output to keep the artifact beside the source")
	flag.StringVar(&o.private, "private", "geosite/private.src.txt", "hand-written PRIVATE list, safe to edit")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "geo-build: "+err.Error())
		os.Exit(1)
	}
}

func run(o *options) error {
	common.Say("[source]")
	gsPath, err := common.Fetch(o.cache, "geosite", o.geoSiteURL)
	if err != nil {
		return err
	}
	src, err := loadGeoSite(gsPath)
	if err != nil {
		return err
	}
	common.Sayf("  upstream  geosite %d tags", len(src.GetEntry()))

	common.Say("[build]")
	lists, reports, ruTags, err := buildGeoSite(src, o.private)
	if err != nil {
		return err
	}
	common.Sayf("  %d Russian tags, names ending in -ru", len(ruTags))
	for _, t := range ruTags {
		common.Sayf("      %s", t)
	}
	for _, r := range reports {
		common.Sayf("  %-12s %7d rules in -> %5d out, %d keywords kept, %d refused",
			r.tag, r.in, r.out, len(r.accepted), len(r.refused))
		if len(r.granted) > 0 {
			parts := make([]string, 0, len(r.granted))
			for _, k := range r.granted {
				parts = append(parts, fmt.Sprintf("%s x%g", k, allowance[k]))
			}
			common.Sayf("      kept on a raised allowance: %s", strings.Join(parts, ", "))
		}
		if len(r.shadowed) > 0 {
			common.Sayf("      dropped, every rule already covered by a longer keyword: %s",
				strings.Join(r.shadowed, ", "))
		}
	}

	common.Say("[write]")
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}

	// The .dat is assembled from what was read back off disk.
	paths := make(map[string]string, len(targets))
	for _, name := range targets {
		path := filepath.Join(o.out, name+".txt")
		if err := writeList(path, lists[name]); err != nil {
			return err
		}
		paths[name] = path
		common.Sayf("  %-16s %5d lines  %s", name+".txt", len(lists[name]), common.SHA256Of(path))
	}

	readBack := make(map[string][]optRule, len(targets))
	for _, name := range targets {
		rules, err := readList(paths[name])
		if err != nil {
			return err
		}
		readBack[name] = rules
	}

	entries, err := entriesFromLists(readBack)
	if err != nil {
		return err
	}
	blob, err := proto.Marshal(&routercommon.GeoSiteList{Entry: entries})
	if err != nil {
		return err
	}

	// Audited before the output directory is touched, so a failed build leaves
	// no partial artifact.
	common.Say("[verify]")
	if err := auditSite(blob, readBack); err != nil {
		return err
	}
	common.Sayf("  geosite.dat  %8d bytes  ok", len(blob))

	path := filepath.Join(o.out, "geosite.dat")
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		return err
	}
	if err := common.WriteSumFile(path+".sha256sum", "geosite.dat", blob); err != nil {
		return err
	}
	common.Sayf("  geosite.dat  %8d bytes  %s", len(blob), common.HexSum(blob))

	common.Sayf("[done] %s", o.out)
	return nil
}

func loadGeoSite(path string) (*routercommon.GeoSiteList, error) {
	raw, err := common.ReadBlob(path)
	if err != nil {
		return nil, err
	}
	var out routercommon.GeoSiteList
	if err := proto.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: not a geosite.dat: %w", path, err)
	}
	if len(out.GetEntry()) == 0 {
		return nil, fmt.Errorf("%s: parsed, but holds no tags", path)
	}
	return &out, nil
}
