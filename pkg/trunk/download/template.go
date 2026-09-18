package download

import (
	"fmt"
	"regexp"
	"strings"
)

// TemplateURL substitutes a download recipe's ${version}/${os}/${cpu} placeholders
// (ARCHITECTURE.md "downloads: url") with resolved values, plus any extra template variables a
// Download's own args: recipe derived (see ResolveArgs) -- extra may be nil.
func TemplateURL(url, version, os, cpu string, extra map[string]string) string {
	pairs := make([]string, 0, 6+2*len(extra))
	pairs = append(pairs, "${version}", version, "${os}", os, "${cpu}", cpu)
	for name, value := range extra {
		pairs = append(pairs, "${"+name+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(url)
}

// ResolveArgs computes the extra template variables a Download's own args: recipe derives (real
// catalog example: taplo's own `args: {semver: "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"}`,
// stripping a release-tag prefix trunk's real GitHub tags carry down to the bare semver GitHub
// actually names its release assets with). Each value is "<template>=><regex>": the template half
// is substituted using version/os/cpu exactly like TemplateURL does (with no extra vars of its own
// -- one arg can't reference another), then the regex half is matched against that substituted
// string, and every one of the regex's own NAMED capture groups becomes an entry in the returned
// map, keyed by the group's own name -- not by the arg's own key in the args: map (real taplo's
// happen to match, "semver" both times, but nothing requires it; a URL references the capture
// group's name directly, e.g. ${semver}). A nil/empty args returns an empty map with no error --
// the overwhelming majority of real Download recipes don't use this feature at all.
func ResolveArgs(args map[string]string, version, os, cpu string) (map[string]string, error) {
	resolved := map[string]string{}
	for key, raw := range args {
		template, pattern, ok := strings.Cut(raw, "=>")
		if !ok {
			return nil, fmt.Errorf("download: args %q: expected \"<template>=><regex>\", got %q", key, raw)
		}
		input := TemplateURL(template, version, os, cpu, nil)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("download: args %q: invalid regex %q: %w", key, pattern, err)
		}
		m := re.FindStringSubmatch(input)
		if m == nil {
			return nil, fmt.Errorf("download: args %q: regex %q did not match %q", key, pattern, input)
		}
		for i, name := range re.SubexpNames() {
			if name != "" {
				resolved[name] = m[i]
			}
		}
	}
	return resolved, nil
}
