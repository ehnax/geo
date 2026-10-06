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

```
geoip/private.src.txt  hand-written PRIVATE list, safe to edit
geoip/scripts/         build, audit
geoip/output/          geoip.dat, geoip.dat.sha256sum, ru.txt, telegram.txt, private.txt
geosite/private.src.txt  hand-written PRIVATE list, safe to edit
geosite/scripts/        build, optimiser, audit
geosite/output/         geosite.dat, geosite.dat.sha256sum, category-ru.txt, telegram.txt, private.txt
common/           shared IPv4 arithmetic, downloads, reporting
.github/          build workflow
```

`output/` is empty in a fresh checkout. It is the only place a build writes, and
its contents are never committed. `common/` is shared so the two builds cannot
disagree about how a prefix is parsed or a block reduced.

## Design

**IPv4 only.** The target router runs with `disable_ipv6=1` and resolves over
IPv4, so a CIDR is a `uint32` plus a prefix length, with no IPv6 cases to get
wrong.

**Two Russian IP sources.** ipdeny and db-ip watch the same addresses on
different schedules, so either alone has holes. Both are read and their union
reduced.

**db-ip as CSV.** Same data as the MMDB at half the size, and it drops an
external converter from the pipeline.

**Tags are reduced independently.** Collapsing across tags would fold a TELEGRAM
block into RU and silently change what `geoip:telegram` means.

**`geosite:category-ru` is a union** of every upstream tag whose name ends in
`-ru`, not the one tag literally named that.

**PRIVATE is hand-written, in both files.** Upstream's `geoip` copy carries
RFC 6890 and TEST-NET documentation ranges and a 6to4 relay switched off in
2015; its `geosite` copy is 131 rules of vendor router admin hosts and the IPv6
reverse delegation tree. Neither belongs in a router's routing data.

Both lists live in `geoip/private.src.txt` and `geosite/private.src.txt`, one
rule per line with `#` comments. They are ordinary committed files: edit them,
add your own entries, and the next build reduces whatever is there. The `.src`
suffix keeps them apart from the reduced `output/private.txt`, which the build
rewrites and which no release touches.

## Optimiser

A `domain:` rule covers everything under it, so `domain:ru` already covers
`sber.ru`. Shorter rules sharing a root are redundant and dropped.

A bare word is a keyword — a substring match. Upstream's `plain:` rules are that
form, and dropping them all would lose real routing, so a keyword is kept only if
it replaces at least two rules, is on the forced list, and does not widen too far.
Widening limits are a table in `geosite/scripts/optimize.go`.

Cyrillic hosts arrive two ways. A punycode IDN TLD is named outright: `.рф`,
`.рус`, `.москва`, `.онлайн`, `.сайт`, `.бел`, `.орг` and `.дети` are `domain:`
suffixes, so everything under one is covered by name. A Cyrillic host under an
ASCII TLD, `xn--90ab.com`, cannot be named that way, so one `regexp:` rule stands
in for the whole shape — a punycode label in second to last position under an
alphanumeric TLD. The trailing character class is what keeps foreign IDN TLDs
out of `category-ru`: an ASCII TLD never carries a hyphen, so the class rejects
every punycode TLD at once and none of them has to be listed to be excluded. The
Russian ones are not caught by it either, and are not meant to be.

Every accept or refusal is decided before anything is written and re-checked
against the file that was written.

## Guarantees

Every build checks itself and aborts rather than writing a bad file. Enforced in
code, not by tests, so a local run gets the same protection as CI:

- The `.dat` is assembled from the text lists read back off disk, so it cannot
  drift from them.
- Address coverage is compared before and after reduction by counting addresses,
  which catches a lost range that a block count would hide.
- The lists are re-derived a second time with a different algorithm and rejected
  if the two disagree.
- Order, duplicates, overlaps and host bits are checked directly, since none of
  them changes a block count.
- The output is unmarshalled and re-checked before it is written.
- The workflow removes the cache, staging copy and every artifact afterwards.

## Flags

`-out` defaults to the current directory. Also: `-cache`, `-month`, and per-build
`-geoip-url`, `-geosite-url`, `-zone-url`, `-dbip-url`.

The first run downloads about 31 MiB into `-cache`; later runs are offline. There
is no flag for the tag list — which tags ship are ports of the Python references
and live in the code. PRIVATE is the exception, being an ordinary editable file
under `Design` above.

## Requirements

Go 1.22 or newer. Nothing else.

## Releases

Every push to `main`, and every Monday at 03:17 UTC, runs the build and updates
the `latest` release. Weekly, because a monthly CSV lands on the 1st and a daily
rebuild would only churn the release.

```
https://github.com/ehnax/geo/releases/download/latest/geoip.dat
https://github.com/ehnax/geo/releases/download/latest/geosite.dat
```

Assets are four files: `geoip.dat`, `geosite.dat`, and a `.sha256sum` for each.
Nothing else. The `.txt` lists are build evidence that stays in `output/`; no
router reads them. The workflow then downloads both `.dat` files back from the
release and checks them against the published sums, so a successful run proves
what a consumer gets is what was built.

Sizes change as upstream publishes; the invariants above must not.
