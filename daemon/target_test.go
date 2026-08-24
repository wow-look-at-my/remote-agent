package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTargetForms(t *testing.T) {
	tests := []struct {
		input string
		want  Endpoint
	}{
		{"host", Endpoint{Host: "host"}},
		{"user@host", Endpoint{User: "user", Host: "host"}},
		{"user@host:2222", Endpoint{User: "user", Host: "host", Port: 2222}},
		{"host:2222", Endpoint{Host: "host", Port: 2222}},
		{"root@127.0.0.1:2201", Endpoint{User: "root", Host: "127.0.0.1", Port: 2201}},
		{" root@127.0.0.1:2201 ", Endpoint{User: "root", Host: "127.0.0.1", Port: 2201}},
		{"::1", Endpoint{Host: "::1"}},
		{"user@[::1]:2222", Endpoint{User: "user", Host: "::1", Port: 2222}},
		{"[fe80::1]", Endpoint{Host: "fe80::1"}},
		{"my-alias", Endpoint{Host: "my-alias"}},
	}
	for _, tt := range tests {
		ep, err := ParseTarget(tt.input)
		require.NoError(t, err, tt.input)
		assert.Equal(t, tt.want, ep, tt.input)
	}
}

func TestParseTargetRejectsBadInput(t *testing.T) {
	for _, input := range []string{"", "   ", "host:", "host:0", "host:70000", "host:ssh", "user@", "[::1", "[::1]x"} {
		_, err := ParseTarget(input)
		assert.Error(t, err, input)
	}
}

func TestEndpointString(t *testing.T) {
	tests := []struct {
		ep   Endpoint
		want string
	}{
		{Endpoint{Host: "host"}, "host"},
		{Endpoint{User: "root", Host: "host"}, "root@host"},
		{Endpoint{User: "root", Host: "host", Port: 2201}, "root@host:2201"},
		{Endpoint{Host: "::1", Port: 22}, "[::1]:22"},
		{Endpoint{User: "root", Host: "::1"}, "root@[::1]"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.ep.String())
		// A rendered endpoint parses back to itself, so keys stay stable.
		back, err := ParseTarget(tt.want)
		require.NoError(t, err)
		assert.Equal(t, tt.want, back.String())
	}
}

func TestLoginFallsBackToEnv(t *testing.T) {
	assert.Equal(t, "root", Endpoint{User: "root", Host: "h"}.Login())
	t.Setenv("USER", "deploy")
	assert.Equal(t, "deploy", Endpoint{Host: "h"}.Login())
	t.Setenv("USER", "")
	assert.Equal(t, "root", Endpoint{Host: "h"}.Login())
}

func TestCanonicalTargetMergesPort(t *testing.T) {
	got, err := CanonicalTarget("root@127.0.0.1", 2201)
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", got)

	// A port in the target needs no help, and the same port twice agrees.
	got, err = CanonicalTarget("root@127.0.0.1:2201", 0)
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", got)

	got, err = CanonicalTarget("root@127.0.0.1:2201", 2201)
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", got)

	// A target with no port keeps none: ssh_config decides it later.
	got, err = CanonicalTarget("my-alias", 0)
	require.NoError(t, err)
	assert.Equal(t, "my-alias", got)
}

func TestCanonicalTargetRejectsTwoPorts(t *testing.T) {
	_, err := CanonicalTarget("root@127.0.0.1:2201", 2202)
	require.Error(t, err, "two different ports name two hosts; picking one silently is the bug")
	assert.Contains(t, err.Error(), "2201")
	assert.Contains(t, err.Error(), "2202")
}

func TestNormalizeTargetKeepsUnparseableText(t *testing.T) {
	assert.Equal(t, "host:bad", normalizeTarget("host:bad"))
	assert.Equal(t, "root@host:2201", normalizeTarget(" root@host:2201 "))
}
