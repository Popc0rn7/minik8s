package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCPUQuantity(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "500m", want: 500_000_000},
		{value: "1", want: 1_000_000_000},
		{value: "2.5", want: 2_500_000_000},
	}
	for _, tt := range tests {
		got, err := ParseCPUQuantity(tt.value)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestParseMemoryQuantity(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "128Mi", want: 134_217_728},
		{value: "1Gi", want: 1_073_741_824},
		{value: "1000000", want: 1_000_000},
	}
	for _, tt := range tests {
		got, err := ParseMemoryQuantity(tt.value)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}
