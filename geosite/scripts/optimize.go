package main

// The optimiser, ported from the Python reference at
// C:\protobuf\__GeoSite\_bin\geosite_optimize.py. It produces the three text
// lists; main assembles geosite.dat from exactly those.
//
// A keyword is accepted only when it collapses at least two rules and does not
// route more than maxWiden times what it replaced; a widening above 1.0x is
// deliberate and printed.
//
// Prefixes are spelled the way the Python spells them, colon and all.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"ehnax-geo-build/common"
)

const (
	// Below this a keyword stops being a word and becomes a letter pair.
	minRootLen = 4

	// Upper bound on how many more domains a keyword may route than it replaces.
	maxWiden = 2.0

	// A punycode label in second to last position under a TLD that is letters and
	// digits only. An ASCII TLD never carries a hyphen, so the trailing class
	// drops every punycode TLD at once -- xn--fiqs8s, xn--3e0b707e and the
	// other foreign ones -- without listing them. The Russian IDN TLDs are
	// suffixes below, not this rule's job.
	punycodeRe = `xn--[a-z0-9-]+\.[a-z0-9]+$`

	// The one place the rule is interpreted. Every answer comes from here, so a
	// domain can never be covered by one reading of the rule and not another.
	punycodeRx = regexp.MustCompile(punycodeRe)
)

var (
	// Added to the Russian list, standing in for every rule ending in one. cn was
	// removed: it covered a whole TLD for four rules' sake. The xn-- entries name
	// the IDN TLDs upstream actually uses, so punycodeRe need not name them; every
	// rule ending in one of them is dropped from the input and covered again here.
	suffixes = []string{
		"ru", "su", "am", "az", "by", "ee", "ge",
		"kg", "kz", "md", "tj", "tm", "ua", "uz",
		"xn--p1ai", "xn--p1acf", "xn--80adxhks", "xn--80asehdb",
		"xn--80aswg", "xn--90ais", "xn--c1avg", "xn--d1acj3b",
	}

	// Dropped from the input before anything is counted.
	skipWords = []string{"icq", "skype"}

	// Accepted on one collapsed rule, but not on nothing.
	forceKeywords = map[string]bool{
		"kaspersky": true, "yandex": true, "tiktok": true,
	}

	// Never become keywords, whatever the numbers say. Not brands, just words.
	neverKeywords = map[string]bool{
		"www": true, "mail": true, "shop": true, "news": true, "blog": true,
		"cdn": true, "static": true, "analytics": true, "api": true,
		"img": true, "audio": true, "video": true, "files": true,
		"data": true, "cloud": true,
	}

	// Widening limits of their own, overriding maxWiden: a holding company's
	// name legitimately answers to a short substring, but the same substring is
	// sometimes a coincidence ("sber" reaches stansberryresearch.com).
	allowance = map[string]float64{
		"ozon": 3.0, "ozonru": 3.0,
		"wildberries": 4.0, "2gis": 4.0, "jivochat": 4.0,
		"odnoklassniki": 4.0, "kaspersky-labs": 4.0, "drweb-av": 4.0,
		"tildacdn": 4.0, "dashlytrack": 4.0, "arteldoc": 4.0,
		"vkuseraudio": 4.0, "vkuserlive": 4.0, "vkuservideo": 4.0,
		"yandexwebcache": 4.0, "rtdefree": 4.0, "mindbox": 4.0,
		"ixbt": 4.0, "sberbank": 4.0, "sberauto": 4.0,
	}

	// The three lists, in file order. File name and tag name are one string on
	// purpose: routing config refers to the tag and Xray matches it exactly.
	targets = []string{"category-ru", "telegram", privateTag}
)

// privateTag is emitted as written; the rest come from upstream and are reduced.
const privateTag = "private"

// Rule prefixes, spelled the way the Python reference spells them.
const (
	pKeyword = "keyword:"
	pRegex   = "regexp:"
	pDomain  = "domain:"
	pFull    = "full:"
)

