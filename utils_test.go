package ocp_dnsnameresolver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsWildcard(t *testing.T) {
	tests := []struct {
		dnsName        string
		expectedOutput bool
	}{
		// success
		{"*.example.com", true},
		{"*.sub1.example.com", true},
		// negative
		{"www.example.com", false},
		{"sub2.sub1.example.com", false},
	}

	for _, test := range tests {
		actualOutput := isWildcard(test.dnsName)
		require.Equal(t, actualOutput, test.expectedOutput)
	}
}

func TestGetWildcard(t *testing.T) {
	tests := []struct {
		dnsName        string
		expectedOutput string
	}{
		{"www.example.com", "*.example.com"},
		{"sub2.sub1.example.com", "*.sub1.example.com"},
		{"*.example.com", "*.example.com"},
		{"*.sub1.example.com", "*.sub1.example.com"},
	}

	for _, test := range tests {
		actualOutput := getWildcard(test.dnsName)
		require.Equal(t, actualOutput, test.expectedOutput)
	}
}

func TestIsSameNextLookupTime(t *testing.T) {
	tests := []struct {
		name                   string
		existingLastLookupTime time.Time
		existingTTL            int32
		currentTTL             int32
		expectedOutput         bool
	}{
		{
			name:                   "Same existing next lookup time and current next lookup time",
			existingLastLookupTime: time.Now(),
			existingTTL:            2,
			currentTTL:             2,
			expectedOutput:         true,
		},
		{
			name:                   "Existing next lookup time is after current next lookup time but within the margin",
			existingLastLookupTime: time.Now(),
			existingTTL:            2,
			currentTTL:             1,
			expectedOutput:         true,
		},
		{
			name:                   "Existing next lookup time is before current next lookup time but within the margin",
			existingLastLookupTime: time.Now().Add(-3 * time.Second),
			existingTTL:            2,
			currentTTL:             1,
			expectedOutput:         true,
		},
		{
			name:                   "Existing next lookup time is after current next lookup time and also outside the margin",
			existingLastLookupTime: time.Now(),
			existingTTL:            7,
			currentTTL:             1,
			expectedOutput:         false,
		},
		{
			name:                   "Existing next lookup time is before current next lookup time and also outside the margin",
			existingLastLookupTime: time.Now().Add(-6 * time.Second),
			existingTTL:            1,
			currentTTL:             1,
			expectedOutput:         false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualOutput := isSameNextLookupTime(tc.existingLastLookupTime, tc.existingTTL, tc.currentTTL)
			if actualOutput != tc.expectedOutput {
				t.Fatalf("Actual output does not match with expected output. Actual output: %t, Expected output: %t", actualOutput, tc.expectedOutput)
			}
		})
	}
}
