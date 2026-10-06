package main

// Building the geosite half: Russian tags in, three lists plus a geosite.dat out.
//
// Tag names are lower case because Xray matches them case-sensitively:
// geosite:CATEGORY-RU matches nothing. Upstream spells them upper case.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

// typeOfPrefix turns a rule prefix back into the protobuf enum.
func typeOfPrefix(prefix string) (routercommon.Domain_Type, error) {
	switch prefix {
	case pKeyword:
		return routercommon.Domain_Plain, nil
	case pRegex:
		return routercommon.Domain_Regex, nil
	case pDomain:
		return routercommon.Domain_RootDomain, nil
	case pFull:
		return routercommon.Domain_Full, nil
	}
	return 0, fmt.Errorf("unknown rule type %q", prefix)
}

// sourceTags indexes the upstream file by lower case tag name. Values are taken
// exactly as they come: lower casing them would invent rules, and the reduction
// belongs to the optimiser.
//
// Field 3 is refused, not read: it is `include` in the standard GeoSite schema
// and `resource_hash` in v2fly's, and guessing would change the Russian list.
func sourceTags(src *routercommon.GeoSiteList) (map[string]*sourceTag, error) {
	tags := make(map[string]*sourceTag, len(src.GetEntry()))
	for _, e := range src.GetEntry() {
		name := strings.ToLower(e.GetCountryCode())
		if name == "" {
			return nil, fmt.Errorf("upstream holds a tag with no name")
		}
		if _, dup := tags[name]; dup {
			return nil, fmt.Errorf("upstream holds %q twice", name)
		}
		if len(e.GetResourceHash()) > 0 {
			return nil, fmt.Errorf("upstream tag %q sets field 3 (%d bytes), which is an include list to some GeoSite readers and a resource hash to v2fly's schema; refusing to guess which it is",
				name, len(e.GetResourceHash()))
		}
		t := &sourceTag{}
		for _, d := range e.GetDomain() {
			t.domains = append(t.domains, optRule{prefixOfType(d.GetType()), d.GetValue(), ""})
		}
		tags[name] = t
	}
	return tags, nil
}

func prefixOfType(t routercommon.Domain_Type) string {
	switch t {
	case routercommon.Domain_Regex:
		return pRegex
	case routercommon.Domain_RootDomain:
		return pDomain
	case routercommon.Domain_Full:
		return pFull
	}
	return pKeyword
}

// selectRuTags returns the Russian tags, in name order.
//
// -ru only, matching the Python reference: a trailing colon names a source file
// rather than a Russian variant. No blocklist -- the `-blocked` names do not end
// in -ru, so such a filter could never fire.
func selectRuTags(tags map[string]*sourceTag) []string {
	var chosen []string
	for name := range tags {
		if strings.HasSuffix(name, "-ru") {
			chosen = append(chosen, name)
		}
	}
	sort.Strings(chosen)
	return chosen
}

// buildTargets collects the rules of the three shipped tags. The Russian list is
// the union of every Russian tag, resolved through includes; telegram is a
// single upstream tag; private is read from the hand-written file.
func buildTargets(tags map[string]*sourceTag, privatePath string) (map[string][]optRule, []string, error) {
	chosen := selectRuTags(tags)
	if len(chosen) == 0 {
		return nil, nil, fmt.Errorf("upstream holds no tag ending in -ru, so there is no Russian list to build")
	}

	sources := make(map[string][]optRule, len(targets))
	var union []optRule
	for _, t := range chosen {
		union = append(union, resolve(t, tags)...)
	}
	sources[targets[0]] = dedupeRules(union)

	if _, ok := tags["telegram"]; !ok {
		return nil, nil, fmt.Errorf("upstream geosite.dat is missing the telegram tag")
	}
	sources["telegram"] = resolve("telegram", tags)

	priv, err := privateRules(privatePath)
	if err != nil {
		return nil, nil, err
	}
	sources["private"] = priv
	return sources, chosen, nil
}

// dedupeRules drops repeated rules, keeping first-seen order: the optimiser
// counts what it collapses, so a doubled rule looks collapsible when it is not.
func dedupeRules(rules []optRule) []optRule {
	seen := make(map[string]bool, len(rules))
	out := make([]optRule, 0, len(rules))
	for _, r := range rules {
		k := r.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

// vocabulary collects every domain in a rule set, used as the thing a keyword
// is measured against: what it would reach.
func vocabulary(rules []optRule) []string {
	seen := make(map[string]bool, len(rules))
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.value == "" || seen[r.value] {
			continue
		}
		seen[r.value] = true
		out = append(out, r.value)
	}
	return out
}

// allVocabulary is the whole upstream file's values: a keyword that looks
// contained in the Russian list is not necessarily contained for a reader
// elsewhere, and that is the mistake worth measuring against.
func allVocabulary(tags map[string]*sourceTag) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 1<<16)
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, r := range tags[name].domains {
			if r.value == "" || seen[r.value] {
				continue
			}
			seen[r.value] = true
			out = append(out, r.value)
		}
	}
	return out
}

// buildGeoSite optimises the three shipped tags and checks each result against
// its source: a rule nothing matches any more is invisible in a rule count.
func buildGeoSite(src *routercommon.GeoSiteList, privatePath string) (map[string][]optRule, []optReport, []string, error) {
	tags, err := sourceTags(src)
	if err != nil {
		return nil, nil, nil, err
	}
	sources, ruTags, err := buildTargets(tags, privatePath)
	if err != nil {
		return nil, nil, nil, err
	}

	vocab := allVocabulary(tags)

	lists := make(map[string][]optRule, len(targets))
	reports := make([]optReport, 0, len(targets))
	for _, name := range targets {
		source := sources[name]
		lines, pre, rep := optimize(source, vocab, name, vocab)
		rep.dropped = len(source) - len(pre)

		missing := checkCoverage(source, lines)
		if len(missing) > 0 {
			more := ""
			if len(missing) > 1 {
				more = fmt.Sprintf(" (+%d more)", len(missing)-1)
			}
			return nil, nil, nil, fmt.Errorf("%s: %d source rules are covered by nothing, first %s%s",
				name, len(missing), missing[0], more)
		}

		lists[name] = lines
		reports = append(reports, rep)
	}
	return lists, reports, ruTags, nil
}

// entriesFromLists turns the text lists into the proto entries, in target order.
func entriesFromLists(lists map[string][]optRule) ([]*routercommon.GeoSite, error) {
	entries := make([]*routercommon.GeoSite, 0, len(targets))
	for _, name := range targets {
		rules, ok := lists[name]
		if !ok {
			return nil, fmt.Errorf("no rules collected for %s", name)
		}
		out := make([]*routercommon.Domain, 0, len(rules))
		for _, r := range rules {
			t, err := typeOfPrefix(r.prefix)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			out = append(out, &routercommon.Domain{Type: t, Value: r.value})
		}
		// Upstream writes codes upper case and Xray looks the tag up that way,
		// so the output has to match. The lookup key stays lower case.
		entries = append(entries, &routercommon.GeoSite{
			CountryCode: strings.ToUpper(name),
			Domain:      out,
		})
	}
	return entries, nil
}
