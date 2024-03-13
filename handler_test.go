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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/miekg/dns"
	ocpnetworkapiv1alpha1 "github.com/openshift/api/network/v1alpha1"
	ocpnetworkfakeclient "github.com/openshift/client-go/network/clientset/versioned/fake"
	ocpnetworklisterv1alpha1 "github.com/openshift/client-go/network/listers/network/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

type dnsTestCase struct {
	name              string
	dnsNameResolvers  []ocpnetworkapiv1alpha1.DNSNameResolver
	dnsQueryTestCases []test.Case
	expectedStatuses  []ocpnetworkapiv1alpha1.DNSNameResolverStatus
}

var dnsTestCases []dnsTestCase = []dnsTestCase{
	// Regular DNS Name successful resolution
	{
		name: "Update regular dns name resolver object status",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.1",
									TTLSeconds: 20,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 20,
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		name: "Update regular dns name resolver object status and append the new IP",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.3"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
									Reason:  dns.RcodeToString[dns.RcodeServerFailure],
									Message: rcodeMessage[dns.RcodeServerFailure],
								},
							},
						},
					},
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{{}},
	},
	{
		name: "Increment resolutionFailure field in regular dns name resolver object's status",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.1",
									TTLSeconds: 2,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 2,
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{

						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.1",
									TTLSeconds: 0,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 0,
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{{}},
	},
	// Wildcard DNS Name successful resolution
	{
		name: "Update wildcard dns name resolver object status with regular dns name",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		name: "Update wildcard dns name resolver object status with wildcard dns name",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		name: "Update wildcard dns name resolver object status with only wildcard dns name",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "www.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
							DNSName: "sub.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 2.1.1.1"),
					test.A("www.example.com. 30 IN A 2.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{{}},
	},
	{
		name: "Remove wildcard dns name from wildcard dns name resolver object status",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.1",
									TTLSeconds: 0,
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
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.2",
									TTLSeconds: 20,
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.2",
								TTLSeconds: 20,
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
				Status: ocpnetworkapiv1alpha1.DNSNameResolverStatus{
					ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
						{
							DNSName: "*.example.com.",
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
								{
									IP:         "1.1.1.1",
									TTLSeconds: 20,
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
							ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeNameError,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
							{
								IP:         "1.1.1.1",
								TTLSeconds: 20,
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
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
	// Regular and Wildcard DNS Name
	{
		name: "Update both regular and wildcard dns name resolver object statuses with regular dns name",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{},
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard",
					Namespace: "dns",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 30 IN A 1.1.1.1"),
					test.A("www.example.com. 30 IN A 1.1.1.2"),
				},
			},
			{
				Qname: "*.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("*.example.com. 30 IN A 1.1.1.1"),
					test.A("*.example.com. 30 IN A 1.1.1.2"),
				},
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "www.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{
					{
						DNSName: "*.example.com.",
						ResolvedAddresses: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{
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

	// Create the fake client.
	fakeNetworkClient := ocpnetworkfakeclient.NewSimpleClientset()
	// Initialize the informer with a fake client and receive dns name resolver objects
	// in the resolverNames channel.
	resolver.initInformer(fakeNetworkClient)

	// Make sure DNS Name Resolver factory is started.
	go resolver.dnsNameResolverInformer.Run(ctx.Done())

	// This is not required in tests, but it serves as a proof-of-concept by
	// ensuring that the informer goroutine have warmed up and called List before
	// we send any events to it.
	cache.WaitForCacheSync(ctx.Done(), resolver.dnsNameResolverInformer.HasSynced)

	for _, dnstc := range dnsTestCases {
		t.Run(dnstc.name, func(t *testing.T) {
			lister := ocpnetworklisterv1alpha1.NewDNSNameResolverLister(resolver.dnsNameResolverInformer.GetIndexer())

			// Iterate through the DNSNameResolver objects.
			for _, dnsNameResolver := range dnstc.dnsNameResolvers {

				// If any resolved address exists in the status then update the LastLookup field to current time.
				// This is done to ensure that the logic checking the next lookup time does not encounter any error
				// and calculates it correctly.
				for resolvedNameIndex, resolvedName := range dnsNameResolver.Status.ResolvedNames {
					for resolvedAddressIndex := range resolvedName.ResolvedAddresses {
						resolvedName.ResolvedAddresses[resolvedAddressIndex].LastLookupTime = &metav1.Time{
							Time: time.Now(),
						}
					}
					dnsNameResolver.Status.ResolvedNames[resolvedNameIndex] = resolvedName
				}

				// Create the DNSNameResolver object.
				_, err := fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers("dns").Create(context.TODO(), &dnsNameResolver, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("error injecting dns name resolver: %v", err)
				}

				// Wait for it to be informer to get the event.
				err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					_, err = lister.DNSNameResolvers("dns").Get(dnsNameResolver.Name)
					if err != nil {
						return false, nil
					}

					return true, nil
				})
				if err != nil {
					t.Fatalf("Informer did not get the added dns name resolver: %v", err)
				}
			}

			w := dnstest.NewRecorder(&test.ResponseWriter{})

			// For each DNS test.Case call the ServeDNS function of the plugin.
			for _, testCase := range dnstc.dnsQueryTestCases {
				resolver.Next = nextPluginHandler(testCase)
				resolver.ServeDNS(context.TODO(), w, testCase.Msg())

				time.Sleep(100 * time.Millisecond)
			}

			// Iterate through the DNSNameResolver objects.
			for index, dnsNameResolver := range dnstc.dnsNameResolvers {

				var resolverObj *ocpnetworkapiv1alpha1.DNSNameResolver
				// Wait for it to be informer to get the events.
				err := wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {

					// Get the current DNSNameResolver object.
					resolverObj, err = lister.DNSNameResolvers("dns").Get(dnsNameResolver.Name)
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
									cmpopts.IgnoreFields(ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{}, "LastLookupTime"),
									cmpopts.SortSlices(func(elem1, elem2 ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress) bool {
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
					if !matched {
						return false, nil
					}

					return true, nil
				})
				// If the object's status did not match the expected status then fail the test.
				if err != nil {
					t.Logf("Current Status: %v", resolverObj.Status)
					t.Logf("Expected Status: %v", dnstc.expectedStatuses[index])
					t.Fatalf("dns name resolver object's status did not match the expected status: DNS name: %s", resolverObj.Spec.Name)
				}

				// Delete the DNSNameResolver object.
				err = fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers("dns").Delete(context.TODO(), dnsNameResolver.Name, metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("error deleting dns name resolver: %v", err)
				}

				// Wait for it to be informer to get the event.
				err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					_, err = lister.DNSNameResolvers("dns").Get(dnsNameResolver.Name)
					if err == nil || !kerrors.IsNotFound(err) {
						return false, nil
					}

					return true, nil
				})
				if err != nil {
					t.Fatalf("error deleting dns name resolver add: %v", err)
				}

			}
		})
	}
}
