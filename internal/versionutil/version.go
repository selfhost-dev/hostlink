// Package versionutil provides utilities for parsing and comparing semantic versions.
package versionutil

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a semantic version string (e.g., "v1.2.3" or "1.2.3")
// and returns major, minor, patch components.
func Parse(version string) (major, minor, patch int, err error) {
	if version == "" {
		return 0, 0, 0, fmt.Errorf("empty version string")
	}

	// Strip optional 'v' prefix
	v := strings.TrimPrefix(version, "v")
	if v == "" {
		return 0, 0, 0, fmt.Errorf("invalid version format: %q", version)
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid version format: %q (expected major.minor.patch)", version)
	}

	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version in %q: %w", version, err)
	}

	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version in %q: %w", version, err)
	}

	patch, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid patch version in %q: %w", version, err)
	}

	return major, minor, patch, nil
}

// Compare compares two semantic version strings.
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
// Returns an error if either version string is invalid.
func Compare(v1, v2 string) (int, error) {
	major1, minor1, patch1, err := Parse(v1)
	if err != nil {
		return 0, fmt.Errorf("invalid version v1: %w", err)
	}

	major2, minor2, patch2, err := Parse(v2)
	if err != nil {
		return 0, fmt.Errorf("invalid version v2: %w", err)
	}

	// Compare major
	if major1 < major2 {
		return -1, nil
	}
	if major1 > major2 {
		return 1, nil
	}

	// Compare minor
	if minor1 < minor2 {
		return -1, nil
	}
	if minor1 > minor2 {
		return 1, nil
	}

	// Compare patch
	if patch1 < patch2 {
		return -1, nil
	}
	if patch1 > patch2 {
		return 1, nil
	}

	return 0, nil
}

// IsNewer returns true if candidate version is newer than current version.
// Returns an error if either version string is invalid.
func IsNewer(candidate, current string) (bool, error) {
	cmp, err := Compare(candidate, current)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}
