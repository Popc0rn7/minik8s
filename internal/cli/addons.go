package cli

import (
	"fmt"
	"sort"
	"strings"
)

type AddonName string

const (
	AddonDNS        AddonName = "dns"
	AddonServerless AddonName = "serverless"
	AddonMetrics    AddonName = "metrics"
)

var knownAddons = []AddonName{AddonDNS, AddonServerless, AddonMetrics}

type addonSet map[AddonName]bool

func newAddonSet(names ...AddonName) addonSet {
	set := make(addonSet, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func defaultAddonSet() addonSet {
	return newAddonSet(AddonDNS, AddonMetrics)
}

func parseAddonSet(value string) (addonSet, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return newAddonSet(), nil
	}
	set := newAddonSet()
	for _, part := range strings.Split(value, ",") {
		name := AddonName(strings.ToLower(strings.TrimSpace(part)))
		if name == "" {
			continue
		}
		if !isKnownAddon(name) {
			return nil, fmt.Errorf("unknown addon %q", name)
		}
		set[name] = true
	}
	return set, nil
}

func (s addonSet) Enabled(name AddonName) bool {
	return s != nil && s[name]
}

func (s addonSet) Names() []AddonName {
	names := make([]AddonName, 0, len(s))
	for _, name := range knownAddons {
		if s.Enabled(name) {
			names = append(names, name)
		}
	}
	return names
}

func (s addonSet) String() string {
	names := s.Names()
	if len(names) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, string(name))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func isKnownAddon(name AddonName) bool {
	for _, known := range knownAddons {
		if known == name {
			return true
		}
	}
	return false
}
