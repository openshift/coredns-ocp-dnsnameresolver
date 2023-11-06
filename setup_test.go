package ocp_dnsnameresolver

import (
	"testing"

	"github.com/coredns/caddy"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		input                    string // Corefile data as string
		shouldErr                bool   // true if test case is expected to produce an error.
		expectedNSCount          int    // expected count of namespaces.
		expectedMinTTL           int32  // expected value of minTTL.
		expectedFailureThreshold int32  // expected value of failureThreshold.
	}{
		// default
		{`ocp_dnsnameresolver`, false, 0, defaultMinTTL, defaultFailureThreshold},
		// namespaces
		{`ocp_dnsnameresolver {
				namespaces foo
			}`, false, 1, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			namespaces foo bar
		}`, false, 2, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			namespaces foo bar foobar
		}`, false, 3, defaultMinTTL, defaultFailureThreshold},
		// minTTL
		{`ocp_dnsnameresolver {
			minTTL 1
		}`, false, 0, 1, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			minTTL 10
		}`, false, 0, 10, defaultFailureThreshold},
		// failureThreshold
		{`ocp_dnsnameresolver {
			failureThreshold 1
		}`, false, 0, defaultMinTTL, 1},
		{`ocp_dnsnameresolver {
			failureThreshold 10
		}`, false, 0, defaultMinTTL, 10},
		// fails
		{`ocp_dnsnameresolver {
			namespaces
		}`, true, 0, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			minTTL
		}`, true, 0, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			minTTL 0
		}`, true, 0, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			failureThreshold
		}`, true, 0, defaultMinTTL, defaultFailureThreshold},
		{`ocp_dnsnameresolver {
			failureThreshold 0
		}`, true, 0, defaultMinTTL, defaultFailureThreshold},
	}
	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		resolver, err := resolverParse(c)

		if test.shouldErr && err == nil {
			t.Errorf("Test %d: Expected error, but did not find error for input '%s'. Error was: '%v'", i, test.input, err)
		}

		if err != nil {
			if !test.shouldErr {
				t.Errorf("Test %d: Expected no error but found one for input %s. Error was: %v", i, test.input, err)
				continue
			}
		}

		if test.shouldErr && err != nil {
			continue
		}

		// namespaces
		foundNSCount := len(resolver.namespaces)
		if foundNSCount != test.expectedNSCount {
			t.Errorf("Test %d: Expected kubernetes controller to be initialized with %d namespaces. Instead found %d namespaces: '%v' for input '%s'", i, test.expectedNSCount, foundNSCount, resolver.namespaces, test.input)
		}

		// minTTL
		if resolver.minimumTTL != test.expectedMinTTL {
			t.Errorf("Test %d: Expected kubernetes controller to be initialized with minTTL '%d'. Instead found minTTL '%d' for input '%s'", i, test.expectedMinTTL, resolver.minimumTTL, test.input)
		}

		// failureThreshold
		if resolver.failureThreshold != test.expectedFailureThreshold {
			t.Errorf("Test %d: Expected kubernetes controller to be initialized with failureThreshold '%d'. Instead found failureThreshold '%d' for input '%s'", i, test.expectedFailureThreshold, resolver.failureThreshold, test.input)
		}
	}
}