type optRule struct {
	prefix string
	value  string
	root   string
}

func (r optRule) String() string { return r.prefix + r.value }

// prefixRank orders the output: keywords first, then exact domains, then
// suffixes, then the regexp.
func prefixRank(prefix string) int {
	switch prefix {
	case pKeyword:
		return 0
	case pFull:
		return 1
	case pDomain:
		return 2
	case pRegex:
		return 3
	}
	return 99
}

// getRoot is the last label long enough to be a word and not a generic word.
// The TLD is skipped, mirroring reversed(parts[:-1]) in the Python.
func getRoot(domain string) string {
	parts := strings.Split(domain, ".")
	for i := len(parts) - 2; i >= 0; i-- {
		p := parts[i]
		if len(p) >= minRootLen && !neverKeywords[p] {
			return p
		}
	}
	return ""
}

// covers is Xray's suffix matching: a.b matches a.b and everything under it,
// but not xa.b.
func covers(suffix, domain string) bool {
	return suffix == domain || strings.HasSuffix(domain, "."+suffix)
}

func containsAny(value string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(value, n) {
			return true
		}
	}
	return false
}

func endsWithAnySuffix(value string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(value, "."+s) {
			return true
		}
	}
	return false
}

// sortCandidates puts the longest candidates first, ties alphabetical. The
// emitter relies on it: the first accepted keyword found in a rule is the one
// written, so a shorter keyword covered by a longer one never gets its own line.
func sortCandidates(c []string) {
	sort.Slice(c, func(i, j int) bool {
		if len(c[i]) != len(c[j]) {
			return len(c[i]) > len(c[j])
		}
		return c[i] < c[j]
	})
}

// ---------------------------------------------------------------------------
// Resolving tags
// ---------------------------------------------------------------------------

// sourceTag is one upstream tag, reduced to what the optimiser needs: the
// values already decoded, with their prefixes, and nothing else.
type sourceTag struct {
	domains  []optRule
	includes []string
}

