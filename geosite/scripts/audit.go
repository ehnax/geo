package main

// Verifies the serialised bytes, not the builder's own structures: those are
// exactly what a bug would have corrupted.

import (
	"fmt"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"

	"ehnax-geo-build/common"
)

// auditSite re-parses the serialised bytes and proves they say what the text
// lists say. Tag names are compared case-sensitively against the unmarshalled
// entries, because the reader that fails on the router looks them up as
// written and a byte scan would miss a case difference.
func auditSite(blob []byte, want map[string][]optRule) error {
	list := &routercommon.GeoSiteList{}
	if err := proto.Unmarshal(blob, list); err != nil {
		return fmt.Errorf("geosite.dat: output does not parse back: %w", err)
	}

	var problems []string
	seenTag := map[string]bool{}
	for _, e := range list.GetEntry() {
		tag := e.GetCountryCode()
		seenTag[tag] = true

		expected, ok := want[strings.ToLower(tag)]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is in the file but was never built", tag))
			continue
		}
		if tag != strings.ToUpper(tag) {
			problems = append(problems, fmt.Sprintf("%s is written as %q; Xray looks tags up in upper case", strings.ToLower(tag), tag))
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
		if !seenTag[strings.ToUpper(tag)] {
			problems = append(problems, fmt.Sprintf("tag %s vanished from the output", tag))
		}
	}
	return common.Report("geosite.dat", problems)
}
