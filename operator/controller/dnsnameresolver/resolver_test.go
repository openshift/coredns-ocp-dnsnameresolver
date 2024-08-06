package dnsnameresolver

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/miekg/dns"
	ocpnetworkv1alpha1 "github.com/openshift/api/network/v1alpha1"
	"github.com/stretchr/testify/assert"

	discoveryv1 "k8s.io/api/discovery/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type addParams struct {
	dnsName           string
	resolvedAddresses []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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
					resolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
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

func TestSendDNSLookupRequest(t *testing.T) {
	tests := []struct {
		name        string
		dnsName     string
		recordType  uint16
		ipAddresses []string
		ttl         uint32
		rcode       int
	}{
		{
			name:        "Lookup for DNS name with IPv4 addresses",
			dnsName:     "www.example.com.",
			recordType:  dns.TypeA,
			ipAddresses: []string{"1.1.1.1", "1.1.1.2"},
			ttl:         30,
			rcode:       dns.RcodeSuccess,
		},
		{
			name:        "Lookup for DNS name with IPv6 address",
			dnsName:     "www.example.com.",
			recordType:  dns.TypeAAAA,
			ipAddresses: []string{"2001:0db8:85a3:0000:0000:8a2e:0370:7334"},
			ttl:         30,
			rcode:       dns.RcodeSuccess,
		},
		{
			name:       "Lookup for DNS name failure",
			dnsName:    "www.example.com.",
			recordType: dns.TypeA,
			rcode:      dns.RcodeNameError,
		},
	}

	dnsServerIP := "127.0.0.1"
	dnsServerPort := "8053"

	// Start a test dns server.
	testServer := &dns.Server{Addr: net.JoinHostPort(dnsServerIP, dnsServerPort), Net: "udp"}
	go testServer.ListenAndServe()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create the handler function for current test case.
			handleRequest := func(w dns.ResponseWriter, r *dns.Msg) {
				m := new(dns.Msg)
				m.SetRcode(r, tc.rcode)
				if tc.rcode == dns.RcodeSuccess {
					records := []dns.RR{}
					switch tc.recordType {
					case dns.TypeA:
						for _, ipAddr := range tc.ipAddresses {
							records = append(records, &dns.A{
								Hdr: dns.RR_Header{
									Name:   tc.dnsName,
									Rrtype: tc.recordType,
									Ttl:    tc.ttl,
								},
								A: net.ParseIP(ipAddr),
							})
						}
					case dns.TypeAAAA:
						for _, ipAddr := range tc.ipAddresses {
							records = append(records, &dns.AAAA{
								Hdr: dns.RR_Header{
									Name:   tc.dnsName,
									Rrtype: tc.recordType,
									Ttl:    tc.ttl,
								},
								AAAA: net.ParseIP(ipAddr),
							})
						}
					}
					m.Answer = append(m.Answer, records...)
				}
				w.WriteMsg(m)
			}
			// Register the handler function.
			dns.HandleFunc(".", handleRequest)

			// Send the DNS lookup request to the test server and check the response.
			msg, _, err := sendDNSLookupRequest(&dns.Client{}, dnsServerIP, dnsServerPort, tc.dnsName, tc.recordType)
			if err != nil {
				t.Fatalf("Unexpected error while sending DNS lookup request: %v", err)
			}
			if msg.Question[0].Name != tc.dnsName {
				t.Fatalf("DNS lookup not matching DNS name. Expected: %s, Actual: %s", tc.dnsName, msg.Question[0].Name)
			}
			if msg.Rcode != tc.rcode {
				t.Fatalf("Rcode not matching. Expected: %d, Actual: %d", tc.rcode, msg.Rcode)
			}
			if tc.rcode == dns.RcodeSuccess {
				for i, answer := range msg.Answer {
					switch msg.Question[0].Qtype {
					case dns.TypeA:
						rec, ok := answer.(*dns.A)
						if !ok {
							t.Fatalf("Unexpected dns type: %v", reflect.TypeOf(answer))
						}
						if rec.A.String() != net.ParseIP(tc.ipAddresses[i]).String() {
							t.Fatalf("IP address not matching. Expected: %s, Actual: %s", tc.ipAddresses[i], rec.A.String())
						}
						if rec.Hdr.Ttl != tc.ttl {
							t.Fatalf("TTL not matching. Expected: %d, Actual: %d", tc.ttl, rec.Hdr.Ttl)
						}
					case dns.TypeAAAA:
						rec, ok := answer.(*dns.AAAA)
						if !ok {
							t.Fatalf("Unexpected dns type: %v", reflect.TypeOf(answer))
						}
						if rec.AAAA.String() != net.ParseIP(tc.ipAddresses[i]).String() {
							t.Fatalf("IP address not matching. Expected: %s, Actual: %s", tc.ipAddresses[i], rec.AAAA.String())
						}
						if rec.Hdr.Ttl != tc.ttl {
							t.Fatalf("TTL not matching. Expected: %d, Actual: %d", tc.ttl, rec.Hdr.Ttl)
						}
					default:
						t.Fatalf("Unexpected record type: %d", msg.Question[0].Qtype)
					}
				}
			}
		})
	}
	testServer.Shutdown()
}

