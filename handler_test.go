package ocp_dnsnameresolver

import (
	"context"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/miekg/dns"
	networkv1alpha1 "github.com/openshift/api/network/v1alpha1"
	networkclient "github.com/openshift/client-go/network/clientset/versioned"
	networkfake "github.com/openshift/client-go/network/clientset/versioned/fake"
	networklisterv1alpha1 "github.com/openshift/client-go/network/listers/network/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

// nextPluginHandler is a fake implementation which returns DNS records of the given recordType
// with the qname, ttl and ips.
func nextPluginHandler(tc test.Case) plugin.Handler {
	return plugin.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetQuestion(tc.Qname, tc.Qtype)
		m.Response = true
		m.Answer = append(m.Answer, tc.Answer...)
		w.WriteMsg(m)
		return tc.Rcode, nil
	})
}

type query struct {
	test.Case
	numObjectUpdated int
}

type dnsTestCase struct {
	name             string
	dnsNameResolvers []networkv1alpha1.DNSNameResolver
	queries          []query
	expectedStatuses []networkv1alpha1.DNSNameResolverStatus
}

var dnsTestCases []dnsTestCase = []dnsTestCase{
	// Regular DNS Name successful resolution
	{
		name: "Update regular dns name resolver object status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update regular dns name resolver object status with latest ttl of the corresponding IPs",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     20,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     20,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Don't update the ttl of an IP in the regular dns name resolver object's status if next lookup time hasn't changed",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 40,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 40,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update regular dns name resolver object status and append the new IP",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     30,
									LastLookupTime: &metav1.Time{Time: time.Now()},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     30,
									LastLookupTime: &metav1.Time{Time: time.Now()},
								},
							},
							ResolutionFailures: 1,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.3"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.3",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update regular dns name resolver object status with latest ttl of the corresponding IPs, set resolutionFailures to 0 and Degraded condition to false",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     5,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-5 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     5,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-5 * time.Second)},
								},
							},
							ResolutionFailures: 2,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeServerFailure],
									Message: rcodeMessage[dns.RcodeServerFailure],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	// Regular DNS Name resolution failure
	{
		name: "Don't update regular dns name resolver object status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{{}},
	},
	{
		name: "Increment resolutionFailure field in regular dns name resolver object's status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     30,
									LastLookupTime: &metav1.Time{Time: time.Now()},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     30,
									LastLookupTime: &metav1.Time{Time: time.Now()},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 1,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionTrue,
								Reason:  dns.RcodeToString[dns.RcodeNameError],
								Message: rcodeMessage[dns.RcodeNameError],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Increment resolutionFailure field in regular dns name resolver object's status and update ttlSeconds and lastLookupTime fields",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     32,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-30 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     32,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-30 * time.Second)},
								},
							},
							ResolutionFailures: 1,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeNameError],
									Message: rcodeMessage[dns.RcodeNameError],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{

						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 5,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 5,
							},
						},
						ResolutionFailures: 2,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionTrue,
								Reason:  dns.RcodeToString[dns.RcodeNameError],
								Message: rcodeMessage[dns.RcodeNameError],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Remove resolved dns name from regular dns name resolver object's status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     5,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-5 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     5,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-5 * time.Second)},
								},
							},
							ResolutionFailures: 5,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeNameError],
									Message: rcodeMessage[dns.RcodeNameError],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{{}},
	},
	// Wildcard DNS Name successful resolution
	{
		name: "Update wildcard dns name resolver object status with regular dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Don't update the ttl of an IP for a regular dns name in the wildcard dns name resolver object's status if next lookup time hasn't changed",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 40,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 40,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update wildcard dns name resolver object status with wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Don't update the ttl of an IP for the wildcard dns name if next lookup time hasn't changed",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     50,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     50,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 50,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 50,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update wildcard dns name resolver object status with only wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update wildcard dns name resolver object status with regular dns name, then remove regular dns name and add wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update wildcard dns name resolver object status with wildcard dns name, then don't add regular dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Remove regular dns names from wildcard dns name resolver object status and add the wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
						{
							DNSName: "sub.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
								{
									IP:             "1.1.1.2",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update wildcard dns name resolver object status with different IPs for regular and wildcard dns names",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 2.1.1.1"),
						test.A("www.example.com. 30 IN A 2.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "2.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "2.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	// Wildcard DNS Name resolution failure
	{
		name: "Don't update wildcard dns name resolver object status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 0,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{{}},
	},
	{
		name: "Remove wildcard dns name from wildcard dns name resolver object status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     20,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
							},
							ResolutionFailures: 5,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeNameError],
									Message: rcodeMessage[dns.RcodeNameError],
								},
							},
						},
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.2",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.2",
								TTLSeconds: 40,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Increment resolutionFailures of regular dns name in wildcard dns name resolver object status",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: networkv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.1",
									TTLSeconds:     40,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:             "1.1.1.2",
									TTLSeconds:     50,
									LastLookupTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
								},
							},
							ResolutionFailures: 0,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionFalse,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeNameError,
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 40,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.2",
								TTLSeconds: 50,
							},
						},
						ResolutionFailures: 1,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionTrue,
								Reason:  dns.RcodeToString[dns.RcodeNameError],
								Message: rcodeMessage[dns.RcodeNameError],
							},
						},
					},
				},
			},
		},
	},
	// Regular and Wildcard DNS Name
	{
		name: "Update both regular and wildcard dns name resolver object statuses with regular dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 2,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update only wildcard dns name resolver object status with wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{},
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update regular dns name resolver object status with regular dns name and wildcard dns name resolver object status with wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
	{
		name: "Update regular dns name resolver object status with regular dns name and wildcard dns name resolver object status with regular dns name, then remove regular dns name and add wildcard dns name",
		dnsNameResolvers: []networkv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: networkv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		queries: []query{
			{
				Case: test.Case{
					Qname: "www.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("www.example.com. 30 IN A 1.1.1.1"),
						test.A("www.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 2,
			},
			{
				Case: test.Case{
					Qname: "*.example.com.",
					Qtype: dns.TypeA,
					Rcode: dns.RcodeSuccess,
					Answer: []dns.RR{
						test.A("*.example.com. 30 IN A 1.1.1.1"),
						test.A("*.example.com. 30 IN A 1.1.1.2"),
					},
				},
				numObjectUpdated: 1,
			},
		},
		expectedStatuses: []networkv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
			{
				ResolvedNames: []networkv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []networkv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 30,
							},
							{
								IP:         "1.1.1.2",
								TTLSeconds: 30,
							},
						},
						ResolutionFailures: 0,
						Conditions: []metav1.Condition{
							{
								Type:    ConditionDegraded,
								Status:  metav1.ConditionFalse,
								Reason:  dns.RcodeToString[dns.RcodeSuccess],
								Message: rcodeMessage[dns.RcodeSuccess],
							},
						},
					},
				},
			},
		},
	},
}

