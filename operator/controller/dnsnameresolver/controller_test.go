package dnsnameresolver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/miekg/dns"
	ocpnetworkv1alpha1 "github.com/openshift/api/network/v1alpha1"
	"github.com/stretchr/testify/assert"

	discoveryv1 "k8s.io/api/discovery/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestIsWildcard(t *testing.T) {
	tests := []struct {
		dnsName        string
		expectedOutput bool
	}{
		// success
		{
			dnsName:        "*.example.com",
			expectedOutput: true,
		},
		{
			dnsName:        "*.sub1.example.com",
			expectedOutput: true,
		},
		// negative
		{
			dnsName:        "www.example.com",
			expectedOutput: false,
		},
		{
			dnsName:        "sub2.sub1.example.com",
			expectedOutput: false,
		},
	}

	for _, tc := range tests {
		actualOutput := isWildcard(tc.dnsName)
		assert.Equal(t, tc.expectedOutput, actualOutput)
	}
}

func TestRemovalOfIPsRequired(t *testing.T) {
	tests := []struct {
		name           string
		status         *ocpnetworkv1alpha1.DNSNameResolverStatus
		expectedStatus *ocpnetworkv1alpha1.DNSNameResolverStatus
		expectedOutput bool
	}{
		{
			name: "None of the TTL of the IP addresses expired",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
							{
								IP:             "1.1.1.2",
								TTLSeconds:     8,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.3",
								TTLSeconds:     30,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
						},
					},
				},
			},
			expectedStatus: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 10,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 8,
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.3",
								TTLSeconds: 30,
							},
						},
					},
				},
			},
			expectedOutput: false,
		},
		{
			name: "The TTL of IP addresses expired, but grace period not over",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-12 * time.Second)},
							},
							{
								IP:             "1.1.1.2",
								TTLSeconds:     8,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-12 * time.Second)},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.3",
								TTLSeconds:     30,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-12 * time.Second)},
							},
						},
					},
				},
			},
			expectedStatus: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 10,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 8,
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.3",
								TTLSeconds: 30,
							},
						},
					},
				},
			},
			expectedOutput: false,
		},
		{
			name: "The TTL of IP addresses expired, and grace period of one IP address is also over",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-14 * time.Second)},
							},
							{
								IP:             "1.1.1.2",
								TTLSeconds:     8,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-14 * time.Second)},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.3",
								TTLSeconds:     30,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-14 * time.Second)},
							},
						},
					},
				},
			},
			expectedStatus: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 10,
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.3",
								TTLSeconds: 30,
							},
						},
					},
				},
			},
			expectedOutput: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualOutput := removalOfIPsRequired(tc.status)
			assert.Equal(t, tc.expectedOutput, actualOutput)
			cmpOpts := []cmp.Option{
				cmpopts.IgnoreFields(ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{}, "LastLookupTime"),
				cmpopts.EquateApproxTime(100 * time.Millisecond),
				cmpopts.SortSlices(func(elem1, elem2 ocpnetworkv1alpha1.DNSNameResolverResolvedAddress) bool {
					return elem1.IP > elem2.IP
				}),
			}
			diff := cmp.Diff(tc.expectedStatus, tc.status, cmpOpts...)
			if diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestReconcileRequired(t *testing.T) {
	tests := []struct {
		name                  string
		status                *ocpnetworkv1alpha1.DNSNameResolverStatus
		expectedTTLExpired    bool
		expectedRemainingTime time.Duration
	}{
		{
			name: "TTL expired of one IP address of a resolved name",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.2",
								TTLSeconds:     4,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-5 * time.Second)},
							},
						},
					},
				},
			},
			expectedTTLExpired:    true,
			expectedRemainingTime: ipRemovalGracePeriod - 1*time.Second,
		},
		{
			name: "TTL expired of IP addresses of different resolved names, return minimum remaining time till grace period",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-12 * time.Second)},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.2",
								TTLSeconds:     4,
								LastLookupTime: &v1.Time{Time: time.Now().Add(-5 * time.Second)},
							},
						},
					},
				},
			},
			expectedTTLExpired:    true,
			expectedRemainingTime: ipRemovalGracePeriod - 2*time.Second,
		},
		{
			name: "TTL not expired of any IP address",
			status: &ocpnetworkv1alpha1.DNSNameResolverStatus{
				ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.1",
								TTLSeconds:     10,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:             "1.1.1.2",
								TTLSeconds:     4,
								LastLookupTime: &v1.Time{Time: time.Now()},
							},
						},
					},
				},
			},
			expectedTTLExpired:    false,
			expectedRemainingTime: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualTTLExpired, actualRemainingTime := reconcileRequired(tc.status)
			assert.Equal(t, tc.expectedTTLExpired, actualTTLExpired)
			if actualTTLExpired {
				cmOpts := []cmp.Option{
					cmpopts.EquateApproxTime(100 * time.Millisecond),
				}
				if !cmp.Equal(time.Now().Add(actualRemainingTime), time.Now().Add(tc.expectedRemainingTime), cmOpts...) {
					t.Fatalf("expected remaining time: %v, actual remaining time: %v", tc.expectedRemainingTime, actualRemainingTime)
				}
			}
		})
	}
}

