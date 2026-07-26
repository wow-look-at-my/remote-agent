package fswire

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenFlagsRoundTrip(t *testing.T) {
	cases := []int{
		os.O_RDONLY,
		os.O_WRONLY,
		os.O_RDWR,
		os.O_WRONLY | os.O_APPEND,
		os.O_RDWR | os.O_TRUNC,
		os.O_WRONLY | os.O_EXCL,
		os.O_RDWR | os.O_APPEND | os.O_SYNC,
	}
	for _, local := range cases {
		got := LocalOpenFlags(PortableOpenFlags(local))
		assert.Equal(t, local, got, "flags 0x%x should survive the round trip", local)
	}
}

func TestPortableOpenFlagsAccessMode(t *testing.T) {
	assert.Equal(t, uint32(OpenRead), PortableOpenFlags(os.O_RDONLY))
	assert.Equal(t, uint32(OpenWrite), PortableOpenFlags(os.O_WRONLY))
	assert.Equal(t, uint32(OpenRead|OpenWrite), PortableOpenFlags(os.O_RDWR))
}

func TestLocalOpenFlagsNeverCreates(t *testing.T) {
	// O_CREATE has no wire representation: an open must never create a file
	// as a side effect of a flag translation.
	for portable := uint32(0); portable <= OpenRead|OpenWrite|OpenAppend|OpenTrunc|OpenExcl|OpenSync; portable++ {
		assert.Zero(t, LocalOpenFlags(portable)&os.O_CREATE, "portable flags 0x%x must not imply O_CREATE", portable)
	}
}

func TestUnknownPortableFlagsDefaultToReadOnly(t *testing.T) {
	assert.Equal(t, os.O_RDONLY, LocalOpenFlags(0))
}
