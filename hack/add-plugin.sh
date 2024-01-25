#!/bin/bash

set -euo pipefail

COREDNS_PATH=$1
PLUGIN_PATH=$2

# Add/update the plugin details in plugin.cfg file.
if grep -wq "ocp_dnsnameresolver" "${COREDNS_PATH}"/plugin.cfg
then
    sed -i "/ocp_dnsnameresolver/c\ocp_dnsnameresolver:github.com/openshift/coredns-ocp-dnsnameresolver" "${COREDNS_PATH}"/plugin.cfg
else
# Add the plugin after cache in the plugin chain. The cache plugin will contain info about DNS names which
# have already been looked up and whose TTL haven't expired. The coredns-ocp-dnsnameresolver should intercept
# those DNS queries which are not cached and a fresh lookup is needed. Whenever the cache plugin isn't able
# to handle a DNS query, it means the upstream DNS server is needed to be invoked. This plugin will use the
# new information received from the upstream servers to update the corresponding DNSNameResolver CRs.
    sed -i "/cache:cache/a ocp_dnsnameresolver:github.com/openshift/coredns-ocp-dnsnameresolver" "${COREDNS_PATH}"/plugin.cfg
fi

cd "${COREDNS_PATH}"

# Replace "github.com/openshift/coredns-ocp-dnsnameresolver" in the go.mod file to use the local code.
go mod edit -replace github.com/openshift/coredns-ocp-dnsnameresolver="${PLUGIN_PATH}"

# Run go commands to fetch the code required for the plugin.
go get github.com/openshift/coredns-ocp-dnsnameresolver

# Generate the files related to the plugin.
GOFLAGS=-mod=mod go generate
# Run go commands to fetch the code required by the generated code.
go get

# Run go mod tidy/vendor/verify to update the dependecies.
go mod tidy
go mod vendor
go mod verify