// resolve returns every rule of `tag` and of everything it includes, each one
// once. The seen set is what stops a cycle of includes from looping.
func resolve(tag string, tags map[string]*sourceTag) []optRule {
	var out []optRule
	known := make(map[string]bool)
	queue := []string{tag}

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if known[t] {
			continue
		}
		known[t] = true

		item := tags[t]
		if item == nil {
			continue
		}
		for _, r := range item.domains {
			if containsAny(r.value, skipWords) {
				continue
			}
			k := r.String()
			if known[k] {
				continue
			}
			known[k] = true
			out = append(out, r)
		}
		queue = append(queue, item.includes...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Coverage
// ---------------------------------------------------------------------------

// keptIndex answers "does any kept rule catch this domain" without walking the
// kept rules: a suffix is a walk up the labels, a full rule a set, keywords one
// compiled alternation. Same verdict as covered, which the tests compare it to.
type keptIndex struct {
	doms  map[string]bool
	fulls map[string]bool
	kws   []string
	kwRx  *regexp.Regexp
	puny  bool
}

func newKeptIndex(kept []optRule) *keptIndex {
	idx := &keptIndex{
		doms:  make(map[string]bool),
		fulls: make(map[string]bool),
	}
	for _, r := range kept {
		switch r.prefix {
		case pDomain:
			idx.doms[r.value] = true
		case pFull:
			idx.fulls[r.value] = true
		case pKeyword:
			idx.kws = append(idx.kws, r.value)
		case pRegex:
			if r.value == punycodeRe {
				idx.puny = true
			}
		}
	}
	if len(idx.kws) > 0 {
		idx.kwRx = regexp.MustCompile(strings.Join(escapeAll(idx.kws), "|"))
	}
	return idx
}

func escapeAll(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = regexp.QuoteMeta(w)
	}
	return out
}

func (idx *keptIndex) covers(value string) bool {
	if idx.fulls[value] {
		return true
	}

	// A suffix matches a domain by name when it is one of its own suffixes, and
	// the chain contains the domain itself.
	for rest := value; ; {
		if idx.doms[rest] {
			return true
		}
		cut := strings.IndexByte(rest, '.')
		if cut < 0 {
			break
		}
		rest = rest[cut+1:]
	}

	if idx.kwRx != nil && idx.kwRx.MatchString(value) {
		return true
	}

// Whether the regexp matches a given domain is not answerable here, so an
	// arbitrary regexp only covers itself.
	return idx.puny && punycodeRx.MatchString(value)
}

// covered is the readable definition of the same rule, kept as the oracle
// keptIndex is checked against.
func covered(prefix, value string, kept []optRule) bool {
	for _, r := range kept {
		if r.prefix == prefix && r.value == value {
			return true
		}
	}
	for _, r := range kept {
		if r.prefix == pDomain && covers(r.value, value) {
			return true
		}
		if r.prefix == pFull && r.value == value {
			return true
		}
		if r.prefix == pKeyword && strings.Contains(value, r.value) {
			return true
		}
		if r.prefix == pRegex && r.value == punycodeRe && punycodeRx.MatchString(value) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Optimising
// ---------------------------------------------------------------------------

// decision is one keyword's fate, kept so the report and the file can be
// checked against each other.
type decision struct {
	gain  int
	extra int
}

type optReport struct {
	tag      string
	in       int
	out      int
	dropped  int
	accepted map[string]decision
	refused  map[string]decision
	granted  []string
	shadowed []string
}

// optimize collapses `rules` into as few output rules as it can without changing
// what they route. `universe` is what a keyword is measured against: the domains
// it would newly route. `allVocab` is for the report only.
func optimize(rules []optRule, universe []string, tagName string, allVocab []string) ([]optRule, []optRule, optReport) {
	// A hand-written list is emitted as written. The collapse below only earns
	// its keep on an upstream list, where one root stands for many rules; here
	// every line is already one deliberate choice, and a keyword that collapses
	// nothing would be refused and then found in the output.
	if tagName == privateTag {
		out := append([]optRule(nil), rules...)
		sort.SliceStable(out, func(i, j int) bool {
			ri, rj := prefixRank(out[i].prefix), prefixRank(out[j].prefix)
			if ri != rj {
				return ri < rj
			}
			return out[i].value < out[j].value
		})
		uniq := make([]optRule, 0, len(out))
		seen := make(map[string]bool, len(out))
		for _, r := range out {
			k := r.String()
			if seen[k] {
				continue
			}
			seen[k] = true
			uniq = append(uniq, r)
		}
		rep := optReport{
			tag:      tagName,
			in:       len(rules),
			out:      len(uniq),
			accepted: map[string]decision{},
			refused:  map[string]decision{},
		}
		return uniq, rules, rep
	}

	var pre []optRule
	roots := make(map[string]int)

	for _, r := range rules {
		if containsAny(r.value, skipWords) {
			continue
		}

		// Dropped only where the matching suffix rule comes back at the end;
		// private gets no domain:cn, so dropping cn there would lose the rule.
		if tagName == "category-ru" &&
			(strings.Contains(r.value, "xn--") || endsWithAnySuffix(r.value)) {
			continue
		}

		r.root = getRoot(r.value)
		if r.root != "" {
			roots[r.root]++
		}
		pre = append(pre, r)
	}

	// A root shared by two rules is the obvious keyword.
	candidates := make(map[string]bool)
	for r, c := range roots {
		if c >= 2 {
			candidates[r] = true
		}
	}
	for w := range forceKeywords {
		if g := getRoot(w); g != "" {
			candidates[g] = true
		} else {
			candidates[w] = true
		}
	}

	// Roots that reach a rule they are not the root of: same count, wider reach.
	for r := range roots {
		if candidates[r] || neverKeywords[r] {
			continue
		}
		hits := 0
		for _, rule := range pre {
			for _, p := range strings.Split(rule.value, ".") {
				if strings.Contains(p, r) {
					hits++
					break
				}
			}
		}
		if hits >= 2 {
			candidates[r] = true
		}
	}

	for w := range neverKeywords {
		delete(candidates, w)
	}
	for r := range candidates {
		if len(r) < minRootLen {
			delete(candidates, r)
		}
	}

	order := make([]string, 0, len(candidates))
	for c := range candidates {
		order = append(order, c)
	}
	sortCandidates(order)

	// A rule stays matchable until a keyword is accepted and takes it out of
	// circulation; the synthetic suffixes and the punycode rule are in from the
	// start because they are in the output from the start.
	kept := make([]optRule, 0, len(pre)+len(suffixes)+1)
	kept = append(kept, pre...)
	if tagName == "category-ru" {
		for _, s := range suffixes {
			kept = append(kept, optRule{pDomain, s, ""})
		}
		kept = append(kept, optRule{pRegex, punycodeRe, ""})
	}
	idx := newKeptIndex(kept)

	reaches := substringHits(order, universe)

	// Decide first, emit second: every keyword is judged against the same
	// starting point and its answer is final before a rule is written. Deciding
	// during the walk let sber be reported refused and shipped anyway.
	accepted := make(map[string]decision)
	for _, w := range order {
		gain := 0
		for _, r := range pre {
			if r.root == w || strings.Contains(r.value, w) {
				gain++
			}
		}
		// Nothing to collapse means nothing to write.
		if gain < 1 {
			continue
		}
		if gain < 2 && !forceKeywords[w] {
			continue
		}
		extra := 0
		for _, d := range reaches[w] {
			if !idx.covers(d) {
				extra++
			}
		}
		limit, ok := allowance[w]
		if !ok {
			limit = maxWiden
		}
		if extra <= int(float64(gain)*limit) {
			accepted[w] = decision{gain, extra}
			// What it replaces stops counting as coverage for later keywords.
			next := kept[:0:0]
			for _, r := range kept {
				if !strings.Contains(r.value, w) {
					next = append(next, r)
				}
			}
			kept = next
			idx = newKeptIndex(kept)
		}
	}

	// Emit, longest keyword first: the first accepted keyword found in a rule is
	// the one written for it.
	ordered := append([]optRule(nil), pre...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].prefix != ordered[j].prefix {
			return ordered[i].prefix < ordered[j].prefix
		}
		return ordered[i].value < ordered[j].value
	})

	var final []optRule
	emitted := make(map[string]bool)
	for _, rule := range ordered {
		hit := ""
		for _, w := range order {
			if _, ok := accepted[w]; ok && strings.Contains(rule.value, w) {
				hit = w
				break
			}
		}
		if hit != "" {
			final = append(final, optRule{pKeyword, hit, ""})
			emitted[hit] = true
		} else {
			final = append(final, rule)
		}
	}
	if tagName == "category-ru" {
		for _, s := range suffixes {
			final = append(final, optRule{pDomain, s, ""})
		}
		final = append(final, optRule{pRegex, punycodeRe, ""})
	}

	sort.SliceStable(final, func(i, j int) bool {
		ri, rj := prefixRank(final[i].prefix), prefixRank(final[j].prefix)
		if ri != rj {
			return ri < rj
		}
		return final[i].value < final[j].value
	})

	var uniq []optRule
	seen := make(map[string]bool)
	for _, r := range final {
		k := r.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		uniq = append(uniq, r)
	}

	// A keyword accepted but never written collapses nothing extra, so drop it.
	var shadowed []string
	for w := range accepted {
		if !emitted[w] {
			shadowed = append(shadowed, w)
		}
	}
	sort.Strings(shadowed)
	for _, w := range shadowed {
		delete(accepted, w)
	}

	refused := make(map[string]decision)
	for _, w := range order {
		if _, ok := accepted[w]; ok {
			continue
		}
		gain := 0
		for _, r := range pre {
			if r.root == w || strings.Contains(r.value, w) {
				gain++
			}
		}
		if gain < 2 {
			continue
		}
		extra := 0
		for _, d := range reaches[w] {
			if !idx.covers(d) {
				extra++
			}
		}
		refused[w] = decision{gain, extra}
	}

	// Report and file must agree: a refused keyword is meant to be absent, so
	// both directions over accepted keywords are checked.
	written := make(map[string]bool)
	for _, r := range uniq {
		if r.prefix == pKeyword {
			written[r.value] = true
		}
	}
	for w := range accepted {
		if !written[w] {
			panic(fmt.Sprintf("internal: %q accepted but not in the output", w))
		}
	}
	for w := range written {
		if _, ok := accepted[w]; !ok {
			panic(fmt.Sprintf("internal: %q is in the output but was never accepted", w))
		}
	}

	rep := optReport{
		tag:      tagName,
		in:       len(pre),
		out:      len(uniq),
		accepted: accepted,
		refused:  refused,
		shadowed: shadowed,
	}
	for w := range accepted {
		if _, ok := allowance[w]; ok {
			rep.granted = append(rep.granted, w)
		}
	}
	sort.Strings(rep.granted)

	return uniq, pre, rep
}

// substringHits reports, for each candidate, the domains that contain it.
//
// One candidate at a time on purpose: a single alternation is faster but lets
// the longer alternative consume the text, hiding a short candidate inside it
// (kaspersky inside kaspersky-labs), which understates what it reaches.
func substringHits(candidates, vocabulary []string) map[string][]string {
	hits := make(map[string][]string, len(candidates))
	for _, w := range candidates {
		var list []string
		for _, d := range vocabulary {
			if strings.Contains(d, w) {
				list = append(list, d)
			}
		}
		hits[w] = list
	}
	return hits
}

// checkCoverage proves nothing from the source went missing: a rule lost in
// optimisation is invisible in a rule count, because keywords lower it anyway.
func checkCoverage(source, lines []optRule) []string {
	sorted := append([]optRule(nil), source...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].prefix != sorted[j].prefix {
			return sorted[i].prefix < sorted[j].prefix
		}
		return sorted[i].value < sorted[j].value
	})

	var missing []string
	for _, r := range sorted {
		if !covered(r.prefix, r.value, lines) {
			missing = append(missing, r.String())
		}
	}
	return missing
}

