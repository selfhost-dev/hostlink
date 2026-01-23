package versionutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_WithVPrefix(t *testing.T) {
	major, minor, patch, err := Parse("v1.2.3")
	require.NoError(t, err)
	assert.Equal(t, 1, major)
	assert.Equal(t, 2, minor)
	assert.Equal(t, 3, patch)
}

func TestParse_WithoutVPrefix(t *testing.T) {
	major, minor, patch, err := Parse("1.2.3")
	require.NoError(t, err)
	assert.Equal(t, 1, major)
	assert.Equal(t, 2, minor)
	assert.Equal(t, 3, patch)
}

func TestParse_ZeroVersion(t *testing.T) {
	major, minor, patch, err := Parse("v0.6.0")
	require.NoError(t, err)
	assert.Equal(t, 0, major)
	assert.Equal(t, 6, minor)
	assert.Equal(t, 0, patch)
}

func TestParse_LargeNumbers(t *testing.T) {
	major, minor, patch, err := Parse("v10.20.30")
	require.NoError(t, err)
	assert.Equal(t, 10, major)
	assert.Equal(t, 20, minor)
	assert.Equal(t, 30, patch)
}

func TestParse_InvalidFormat_TwoParts(t *testing.T) {
	_, _, _, err := Parse("v1.2")
	assert.Error(t, err)
}

func TestParse_InvalidFormat_FourParts(t *testing.T) {
	_, _, _, err := Parse("v1.2.3.4")
	assert.Error(t, err)
}

func TestParse_InvalidFormat_NonNumeric(t *testing.T) {
	_, _, _, err := Parse("v1.2.abc")
	assert.Error(t, err)
}

func TestParse_InvalidFormat_Empty(t *testing.T) {
	_, _, _, err := Parse("")
	assert.Error(t, err)
}

func TestParse_InvalidFormat_JustV(t *testing.T) {
	_, _, _, err := Parse("v")
	assert.Error(t, err)
}

func TestCompare_FirstLessThanSecond(t *testing.T) {
	tests := []struct {
		v1, v2 string
	}{
		{"v1.0.0", "v2.0.0"},
		{"v1.1.0", "v1.2.0"},
		{"v1.1.1", "v1.1.2"},
		{"v0.5.5", "v0.6.0"},
		{"v0.0.1", "v0.0.2"},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			result, err := Compare(tt.v1, tt.v2)
			require.NoError(t, err)
			assert.Equal(t, -1, result, "%s should be less than %s", tt.v1, tt.v2)
		})
	}
}

func TestCompare_FirstGreaterThanSecond(t *testing.T) {
	tests := []struct {
		v1, v2 string
	}{
		{"v2.0.0", "v1.0.0"},
		{"v1.2.0", "v1.1.0"},
		{"v1.1.2", "v1.1.1"},
		{"v0.6.0", "v0.5.5"},
		{"v1.0.0", "v0.99.99"},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			result, err := Compare(tt.v1, tt.v2)
			require.NoError(t, err)
			assert.Equal(t, 1, result, "%s should be greater than %s", tt.v1, tt.v2)
		})
	}
}

func TestCompare_Equal(t *testing.T) {
	tests := []struct {
		v1, v2 string
	}{
		{"v1.0.0", "v1.0.0"},
		{"v0.6.0", "v0.6.0"},
		{"1.2.3", "v1.2.3"}, // With and without v prefix
		{"v1.2.3", "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			result, err := Compare(tt.v1, tt.v2)
			require.NoError(t, err)
			assert.Equal(t, 0, result, "%s should equal %s", tt.v1, tt.v2)
		})
	}
}

func TestCompare_HandlesMissingVPrefix(t *testing.T) {
	result, err := Compare("1.2.3", "1.2.4")
	require.NoError(t, err)
	assert.Equal(t, -1, result)

	result, err = Compare("1.2.4", "1.2.3")
	require.NoError(t, err)
	assert.Equal(t, 1, result)
}

func TestCompare_ErrorOnInvalidV1(t *testing.T) {
	_, err := Compare("invalid", "v1.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid version v1")
}

func TestCompare_ErrorOnInvalidV2(t *testing.T) {
	_, err := Compare("v1.0.0", "invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid version v2")
}

func TestCompare_ErrorOnMalformedVersions(t *testing.T) {
	tests := []struct {
		name   string
		v1, v2 string
	}{
		{"two parts", "v1.2", "v1.0.0"},
		{"prerelease suffix", "v1.2.3-beta", "v1.0.0"},
		{"build metadata", "v1.2.3+build", "v1.0.0"},
		{"empty string", "", "v1.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compare(tt.v1, tt.v2)
			assert.Error(t, err, "Compare(%q, %q) should return error", tt.v1, tt.v2)
		})
	}
}

func TestIsNewer_True(t *testing.T) {
	tests := []struct {
		candidate, current string
	}{
		{"v0.6.0", "v0.5.5"},
		{"v1.0.0", "v0.9.9"},
		{"v2.0.0", "v1.99.99"},
		{"v1.1.0", "v1.0.0"},
		{"v1.0.1", "v1.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.candidate+"_newer_than_"+tt.current, func(t *testing.T) {
			result, err := IsNewer(tt.candidate, tt.current)
			require.NoError(t, err)
			assert.True(t, result, "%s should be newer than %s", tt.candidate, tt.current)
		})
	}
}

func TestIsNewer_False_Older(t *testing.T) {
	tests := []struct {
		candidate, current string
	}{
		{"v0.5.5", "v0.6.0"},
		{"v0.9.9", "v1.0.0"},
		{"v1.0.0", "v1.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.candidate+"_not_newer_than_"+tt.current, func(t *testing.T) {
			result, err := IsNewer(tt.candidate, tt.current)
			require.NoError(t, err)
			assert.False(t, result, "%s should not be newer than %s", tt.candidate, tt.current)
		})
	}
}

func TestIsNewer_False_Equal(t *testing.T) {
	result, err := IsNewer("v0.6.0", "v0.6.0")
	require.NoError(t, err)
	assert.False(t, result, "same version should not be newer")
}

func TestIsNewer_ErrorOnInvalidCandidate(t *testing.T) {
	_, err := IsNewer("v1.2", "v1.0.0")
	assert.Error(t, err)
}

func TestIsNewer_ErrorOnInvalidCurrent(t *testing.T) {
	_, err := IsNewer("v1.0.0", "v1.2")
	assert.Error(t, err)
}

func TestIsNewer_ErrorOnPrereleaseVersion(t *testing.T) {
	// Prerelease versions like "v1.2.3-beta" should return error
	// because our simple parser doesn't support them
	_, err := IsNewer("v1.2.3-beta", "v1.0.0")
	assert.Error(t, err, "prerelease versions should return error")
}
