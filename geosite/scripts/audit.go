package main

// Verifies the serialised bytes, not the builder's own structures: those are
// exactly what a bug would have corrupted.

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"

	"ehnax-geo-build/common"
)

// auditSite re-parses the serialised bytes and proves they say what the text
// lists say. Names are checked as raw bytes: the parser lower-cases on the way
// in, so it would report an all-upper-case file as correct.
func auditSite(blob []byte, want map[string][]optRule) error {
	var problems []string
	for name := range want {
		if !bytes.Contains(blob, []byte(name)) {
			problems = append(problems, fmt.Sprintf("%s is not in the file in lower case", name))
		}
		if bytes.Contains(blob, []byte(strings.ToUpper(name))) {
			problems = append(problems, fmt.Sprintf("%s is in the file in upper case", name))
		}
	}

	list := &routercommon.GeoSiteList{}
	if err := proto.Unmarshal(blob, list); err != nil {
		return fmt.Errorf("geosite.dat: output does not parse back: %w", err)
	}

	seenTag := map[string]bool{}
	for _, e := range list.GetEntry() {
		tag := e.GetCountryCode()
		seenTag[tag] = true

		expected, ok := want[tag]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is in the file but was never built", tag))
			continue
		}

		got := make([]optRule, 0, len(e.GetDomain()))
		for _, d := range e.GetDomain() {
			got = append(got, optRule{prefixOfType(d.GetType()), d.GetValue(), ""})
		}
		if len(got) != len(expected) {
			problems = append(problems, fmt.Sprintf("%s: %d rules read back, %d written", tag, len(got), len(expected)))
			continue
		}
		for i := range got {
			if got[i].String() != expected[i].String() {
				problems = append(problems, fmt.Sprintf("%s: rule %d reads back as %s, written %s",
					tag, i+1, got[i], expected[i]))
				break
			}
		}
	}
	for tag := range want {
		if !seenTag[tag] {
			problems = append(problems, fmt.Sprintf("tag %s vanished from the output", tag))
		}
	}
	return common.Report("geosite.dat", problems)
}
