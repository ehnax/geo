// Package main builds geoip.dat for a router running Xray: RU from ipdeny plus db-ip,
// TELEGRAM from upstream, PRIVATE from privateIPv4. IPv4 only.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"

	"ehnax-geo-build/common"
)

const (
	defaultGeoIPURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"
	defaultZoneURL  = "https://www.ipdeny.com/ipblocks/data/countries/ru.zone"
	defaultDBIPURL  = "https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz"
)

type options struct {
	geoIPURL string
	zoneURL  string
	dbipURL  string
	month    string
	cache    string
	out      string
	private  string
}

func main() {
	o := &options{}
	flag.StringVar(&o.geoIPURL, "geoip-url", defaultGeoIPURL, "upstream geoip.dat URL, source of TELEGRAM")
	flag.StringVar(&o.zoneURL, "zone-url", defaultZoneURL, "ipdeny zone file URL, one of the two Russian sources")
	flag.StringVar(&o.dbipURL, "dbip-url", defaultDBIPURL, "db-ip monthly CSV URL, %s is replaced by the month")
	flag.StringVar(&o.month, "month", "", "month for the db-ip file, YYYY-MM; defaults to the current UTC month")
	flag.StringVar(&o.cache, "cache", "cache", "directory holding the downloaded sources")
	flag.StringVar(&o.out, "out", ".", "output directory, default the current one; pass geoip/output to keep the artifact beside the source")
	flag.StringVar(&o.private, "private", "geoip/private.src.txt", "hand-written PRIVATE list, safe to edit")
	flag.Parse()

	if o.month == "" {
		o.month = time.Now().UTC().Format("2006-01")
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "geo-build: "+err.Error())
		os.Exit(1)
	}
}

func run(o *options) error {
	common.Say("[source]")
	giPath, err := common.Fetch(o.cache, "geoip", o.geoIPURL)
	if err != nil {
		return err
	}
	zonePath, err := common.FetchTo(o.cache, "ru.zone", o.zoneURL)
	if err != nil {
		return err
	}
	dbipFile := fmt.Sprintf("dbip-country-lite-%s.csv.gz", o.month)
	dbipPath, err := common.FetchTo(o.cache, dbipFile, fmt.Sprintf(o.dbipURL, o.month))
	if err != nil {
		return err
	}

	upstream, err := loadGeoIP(giPath)
	if err != nil {
		return err
	}
	zoneRanges, zoneV6, err := common.ParseZoneFile(zonePath)
	if err != nil {
		return err
	}
	dbipRanges, dbipV6, dbipRows, err := common.LoadDBIP(dbipPath, targetCountry)
	if err != nil {
		return err
	}
	common.Sayf("  ipdeny   %7d ranges (%d IPv6 dropped)", len(zoneRanges), zoneV6)
	common.Sayf("  db-ip    %7d ranges (%d rows, %d IPv6 dropped)", len(dbipRanges), dbipRows, dbipV6)
	common.Sayf("  upstream  geoip %d tags", len(upstream.GetEntry()))

	ruRanges := append(append([]common.IPRange{}, zoneRanges...), dbipRanges...)

	common.Say("[build]")
	stats, srcRanges, err := buildGeoIP(&ipSources{RU: ruRanges, Upstream: upstream}, ipTargets, o.private)
	if err != nil {
		return err
	}
	for _, s := range stats {
		common.Sayf("  %-10s %7d in source (%-12s) -> %6d in file   (IPv6 dropped: %d)",
			s.Tag, s.Src, s.Origin, s.Emitted, s.V6Skipped)
	}

	common.Say("[write]")
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}

	// Assembled from the lists read back off disk, so the .dat cannot drift from them.
	paths := make(map[string]string, len(ipTargets))
	for _, tag := range ipTargets {
		nets := common.CollapseRanges(srcRanges[tag])
		path := filepath.Join(o.out, strings.ToLower(tag)+".txt")
		if err := common.WriteLines(path, common.CIDRStrings(nets)); err != nil {
			return err
		}
		paths[tag] = path
		common.Sayf("  %-14s %5d lines  %s", strings.ToLower(tag)+".txt", len(nets), common.SHA256Of(path))
	}

	readBack := make(map[string][]common.CIDR, len(ipTargets))
	for _, tag := range ipTargets {
		nets, err := common.ReadCIDRList(paths[tag])
		if err != nil {
			return err
		}
		readBack[tag] = nets
	}

	entries := make([]*routercommon.GeoIP, 0, len(ipTargets))
	for _, tag := range ipTargets {
		nets := readBack[tag]
		out := make([]*routercommon.CIDR, 0, len(nets))
		for _, n := range nets {
			out = append(out, &routercommon.CIDR{Ip: n.ProtoBytes(), Prefix: uint32(n.Bits)})
		}
		entries = append(entries, &routercommon.GeoIP{CountryCode: tag, Cidr: out})
	}
	blob, err := proto.Marshal(&routercommon.GeoIPList{Entry: entries})
	if err != nil {
		return err
	}

	// Audit before writing, so a failed build leaves no partial artifact.
	common.Say("[verify]")
	if err := auditIP(blob, readBack, srcRanges); err != nil {
		return err
	}
	common.Sayf("  geoip.dat  %8d bytes  ok", len(blob))

	path := filepath.Join(o.out, "geoip.dat")
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		return err
	}
	// The router checks the download against this file, so it ships too.
	if err := common.WriteSumFile(path+".sha256sum", "geoip.dat", blob); err != nil {
		return err
	}
	common.Sayf("  geoip.dat  %8d bytes  %s", len(blob), common.HexSum(blob))

	common.Sayf("[done] %s", o.out)
	return nil
}

func loadGeoIP(path string) (*routercommon.GeoIPList, error) {
	raw, err := common.ReadBlob(path)
	if err != nil {
		return nil, err
	}
	var out routercommon.GeoIPList
	if err := proto.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: not a geoip.dat: %w", path, err)
	}
	if len(out.GetEntry()) == 0 {
		return nil, fmt.Errorf("%s: parsed, but holds no tags", path)
	}
	return &out, nil
}
