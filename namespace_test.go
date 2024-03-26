package ocp_dnsnameresolver

import "testing"

func TestConfiguredNamespace(t *testing.T) {
	tests := []struct {
		expected      bool
		namespaces    map[string]struct{}
		testNamespace string
	}{
		{
			expected:      true,
			namespaces:    map[string]struct{}{"foobar": {}},
			testNamespace: "foobar",
		},
		{
			expected:      false,
			namespaces:    map[string]struct{}{"foobar": {}},
			testNamespace: "nsnoexist",
		},
		{
			expected:      true,
			namespaces:    map[string]struct{}{},
			testNamespace: "foobar",
		},
		{
			expected:      true,
			namespaces:    map[string]struct{}{},
			testNamespace: "nsnoexist",
		},
	}

	resolver := OCPDNSNameResolver{}
	for i, test := range tests {
		resolver.namespaces = test.namespaces
		actual := resolver.configuredNamespace(test.testNamespace)
		if actual != test.expected {
			t.Errorf("Test %d failed. Namespace %s was expected to be configured", i, test.testNamespace)
		}
	}
}
