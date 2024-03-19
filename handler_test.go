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

// fakeNextPluginHandler is a fake implementation which returns DNS records of the given recordType
// with the qname, ttl and ips.
func fakeNextPluginHandler(tc test.Case) plugin.Handler {
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
	name                string
	dnsNameResolvers    []ocpnetworkapiv1alpha1.DNSNameResolver
	timeSinceLastLookup [][][]time.Duration
	dnsQueryTestCases   []test.Case
	expectedStatuses    []ocpnetworkapiv1alpha1.DNSNameResolverStatus
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(20), time.Duration(20),
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
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
		name: "Reset resolutionsFailures to zero for a resolved name in regular dns name resolver object status",
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
				},
			},
		},
		dnsQueryTestCases: []test.Case{
			{
				Qname: "www.example.com.",
				Qtype: dns.TypeA,
				Rcode: dns.RcodeSuccess,
				Answer: []dns.RR{
					test.A("www.example.com. 20 IN A 1.1.1.1"),
					test.A("www.example.com. 20 IN A 1.1.1.2"),
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
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
									TTLSeconds: 30,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 30,
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(25), time.Duration(25),
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
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: nil,
			},
		},
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
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
		name: "Increment resolutionFailure field in regular dns name resolver object's status and " +
			"update ttlSeconds to minTTL as the TTLs of the IP addresses are about to expire",
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
									TTLSeconds: 12,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 12,
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
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
					// If the resolved name entry is not getting removed, then the IP addresses whose TTLs
					// have expired or about to expire should be set to the minimum TTL value.
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
		name: "Remove resolved dns name from regular dns name resolver object's status because the ResolutionFailures" +
			" is greater than or equal to the failure threshold and the TTLs of the IP addresses have expired",
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(30), time.Duration(30),
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
				ResolvedNames: []ocpnetworkapiv1alpha1.DNSNameResolverResolvedName{},
			},
		},
	},
	{
		name: "Don't update status if the DNS record type is CNAME and not A/AAAA",
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
				Qtype: dns.TypeCNAME,
				Rcode: dns.RcodeSuccess,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: nil,
			},
		},
	},
	{
		name: "Don't update status if the DNS record type is NS and not A/AAAA",
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
				Qtype: dns.TypeNS,
				Rcode: dns.RcodeSuccess,
			},
		},
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: nil,
			},
		},
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
		name: "Don't update the ttl of an IP for a regular dns name in the wildcard dns name" +
			" resolver object's status if next lookup time hasn't changed",
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(10), time.Duration(10),
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(20), time.Duration(20),
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
		name: "Reset resolutionsFailures to zero for a wildcard resolved name in wildcard dns name resolver object status",
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
									TTLSeconds: 50,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 50,
								},
							},
							ResolutionFailures: 2,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(20), time.Duration(20),
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
		name: "Reset resolutionsFailures to zero for a regular resolved name in wildcard dns name resolver object status",
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
									TTLSeconds: 50,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 50,
								},
							},
							ResolutionFailures: 2,
							Conditions: []metav1.Condition{
								{
									Type:    ConditionDegraded,
									Status:  metav1.ConditionTrue,
									Reason:  dns.RcodeToString[dns.RcodeSuccess],
									Message: rcodeMessage[dns.RcodeSuccess],
								},
							},
						},
					},
				},
			},
		},
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(20), time.Duration(20),
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
									TTLSeconds: 35,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 35,
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
									TTLSeconds: 35,
								},
								{
									IP:         "1.1.1.2",
									TTLSeconds: 35,
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(5), time.Duration(5),
				},
				{
					time.Duration(5), time.Duration(5),
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
		expectedStatuses: []ocpnetworkapiv1alpha1.DNSNameResolverStatus{
			{
				ResolvedNames: nil,
			},
		},
	},
	{
		name: "Remove wildcard dns name from wildcard dns name resolver object status because the ResolutionFailures" +
			" is greater than or equal to the failure threshold and the TTLs of the IP addresses have expired",
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(30),
				},
				{
					time.Duration(10),
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
		timeSinceLastLookup: [][][]time.Duration{
			{
				{
					time.Duration(5),
				},
				{
					time.Duration(5),
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
		name: "Update regular dns name resolver object status with regular dns name and wildcard dns name" +
			" resolver object status with regular dns name, then remove regular dns name and add wildcard dns name",
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
	// Same regular/wildcard DNS name in multiple namespaces
	{
		name: "Same regular DNS name in multiple namespaces",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "resolver",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "www.example.com.",
				},
			},
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
		name: "Same wildcard DNS name in multiple namespaces",
		dnsNameResolvers: []ocpnetworkapiv1alpha1.DNSNameResolver{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
					Namespace: "resolver",
				},
				Spec: ocpnetworkapiv1alpha1.DNSNameResolverSpec{
					Name: "*.example.com.",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular",
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

	// Make sure DNS Name Resolver factory is started.s
	go resolver.dnsNameResolverInformer.Run(ctx.Done())

	// This is not required in tests, but it serves as a proof-of-concept by
	// ensuring that the informer goroutine have warmed up and called List before
	// we send any events to it.
	cache.WaitForCacheSync(ctx.Done(), resolver.dnsNameResolverInformer.HasSynced)

	for _, dnstc := range dnsTestCases {
		t.Run(dnstc.name, func(t *testing.T) {
			lister := ocpnetworklisterv1alpha1.NewDNSNameResolverLister(resolver.dnsNameResolverInformer.GetIndexer())

			// Iterate through the DNSNameResolver objects.
			for objIndex, dnsNameResolver := range dnstc.dnsNameResolvers {

				// If any resolved address exists in the status then update the LastLookup field by subtracting the
				// time since last lookup from the current time. This is done to ensure that the logic checking the
				// next lookup time does not encounter any error and calculates it correctly.
				for resolvedNameIndex, resolvedName := range dnsNameResolver.Status.ResolvedNames {
					for resolvedAddressIndex := range resolvedName.ResolvedAddresses {
						resolvedName.ResolvedAddresses[resolvedAddressIndex].LastLookupTime = &metav1.Time{
							Time: time.Now().Add(-dnstc.timeSinceLastLookup[objIndex][resolvedNameIndex][resolvedAddressIndex] * time.Second),
						}
					}
					dnsNameResolver.Status.ResolvedNames[resolvedNameIndex] = resolvedName
				}

				// Create the DNSNameResolver object.
				_, err := fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers(dnsNameResolver.Namespace).Create(context.TODO(),
					&dnsNameResolver, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("error injecting dns name resolver: %v", err)
				}

				// Wait for the informer to get the create event.
				err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					_, err = lister.DNSNameResolvers(dnsNameResolver.Namespace).Get(dnsNameResolver.Name)
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
				resolver.Next = fakeNextPluginHandler(testCase)
				resolver.ServeDNS(context.TODO(), w, testCase.Msg())

				// Wait for the informer to sync the updates to the DNSNameResolver resources during
				// the execution of the ServeDNS function.
				time.Sleep(100 * time.Millisecond)
			}

			// Iterate through the DNSNameResolver objects.
			for index, dnsNameResolver := range dnstc.dnsNameResolvers {

				// Get the current DNSNameResolver object.
				resolverObj, err := lister.DNSNameResolvers(dnsNameResolver.Namespace).Get(dnsNameResolver.Name)
				if err != nil {
					t.Fatalf("error retrieving dns name resolver add: %v", err)
				}

				// Compare the expected status with the actual status of the DNSNameResolver object.
				cmpOpts := []cmp.Option{
					cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime"),
					cmpopts.IgnoreFields(ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress{}, "LastLookupTime"),
					cmpopts.SortSlices(func(elem1, elem2 ocpnetworkapiv1alpha1.DNSNameResolverResolvedAddress) bool {
						return elem1.IP > elem2.IP
					}),
				}
				// If the object's status did not match the expected status then fail the test.
				if diff := cmp.Diff(dnstc.expectedStatuses[index], resolverObj.Status, cmpOpts...); diff != "" {
					t.Fatalf("dns name resolver object's status did not match the expected status: DNS name: %s, object name: %s, namespace: %s\nDiff: %s",
						resolverObj.Spec.Name, resolverObj.Name, resolverObj.Namespace, diff)
				}

				// Delete the DNSNameResolver object.
				err = fakeNetworkClient.NetworkV1alpha1().DNSNameResolvers(dnsNameResolver.Namespace).Delete(context.TODO(),
					dnsNameResolver.Name, metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("error deleting dns name resolver: %v", err)
				}

				// Wait for the informer to get the delete event.
				err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					_, err = lister.DNSNameResolvers(dnsNameResolver.Namespace).Get(dnsNameResolver.Name)
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
