// Package naming maps upstream tools to gateway tool names, <slug>__<tool>, and back.
//
// Slugs may not contain '_', so splitting on the first "__" always recovers the slug even
// when the upstream's own tool name contains "__".
package naming

import (
	"fmt"
	"regexp"
	"strings"
)

// Separator joins a server's slug and its tool name.
const Separator = "__"

// Gateway is the slug of the gateway's own tools, so no server may take it.
const Gateway = "mirage"

// maxNameLen is the longest tool name clients accept; the Claude API caps tool names at 64.
const maxNameLen = 64

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ValidateSlug reports whether slug can name a server: lowercase letters, digits, and
// single hyphens between them, at most 32 characters, other than Gateway.
func ValidateSlug(slug string) error {
	if len(slug) > 32 || !slugPattern.MatchString(slug) {
		return fmt.Errorf("server slug %q must be 1-32 lowercase letters, digits, or single hyphens", slug)
	}
	if slug == Gateway {
		return fmt.Errorf("server slug %q is reserved for the gateway's own tools", slug)
	}
	return nil
}

// Encode returns the gateway name for an upstream tool. It fails when the result would be
// too long or use characters clients reject.
func Encode(slug, tool string) (string, error) {
	if err := ValidateSlug(slug); err != nil {
		return "", err
	}
	name := slug + Separator + tool
	if len(name) > maxNameLen || !namePattern.MatchString(tool) {
		return "", fmt.Errorf("tool %q of server %s cannot be exposed: names must be at most %d characters of letters, digits, '_' or '-'",
			tool, slug, maxNameLen)
	}
	return name, nil
}

// Decode splits a gateway tool name into the server's slug and the upstream tool name.
func Decode(name string) (slug, tool string, ok bool) {
	slug, tool, ok = strings.Cut(name, Separator)
	if !ok || tool == "" || ValidateSlug(slug) != nil {
		return "", "", false
	}
	return slug, tool, true
}
