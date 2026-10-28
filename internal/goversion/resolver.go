package goversion

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Normalize strips any leading "go" or "v" prefix and whitespace from a Go version string.
// For example, "go1.27.3" -> "1.27.3", "v1.27.0" -> "1.27.0", "1.22rc1" -> "1.22rc1".
func Normalize(v string) string {
	s := strings.TrimSpace(strings.ToLower(v))
	s = strings.TrimPrefix(s, "go")
	s = strings.TrimPrefix(s, "v")
	return s
}

// ToGoTag converts a normalized version back to the official Go tag ("1.27.3" -> "go1.27.3").
func ToGoTag(v string) string {
	norm := Normalize(v)
	if norm == "" {
		return ""
	}
	return "go" + norm
}

type parsedVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string // e.g. "rc1", "beta2", "" for stable
	raw        string
}

func parseVersion(v string) parsedVersion {
	norm := Normalize(v)
	pv := parsedVersion{raw: norm}
	if norm == "" {
		return pv
	}

	// Separate prerelease suffix like "1.27rc1" or "1.27.0-rc1" or "1.27beta1"
	var corePart, prePart string
	idx := -1
	for i, ch := range norm {
		if (ch < '0' || ch > '9') && ch != '.' {
			idx = i
			break
		}
	}
	if idx >= 0 {
		corePart = norm[:idx]
		prePart = strings.TrimPrefix(norm[idx:], "-")
	} else {
		corePart = norm
	}
	pv.prerelease = prePart

	parts := strings.Split(corePart, ".")
	if len(parts) > 0 {
		pv.major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		pv.minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		pv.patch, _ = strconv.Atoi(parts[2])
	}
	return pv
}

// CompareSemver compares two Go version strings (e.g. "1.27.3" vs "1.26.9", "1.27.0" vs "1.27rc1").
// Returns -1 if a < b, 0 if a == b, +1 if a > b.
func CompareSemver(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)

	if pa.major != pb.major {
		if pa.major < pb.major {
			return -1
		}
		return 1
	}
	if pa.minor != pb.minor {
		if pa.minor < pb.minor {
			return -1
		}
		return 1
	}
	if pa.patch != pb.patch {
		if pa.patch < pb.patch {
			return -1
		}
		return 1
	}

	// Stable (empty prerelease) is higher than any prerelease (rc/beta)
	if pa.prerelease == "" && pb.prerelease != "" {
		return 1
	}
	if pa.prerelease != "" && pb.prerelease == "" {
		return -1
	}
	return strings.Compare(pa.prerelease, pb.prerelease)
}

// MatchesSpec returns true if candidateVersion matches the requested version spec.
// Spec can be:
// - "latest" or "stable" (handled by caller, or matches any stable)
// - exact match ("1.27.3" matches "1.27.3", "1.21.0" matches "1.21" or "1.21.0")
// - major.minor prefix ("1.27" matches "1.27", "1.27.0", "1.27.3", etc.)
func MatchesSpec(candidateVersion, spec string) bool {
	candNorm := Normalize(candidateVersion)
	specNorm := Normalize(spec)
	if candNorm == "" || specNorm == "" {
		return false
	}
	if candNorm == specNorm {
		return true
	}

	pc := parseVersion(candNorm)
	ps := parseVersion(specNorm)

	// If spec has a prerelease (e.g., "1.27rc1"), require exact match
	if ps.prerelease != "" {
		return candNorm == specNorm
	}

	specDots := strings.Count(specNorm, ".")
	if specDots == 1 {
		// Major.Minor prefix (e.g. "1.27") matches any stable patch release of that major.minor
		return pc.major == ps.major && pc.minor == ps.minor && pc.prerelease == ""
	}
	if specDots == 2 && ps.patch == 0 {
		// In Go pre-1.21, "1.20" had tag "go1.20" (patch 0). Allow "1.20.0" to match "1.20".
		return pc.major == ps.major && pc.minor == ps.minor && pc.patch == 0 && pc.prerelease == ""
	}
	return false
}

// ResolveFromList finds the highest matching version from a slice of version strings.
func ResolveFromList(query string, versions []string) (string, error) {
	q := Normalize(query)
	if q == "" {
		return "", fmt.Errorf("empty version query")
	}

	sorted := slices.Clone(versions)
	slices.SortFunc(sorted, func(a, b string) int {
		// Sort descending (newest first)
		return CompareSemver(b, a)
	})

	if q == "latest" || q == "stable" {
		for _, v := range sorted {
			pv := parseVersion(v)
			if pv.prerelease == "" {
				return Normalize(v), nil
			}
		}
		if len(sorted) > 0 {
			return Normalize(sorted[0]), nil
		}
		return "", fmt.Errorf("no versions available")
	}

	// 1. Exact match first
	for _, v := range sorted {
		if Normalize(v) == q {
			return Normalize(v), nil
		}
	}

	// 2. Prefix match (sorted descending, so first match is highest patch)
	for _, v := range sorted {
		if MatchesSpec(v, q) {
			return Normalize(v), nil
		}
	}

	return "", fmt.Errorf("version %q not found", query)
}

// ResolveRemoteVersion resolves a version query (e.g. "latest", "1.27", "1.27.3", "go1.26.1")
// against official Go releases and returns the matching Release and OS/Arch ReleaseFile.
func ResolveRemoteVersion(query string, releases []Release, goos, goarch string) (*Release, *ReleaseFile, error) {
	q := Normalize(query)
	if q == "" {
		return nil, nil, fmt.Errorf("version cannot be empty")
	}

	// Sort releases descending by semver
	sorted := slices.Clone(releases)
	slices.SortFunc(sorted, func(a, b Release) int {
		return CompareSemver(b.Version, a.Version)
	})

	findArchiveFile := func(rel *Release) *ReleaseFile {
		for i := range rel.Files {
			f := &rel.Files[i]
			if f.Kind == "archive" && f.OS == goos && f.Arch == goarch {
				return f
			}
		}
		return nil
	}

	if q == "latest" || q == "stable" {
		for i := range sorted {
			rel := &sorted[i]
			if !rel.Stable {
				continue
			}
			if rf := findArchiveFile(rel); rf != nil {
				return rel, rf, nil
			}
		}
		return nil, nil, fmt.Errorf("no stable Go release found for %s/%s", goos, goarch)
	}

	// 1. Check exact match first
	for i := range sorted {
		rel := &sorted[i]
		if Normalize(rel.Version) == q {
			rf := findArchiveFile(rel)
			if rf == nil {
				return nil, nil, fmt.Errorf("Go %s exists but has no archive for platform %s/%s", Normalize(rel.Version), goos, goarch)
			}
			return rel, rf, nil
		}
	}

	// 2. Check prefix match (e.g. "1.27" -> highest "1.27.x")
	for i := range sorted {
		rel := &sorted[i]
		if MatchesSpec(rel.Version, q) {
			if rf := findArchiveFile(rel); rf != nil {
				return rel, rf, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("Go version %q not found in official releases for %s/%s", query, goos, goarch)
}