// render writes one rule per line, dropping the keyword prefix: Xray reads a
// bare word as the same substring match. Only from the front -- searching the
// whole line would mangle a value containing the word.
func render(lines []optRule) []string {
	out := make([]string, 0, len(lines))
	for _, r := range lines {
		if r.prefix == pKeyword {
			out = append(out, r.value)
		} else {
			out = append(out, r.String())
		}
	}
	return out
}

// writeList puts one rule per line, LF, no BOM: the parity test compares bytes
// against the Python reference.
func writeList(path string, rules []optRule) error {
	return common.WriteLines(path, render(rules))
}

// readList reads one of our own text lists back; a bare word is a keyword.
func readList(path string) ([]optRule, error) {
	raw, err := common.ReadBlob(path)
	if err != nil {
		return nil, err
	}
	var out []optRule
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, ':'); i >= 0 {
			prefix := line[:i+1]
			// pKeyword is deliberately absent: Xray has no keyword: prefix, a
			// keyword is a bare word. Accepting one would hide the mistake.
			switch prefix {
			case pRegex, pDomain, pFull:
				out = append(out, optRule{prefix, line[i+1:], ""})
			default:
				return nil, fmt.Errorf("%s: unknown rule type %q; a keyword is a bare word, with no prefix", path, prefix)
			}
			continue
		}
		out = append(out, optRule{pKeyword, line, ""})
	}
	return out, nil
}
