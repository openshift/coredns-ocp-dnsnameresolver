package ocp_dnsnameresolver

// configuredNamespace returns true when the namespace is exposed through the plugin
// `namespaces` configuration. However, if the `namespaces` configuration is omitted
// then all namespaces are exposed.
func (resolver *OCPDNSNameResolver) configuredNamespace(namespace string) bool {
	_, ok := resolver.namespaces[namespace]
	if len(resolver.namespaces) > 0 && !ok {
		return false
	}
	return true
}
