package main

import (
	"fmt"
)

// geosite:private, read from geosite/private.src.txt rather than upstream.
//
// Upstream's private tag is 131 rules of vendor router admin hosts
// (router.asus.com, tplogin.cn, oasisauth.h3c.com), the IPv6 reverse delegation
// tree (*.ip6.arpa) and keyword shorthands for the same. A router resolves none
// of it: the admin UI is reached by address, and ip6.arpa is only ever looked up
// for a PTR record. The hand-written file keeps the part with a specification
// behind it and is safe to edit.

// privateRules parses the hand-written list into optimiser input.
func privateRules(path string) ([]optRule, error) {
	rules, err := readList(path)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s: parsed, but holds no rules", path)
	}
	return rules, nil
}