func TestGetRandomCoreDNSPodIPs(t *testing.T) {
	tests := []struct {
		name                  string
		readyCoreDNSPodIPs    []string
		notReadyCoreDNSPodIPs []string
		maxIPs                int
		expectedError         bool
	}{
		{
			name:               "Less than max IPs",
			readyCoreDNSPodIPs: []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			maxIPs:             5,
			expectedError:      false,
		},
		{
			name:               "Equal to max IPs",
			readyCoreDNSPodIPs: []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5"},
			maxIPs:             5,
			expectedError:      false,
		},
		{
			name:               "More than max IPs",
			readyCoreDNSPodIPs: []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5", "6.6.6.6"},
			maxIPs:             5,
			expectedError:      false,
		},
		{
			name:                  "Only use ready endpoint addresses",
			readyCoreDNSPodIPs:    []string{"1.1.1.1"},
			notReadyCoreDNSPodIPs: []string{"2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5", "6.6.6.6"},
			maxIPs:                5,
			expectedError:         false,
		},
		{
			name:                  "No endpoint address available",
			notReadyCoreDNSPodIPs: []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5", "6.6.6.6"},
			maxIPs:                5,
			expectedError:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create the endpoint slice object for CoreDNS pods and
			// use the fake client to create the object. Initialize
			// fake cache using the fake client.
			epSlice := &discoveryv1.EndpointSlice{
				ObjectMeta: v1.ObjectMeta{
					Name: "dns-default",
				},
				Endpoints: []discoveryv1.Endpoint{
					{
						Conditions: discoveryv1.EndpointConditions{
							Ready: ptr.To(true),
						},
						Addresses: tc.readyCoreDNSPodIPs,
					},
					{
						Conditions: discoveryv1.EndpointConditions{
							Ready: ptr.To(false),
						},
						Addresses: tc.readyCoreDNSPodIPs,
					},
				},
			}
			fakeClient := fake.NewFakeClient(epSlice)
			cache := &fakeCache{
				FakeInformers: &informertest.FakeInformers{},
				Reader:        fakeClient,
			}

			// Initialize the resolver using the fake cache.
			resolver := NewResolver(cache, "")

			// Call the getRandomCoreDNSPodIPs() function and check the response.
			ips, err := resolver.getRandomCoreDNSPodIPs(tc.maxIPs)
			if err != nil {
				if !tc.expectedError {
					t.Fatalf("Unexpected error while getting random CoreDNS pod IPs: %v", err)
				}
			} else {
				if tc.expectedError {
					t.Fatal("Expected error while getting random CoreDNS pod IPs but got none")
				}
				if len(tc.readyCoreDNSPodIPs) < tc.maxIPs && len(ips) != len(tc.readyCoreDNSPodIPs) {
					t.Fatalf("Unexpected number of Pod IPs returned. Expected number of IPs: %d, Actual number of IPs: %d", len(tc.readyCoreDNSPodIPs), len(ips))
				}
				if len(tc.readyCoreDNSPodIPs) >= tc.maxIPs && len(ips) != tc.maxIPs {
					t.Fatalf("Unexpected number of Pod IPs returned. Expected number of IPs: %d, Actual number of IPs: %d", tc.maxIPs, len(ips))
				}
				uniqueIPs := sets.New(ips...)
				if len(ips) != uniqueIPs.Len() {
					t.Fatalf("Same IP address returned more than once: %v", ips)
				}
				notReadyIPIntersection := uniqueIPs.Intersection(sets.New(tc.notReadyCoreDNSPodIPs...))
				if notReadyIPIntersection.Len() != 0 {
					t.Fatalf("Not ready IP addresses returned: %v", notReadyIPIntersection.UnsortedList())
				}

			}
		})
	}
}

type fakeCache struct {
	*informertest.FakeInformers
	client.Reader
}

func (fcache *fakeCache) Start(ctx context.Context) error {
	return fcache.FakeInformers.Start(ctx)
}

func (fcache *fakeCache) WaitForCacheSync(ctx context.Context) bool {
	return fcache.FakeInformers.WaitForCacheSync(ctx)
}

func (fcache *fakeCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return fcache.Reader.Get(ctx, key, obj, opts...)
}

func (fcache *fakeCache) List(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	return fcache.Reader.List(ctx, obj, opts...)
}
