package agent

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestParseKBValue(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1024 kB", 1024},
		{"0 kB", 0},
		{"16384 kB", 16384},
		{"100", 100},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseKBValue(tt.input)
		assert.Equal(t, tt.want, got)
	}
}