func TestServeDNS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := New()

	// Create channel to know when the watch has started.
	watcherStarted := make(chan struct{})
	// Create the fake client.
	fakeNetworkClient := networkfake.NewSimpleClientset()
	// A watch reactor for dns name resolver objects that allows the injection of the watcherStarted channel.
	fakeNetworkClient.PrependWatchReactor("dnsnameresolvers", func(action clienttesting.Action) (handled bool, ret watch.Interface, err error) {
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := fakeNetworkClient.Tracker().Watch(gvr, ns)
		if err != nil {
			return false, nil, err
		}
		close(watcherStarted)
		return true, watch, nil
	})

	// createFakeClient returns the fakeNetworkClient.
	createFakeClient := func() (networkclient.Interface, error) {
		return fakeNetworkClient, nil
	}
	// Create a channel to receive the dns name resolver objects from the informer.
	resolverNames := make(chan *networkv1alpha1.DNSNameResolver, 1)
	// sendToChannel sends the received dns name resolver objects to the resolverNames channel.
	sendToChannel := func(resolverObj *networkv1alpha1.DNSNameResolver) {
		resolverNames <- resolverObj
	}
	// Initialize the informer with a fake client and receive dns name resolver objects
	// in the resolverNames channel.
	resolver.initInformer(createFakeClient, sendToChannel)

	// Make sure informer is running.
	go resolver.dnsNameResolverInformer.Run(ctx.Done())

	// This is not required in tests, but it serves as a proof-of-concept by
	// ensuring that the informer goroutine have warmed up and called List before
	// we send any events to it.
	cache.WaitForCacheSync(ctx.Done(), resolver.dnsNameResolverInformer.HasSynced)

	// The fake client doesn't support resource version. Any writes to the client
	// after the informer's initial LIST and before the informer establishing the
	// watcher will be missed by the informer. Therefore we wait until the watcher
	// starts.
	<-watcherStarted

	for _, dnstc := range dnsTestCases {
		t.Run(dnstc.name, func(t *testing.T) {

			// Iterate through the DNSNameResolver objects.
			for _, dnsNameResolver := range dnstc.dnsNameResolvers {
				// Create the DNSNameResolver object.
				_, err := fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers("dns").Create(context.TODO(), &dnsNameResolver, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("error injecting dns name resolver add: %v", err)
				}

				// Wait for it to be informer to get the event.
				select {
				case <-resolverNames:
				case <-time.After(wait.ForeverTestTimeout):
					t.Fatal("Informer did not get the added dns name resolver")
				}
			}

			w := dnstest.NewRecorder(&test.ResponseWriter{})

			// For each DNS query call the ServeDNS function of the plugin.
			for _, query := range dnstc.queries {
				resolver.Next = nextPluginHandler(query.Case)
				resolver.ServeDNS(context.TODO(), w, query.Msg())

				// Wait for it to be informer to get the events.
				for i := 0; i < query.numObjectUpdated; i++ {
					select {
					case <-resolverNames:
					case <-time.After(wait.ForeverTestTimeout):
						t.Fatal("Informer did not get the updated dns name resolver")
					}
				}
			}

			// Iterate through the DNSNameResolver objects.
			for index, dnsNameResolver := range dnstc.dnsNameResolvers {
				// Get the current DNSNameResolver object.
				resolverObj, err := networklisterv1alpha1.NewDNSNameResolverLister(resolver.dnsNameResolverInformer.GetIndexer()).DNSNameResolvers("dns").Get(dnsNameResolver.Name)
				if err != nil {
					t.Fatalf("error retrieving dns name resolver add: %v", err)
				}

				matched := false

				// Check if the number of resolved names in the objects's status and in the expected status is equal to zero.
				if len(dnstc.expectedStatuses[index].ResolvedNames) == 0 && len(resolverObj.Status.ResolvedNames) == 0 {
					matched = true
				}

				// Check if the number of resolved names in the status matches that of the expected status.
				if len(dnstc.expectedStatuses[index].ResolvedNames) == len(resolverObj.Status.ResolvedNames) {

					// Iterate through each resolved name in the object's status and check if it matches that of the expected status.
					for index, expectedResolvedName := range dnstc.expectedStatuses[index].ResolvedNames {
						matched = false
						currentResolvedName := resolverObj.Status.ResolvedNames[index]
						if currentResolvedName.DNSName == expectedResolvedName.DNSName {
							cmpOpts := []cmp.Option{
								cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime"),
								cmpopts.IgnoreFields(networkv1alpha1.DNSNameResolverResolvedAddress{}, "LastLookupTime"),
								cmpopts.SortSlices(func(elem1, elem2 networkv1alpha1.DNSNameResolverResolvedAddress) bool {
									return elem1.IP > elem2.IP
								}),
							}
							if cmp.Diff(currentResolvedName, expectedResolvedName, cmpOpts...) == "" {
								matched = true
							}
						}
						if !matched {
							break
						}
					}
				}

				// Delete the DNSNameResolver object.
				err = fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers("dns").Delete(context.TODO(), dnsNameResolver.Name, metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("error deleting dns name resolver add: %v", err)
				}

				// Wait for it to be informer to get the event.
				select {
				case <-resolverNames:
				case <-time.After(wait.ForeverTestTimeout):
					t.Fatal("Informer did not get the deleted dns name resolver")
				}

				// If the object's status did not match the expected status then fail the test.
				if !matched {
					t.Logf("Current Status: %v", resolverObj.Status)
					t.Logf("Expected Status: %v", dnstc.expectedStatuses)
					t.Fatalf("dns name resolver object's status did not match the expected status: DNS name: %s", resolverObj.Spec.Name)
				}
			}
		})
	}
}
