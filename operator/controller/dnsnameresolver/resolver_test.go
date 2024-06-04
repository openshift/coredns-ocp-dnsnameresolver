package dnsnameresolver

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	networkv1alpha1 "github.com/openshift/api/network/v1alpha1"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type addParams struct {
	dnsName           string
	resolvedAddresses []networkv1alpha1.DNSNameResolverResolvedAddress
	matchesRegular    bool
	objName           string
}

type deleteParams struct {
	objName          string
	isDNSNameRemoved bool
	numRemoved       int
}

func TestResolver(t *testing.T) {
	tests := []struct {
		name                    string
		actions                 []string
		parameters              []interface{}
		expectedNextDNSNames    []string
		expectedNextLookupTimes []time.Time
		expectedNumIPs          []int
		expectedOutputs         []bool
	}{
		{
			name:    "Add a resolved name belonging to a regular DNSNameResolver object and then delete it",
			actions: []string{"Add", "Delete"},
			parameters: []interface{}{
				&addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: true,
					objName:        "regular",
				},
				&deleteParams{
					objName:          "regular",
					isDNSNameRemoved: true,
					numRemoved:       1,
				},
			},
			expectedNextDNSNames:    []string{"www.example.com.", ""},
			expectedNextLookupTimes: []time.Time{time.Now().Add(10 * time.Second)},
			expectedNumIPs:          []int{1, 0},
			expectedOutputs:         []bool{true, false},
		},
		{
			name:    "Add a resolved name belonging to a wildcard DNSNameResolver object and then delete it",
			actions: []string{"Add", "Delete"},
			parameters: []interface{}{
				&addParams{
					dnsName: "*.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.2",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
				&deleteParams{
					objName:          "wildcard",
					isDNSNameRemoved: true,
					numRemoved:       1,
				},
			},
			expectedNextDNSNames:    []string{"*.example.com.", ""},
			expectedNextLookupTimes: []time.Time{time.Now().Add(10 * time.Second)},
			expectedNumIPs:          []int{1, 0},
			expectedOutputs:         []bool{true, false},
		},
		{
			name: "Add a wildcard resolved name belonging to a wildcard DNSNameResolver object," +
				" then add a regular resolved name belonging to the wildcard DNSNameResolver object",
			actions: []string{"Add", "Add"},
			parameters: []interface{}{
				&addParams{
					dnsName: "*.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.2",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
						{
							IP:             "1.1.1.3",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				}, &addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
			},
			expectedNextDNSNames:    []string{"*.example.com.", "*.example.com."},
			expectedNextLookupTimes: []time.Time{time.Now().Add(8 * time.Second), time.Now().Add(8 * time.Second)},
			expectedNumIPs:          []int{2, 2},
			expectedOutputs:         []bool{true, true},
		},
		{
			name: "Add a wildcard resolved name belonging to a wildcard DNSNameResolver object," +
				" add a regular resolved name belonging to the wildcard DNSNameResolver object," +
				" then delete the wildcard DNSNameResolver object",
			actions: []string{"Add", "Add", "Delete"},
			parameters: []interface{}{
				&addParams{
					dnsName: "*.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.2",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
						{
							IP:             "1.1.1.3",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
				&addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
				&deleteParams{
					objName:          "wildcard",
					isDNSNameRemoved: true,
					numRemoved:       2,
				},
			},
			expectedNextDNSNames:    []string{"*.example.com.", "*.example.com.", ""},
			expectedNextLookupTimes: []time.Time{time.Now().Add(8 * time.Second), time.Now().Add(8 * time.Second)},
			expectedNumIPs:          []int{2, 2, 0},
			expectedOutputs:         []bool{true, true, false},
		},
		{
			name: "Add a wildcard resolved name belonging to a wildcard DNSNameResolver object," +
				" add a regular resolved name belonging to a regular DNSNameResolver object," +
				" add a regular resolved name belonging to the wildcard DNSNameResolver object," +
				" then delete the wildcard DNSNameResolver object",
			actions: []string{"Add", "Add", "Add", "Delete"},
			parameters: []interface{}{
				&addParams{
					dnsName: "*.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.2",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
						{
							IP:             "1.1.1.3",
							TTLSeconds:     8,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
				&addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: true,
					objName:        "regular",
				},
				&addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     10,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: false,
					objName:        "wildcard",
				},
				&deleteParams{
					objName:          "wildcard",
					isDNSNameRemoved: true,
					numRemoved:       1,
				},
			},
			expectedNextDNSNames:    []string{"*.example.com.", "*.example.com.", "*.example.com.", "www.example.com."},
			expectedNextLookupTimes: []time.Time{time.Now().Add(8 * time.Second), time.Now().Add(8 * time.Second), time.Now().Add(8 * time.Second), time.Now().Add(10 * time.Second)},
			expectedNumIPs:          []int{2, 2, 2, 1},
			expectedOutputs:         []bool{true, true, true, true},
		},
		{
			name: "Add a regular DNS name first with an IP address and with next lookup time past the current time," +
				" then add the same regular DNS name again without any resolved address",
			actions: []string{"Add", "Add"},
			parameters: []interface{}{
				&addParams{
					dnsName: "www.example.com.",
					resolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
						{
							IP:             "1.1.1.1",
							TTLSeconds:     -5,
							LastLookupTime: &v1.Time{Time: time.Now()},
						},
					},
					matchesRegular: true,
					objName:        "regular",
				},
				&addParams{
					dnsName:        "www.example.com.",
					matchesRegular: true,
					objName:        "regular",
				},
			},
			expectedNextDNSNames:    []string{"www.example.com.", "www.example.com."},
			expectedNextLookupTimes: []time.Time{time.Now().Add(-5 * time.Second), time.Now().Add(defaultMaxTTL)},
			expectedNumIPs:          []int{1, 0},
			expectedOutputs:         []bool{true, true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create the Resolver object.
			resolver := NewResolver(nil, "")
			for i := range tc.actions {
				switch tc.actions[i] {
				case "Add":
					// Get the parameters for the Add action.
					params := tc.parameters[i].(*addParams)

					// Call Add with the parameters.
					resolver.add(dnsDetails{
						dnsName:           params.dnsName,
						resolvedAddresses: params.resolvedAddresses,
						matchesRegular:    params.matchesRegular,
						objName:           params.objName,
					})

				case "Delete":
					// Get the parameters for the Delete action.
					params := tc.parameters[i].(*deleteParams)

					// Call delete with the parameters.
					resolver.delete(dnsDetails{objName: params.objName})
				default:
					assert.FailNow(t, "unknown action")
				}

				// Get the details of the next DNS name to be looked up.
				nextDNSName, nextLookupTime, numIPs, exists := resolver.getNextDNSNameDetails()
				assert.Equal(t, tc.expectedNextDNSNames[i], nextDNSName)
				assert.Equal(t, tc.expectedNumIPs[i], numIPs)
				assert.Equal(t, tc.expectedOutputs[i], exists)
				if exists {
					cmpOpts := cmpopts.EquateApproxTime(100 * time.Millisecond)
					diff := cmp.Diff(tc.expectedNextLookupTimes[i], nextLookupTime, cmpOpts)
					if diff != "" {
						t.Fatalf("unexpected next lookuptime (-want +got): %s\n", diff)
					}
				}
			}
		})
	}
}

func TestGetTimeTillNextLookup(t *testing.T) {
	tests := []struct {
		name                       string
		dnsExists                  bool
		remainingDuration          time.Duration
		expectedTimeTillNextLookup time.Duration
	}{
		{
			name:                       "DNS does not exist",
			dnsExists:                  false,
			remainingDuration:          0,
			expectedTimeTillNextLookup: defaultMaxTTL,
		},
		{
			name:                       "DNS exists and remaianing duration is greater than default max TTL",
			dnsExists:                  true,
			remainingDuration:          defaultMaxTTL + 1,
			expectedTimeTillNextLookup: defaultMaxTTL,
		},
		{
			name:                       "DNS exists and remaining duration is less than default max TTL",
			dnsExists:                  true,
			remainingDuration:          defaultMinTTL,
			expectedTimeTillNextLookup: defaultMinTTL,
		},
		{
			name:                       "DNS exists and remaining duration is not greater than 0",
			dnsExists:                  true,
			remainingDuration:          0,
			expectedTimeTillNextLookup: 2 * defaultMinTTL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			timeTillNextLookup := getTimeTillNextLookup(tc.dnsExists, tc.remainingDuration)
			assert.Equal(t, tc.expectedTimeTillNextLookup, timeTillNextLookup)
		})
	}
}
