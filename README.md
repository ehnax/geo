# geo-build

Builds `geoip.dat` and `geosite.dat` for a router running Xray.

```
go run ./geoip/scripts   -out geoip/output
go run ./geosite/scripts -out geosite/output
```

## Sources

| List | Source |
|---|---|
| `geoip:ru` | [ipdeny](https://www.ipdeny.com/ipblocks/data/countries/ru.zone) + [db-ip](https://db-ip.com/db/download/ip-to-country-lite) |
| `geoip:telegram` | [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) |
| `geoip:private` | `geoip/private.src.txt` |
| `geosite:category-ru`, `geosite:telegram` | [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) |
| `geosite:private` | `geosite/private.src.txt` |

## Layout

`geoip/private.src.txt` and `geosite/private.src.txt` are hand-written and safe to
edit — the next build reduces whatever is there. `geoip/scripts/` and
`geosite/scripts/` build and audit; `common/` is shared so the two builds cannot
disagree about a prefix or a block. `output/` holds the artifacts and is the only
place a build writes; a checkout carries it empty.

## Design

**IPv4 only.** The router runs `disable_ipv6=1`, so a CIDR is a `uint32` and a
length.

**Two Russian IP sources.** ipdeny and db-ip publish on different schedules, so
each has holes the other fills. Both are read and the union reduced.

**db-ip as CSV.** The same data as the MMDB, half the size, no converter to
install.

**Tags reduced independently.** Collapsing across tags would fold a TELEGRAM block
into RU and quietly change what `geoip:telegram` means.

**`geosite:category-ru` is a union** of every upstream tag ending in `-ru`, not the
one named that.

**PRIVATE is hand-written.** Upstream's copies carry RFC 6890 documentation ranges,
a 6to4 relay switched off in 2015, and 131 rules of vendor router admin hosts.
None of that belongs in routing data.

## Optimiser

`domain:` covers everything under it, so `domain:ru` already covers `sber.ru`;
shorter rules sharing a root are redundant and dropped.

A bare word is a keyword, a substring match. Upstream's `plain:` rules are that
form, so a keyword is kept only if it replaces at least two rules, is on the
forced list, and does not widen past the limits in `geosite/scripts/optimize.go`.

Cyrillic hosts arrive two ways. IDN TLDs are named outright — `.рф`, `.рус`,
`.москва`, `.онлайн`, `.сайт`, `.бел`, `.орг` and `.дети` are `domain:` suffixes.
A host under an ASCII TLD, `xn--90ab.com`, cannot be, so one `regexp:` rule covers
the shape: a punycode label in second to last position under an alphanumeric TLD.
That trailing class is what excludes foreign IDN TLDs, since an ASCII TLD never
carries a hyphen.

Every accept or refusal is decided before anything is written, then re-checked
against what was written.

## Flags

`-out` defaults to the current directory. Also `-cache`, `-month`, and per-build
`-geoip-url`, `-geosite-url`, `-zone-url`, `-dbip-url`. The first run downloads
about 31 MiB into `-cache`; later runs are offline.

Which tags ship are ports of the Python references and live in the code. PRIVATE is
the exception, being an ordinary editable file.

## Requirements

Go 1.22 or newer. Nothing else.

## Releases

Every push to `main`, and every Monday at 03:17 UTC, updates `latest`. Weekly,
because the monthly CSV lands on the 1st and a daily rebuild would only churn it.

```
https://github.com/ehnax/geo/releases/download/latest/geoip.dat
https://github.com/ehnax/geo/releases/download/latest/geosite.dat
```

Four assets: the two `.dat` and a `.sha256sum` for each. The `.txt` lists are build
evidence and never committed. The workflow then downloads both `.dat` back from the
release and checks them against the published sums, so a passing run proves what a
consumer gets is what was built.

Sizes change as upstream publishes; the checks in the code must not.