func Test_Reconcile(t *testing.T) {
	dnsServerIP := "127.0.0.1"
	dnsServerPort := "8053"
	testDNSName := "www.example.com."

	// Start a test dns server.
	testServer := &dns.Server{Addr: net.JoinHostPort(dnsServerIP, dnsServerPort), Net: "udp"}
	go testServer.ListenAndServe()

	// Create the handler function for current test case.
	handleRequest := func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m)
		if r.Question[0].Name != testDNSName {
			t.Fatalf("Unexpected DNS name lookup request. Expected: %s, Actual, %s", testDNSName, r.Question[0].Name)
		}
	}
	// Register the handler function.
	dns.HandleFunc(".", handleRequest)

	// Create endpoint slice list containing the dnsServerIP.
	epSliceList := &discoveryv1.EndpointSliceList{
		Items: []discoveryv1.EndpointSlice{
			{
				ObjectMeta: v1.ObjectMeta{
					Name: "dns-default",
				},
				Endpoints: []discoveryv1.Endpoint{
					{
						Addresses: []string{dnsServerIP},
					},
				},
			},
		},
	}

	// Add the  endpoint slice list and the scheme for the DNSNameResolver
	// resource to the fake client and use the fake client to initialize
	// the fake cache.
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ocpnetworkv1alpha1.Install(scheme))
	fakeClient := fake.NewClientBuilder().WithLists(epSliceList).WithScheme(scheme).Build()
	cache := &fakeCache{
		FakeInformers: &informertest.FakeInformers{},
		Reader:        fakeClient,
	}

	// Initialize the resolver using the fake cache.
	resolver := NewResolver(cache, "")
	// Use the fake cache, fake client and the resolver to initialize the
	// reconciler.
	rec := reconciler{
		dnsNameResolverCache: cache,
		client:               fakeClient,
		resolver:             resolver,
	}

	// Create a DNSNameResolver object without any status.
	dnsNameResolver := &ocpnetworkv1alpha1.DNSNameResolver{
		ObjectMeta: v1.ObjectMeta{
			Name:      "regular",
			Namespace: "namespace1",
		},
		Spec: ocpnetworkv1alpha1.DNSNameResolverSpec{
			Name: ocpnetworkv1alpha1.DNSName(testDNSName),
		},
	}
	err := fakeClient.Create(context.Background(), dnsNameResolver, &client.CreateOptions{})
	if err != nil {
		t.Fatalf("Unexpected error while creating DNSNameResolver object: %v", err)
	}

	// Recocile the create event of the DNSNameResolver object.
	_, err = rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      dnsNameResolver.Name,
		Namespace: dnsNameResolver.Namespace,
	}})
	if err != nil {
		t.Fatalf("Unexpected error while reconciling the DNSNameResolver object create event: %v", err)
	}

	// As the DNSNameResolver object did not have any status, the nextLookupTime should match the
	// current time + defaultMaxTTL. Also, numIPs should be 0.
	var (
		dnsName        string
		nextLookupTime time.Time
		numIPs         int
		exists         bool
	)
	err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Second, true, func(ctx context.Context) (done bool, err error) {
		dnsName, nextLookupTime, numIPs, exists = resolver.getNextDNSNameDetails()
		if !exists {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatal("No next DNS name to lookup found")
	}
	if dnsName != testDNSName {
		t.Fatalf("Unexpected next DNS name to lookup. Expected: %s, Actual: %s", dnsName, testDNSName)
	}
	if numIPs != 0 {
		t.Fatalf("Expected num IPs to 0, but got %d", numIPs)
	}
	cmpOpts := cmpopts.EquateApproxTime(1 * time.Second)
	diff := cmp.Diff(time.Now().Add(defaultMaxTTL), nextLookupTime, cmpOpts)
	if diff != "" {
		t.Fatalf("unexpected next lookuptime (-want +got): %s\n", diff)
	}

	// Update the DNSNameResolver object to the details of the corresponding DNS name's
	// resolved name to the status.
	var ttlSeconds int32 = 600
	addresses := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	resolvedAddresses := []ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{}
	for _, address := range addresses {
		resolvedAddresses = append(resolvedAddresses, ocpnetworkv1alpha1.DNSNameResolverResolvedAddress{
			IP:             address,
			TTLSeconds:     ttlSeconds,
			LastLookupTime: &v1.Time{Time: time.Now()},
		})
	}
	dnsNameResolver.Status = ocpnetworkv1alpha1.DNSNameResolverStatus{
		ResolvedNames: []ocpnetworkv1alpha1.DNSNameResolverResolvedName{
			{
				DNSName:           ocpnetworkv1alpha1.DNSName(testDNSName),
				ResolvedAddresses: resolvedAddresses,
			},
		},
	}
	err = fakeClient.Update(context.Background(), dnsNameResolver, &client.UpdateOptions{})
	if err != nil {
		t.Fatalf("Unexpected error while creating DNSNameResolver object: %v", err)
	}

	// Recocile the update event of the DNSNameResolver object.
	_, err = rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      dnsNameResolver.Name,
		Namespace: dnsNameResolver.Namespace,
	}})
	if err != nil {
		t.Fatalf("Unexpected error while reconciling the DNSNameResolver object update event: %v", err)
	}

	// As the DNSNameResolver object's status is updated with the details of the corresponding DNS name's
	// resolved name, the nextLookupTime should match the current time + TTL. Also, numIPs should be 1.
	err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Second, true, func(ctx context.Context) (done bool, err error) {
		dnsName, nextLookupTime, numIPs, exists = resolver.getNextDNSNameDetails()
		if !exists {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatal("No next DNS name to lookup found")
	}
	if dnsName != testDNSName {
		t.Fatalf("Unexpected next DNS name to lookup. Expected: %s, Actual: %s", dnsName, testDNSName)
	}
	if numIPs != len(addresses) {
		t.Fatalf("Expected num IPs to 1, but got %d", numIPs)
	}
	diff = cmp.Diff(time.Now().Add(time.Duration(ttlSeconds)*time.Second), nextLookupTime, cmpOpts)
	if diff != "" {
		t.Fatalf("unexpected next lookuptime (-expected +actual): %s\n", diff)
	}

	// Delete a DNSNameResolver object.
	err = fakeClient.Delete(context.Background(), dnsNameResolver, &client.DeleteOptions{})
	if err != nil {
		t.Fatalf("Unexpected error while deleting the DNSNameResolver object: %v", err)
	}

	// Recocile the delete event of the DNSNameResolver object.
	_, err = rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      dnsNameResolver.Name,
		Namespace: dnsNameResolver.Namespace,
	}})
	if err != nil {
		t.Fatalf("Unexpected error while getting random CoreDNS pod IPs: %v", err)
	}

	// As the DNSNameResolver object is deleted the next dns name should not exist.
	err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Second, true, func(ctx context.Context) (done bool, err error) {
		dnsName, _, _, exists = resolver.getNextDNSNameDetails()
		if exists {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Unexpected next DNS name found: %s", dnsName)
	}
}
