// Package version reports the build identity and compares release tags.
//
// The three variables are filled at link time with -ldflags -X. A binary built
// without those flags reports "dev", which the updater treats as older than any
// published release.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Injected by the build. Keep them as plain strings so -X can set them.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is the JSON shape served to the admin panel.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func Current() Info { return Info{Version: Version, Commit: Commit, Date: Date} }

func String() string {
	if Commit == "unknown" || Commit == "" {
		return Version
	}
	if len(Commit) > 7 {
		return Version + " (" + Commit[:7] + ")"
	}
	return Version + " (" + Commit + ")"
}

// Parsed is a dotted numeric version. Pre-release and build metadata are
// ignored for ordering: v1.2.0-rc1 is treated as 1.2.0, which keeps the
// updater from offering a pre-release to a stable install.
type Parsed struct {
	Major, Minor, Patch int
}

// Parse accepts "v1.2.3", "1.2.3" and "v1.2" (patch defaults to 0). Anything
// else is an error so a malformed tag cannot silently look like an upgrade.
func Parse(tag string) (Parsed, error) {
	raw := strings.TrimSpace(tag)
	raw = strings.TrimPrefix(raw, "v")
	if raw == "" {
		return Parsed{}, fmt.Errorf("version: empty tag")
	}
	// Drop pre-release and build metadata.
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Parsed{}, fmt.Errorf("version: invalid tag %q", tag)
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		if part == "" {
			return Parsed{}, fmt.Errorf("version: invalid tag %q", tag)
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Parsed{}, fmt.Errorf("version: invalid tag %q", tag)
		}
		numbers[i] = value
	}
	return Parsed{numbers[0], numbers[1], numbers[2]}, nil
}

// Compare returns -1, 0 or 1. Invalid local versions sort below everything so
// a dev build always sees the published release as newer.
func Compare(a, b string) int {
	left, errLeft := Parse(a)
	right, errRight := Parse(b)
	switch {
	case errLeft != nil && errRight != nil:
		return 0
	case errLeft != nil:
		return -1
	case errRight != nil:
		return 1
	}
	if left.Major != right.Major {
		return sign(left.Major - right.Major)
	}
	if left.Minor != right.Minor {
		return sign(left.Minor - right.Minor)
	}
	return sign(left.Patch - right.Patch)
}

// Newer reports whether remote is a strictly newer release than local.
func Newer(remote, local string) bool { return Compare(remote, local) > 0 }

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

// IsDev reports whether this binary predates version tagging. The updater
// allows a dev build to move to any release.
func IsDev() bool { _, err := Parse(Version); return err != nil }
