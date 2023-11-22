#!/bin/bash

set -euxo pipefail

COREDNS_PATH=$1
PLUGIN_PATH=$(readlink -f "$(dirname "$0")/..")

# Add/update the plugin details in plugin.cfg file.
if grep -wq "ocp_dnsnameresolver" $COREDNS_PATH/plugin.cfg
then
    sed -i "/ocp_dnsnameresolver/c\ocp_dnsnameresolver:github.com/openshift/coredns-ocp-dnsnameresolver" $COREDNS_PATH/plugin.cfg
else
    sed -i "/cache:cache/a ocp_dnsnameresolver:github.com/openshift/coredns-ocp-dnsnameresolver" $COREDNS_PATH/plugin.cfg
fi

CURRENT_DIR=$(pwd)

cd $COREDNS_PATH

# Replace "github.com/openshift/coredns-ocp-dnsnameresolver" in the go.mod file to use the local code.
go mod edit -replace github.com/openshift/coredns-ocp-dnsnameresolver=$PLUGIN_PATH

# Run go commands to fetch the code required for the plugin.
go get github.com/openshift/coredns-ocp-dnsnameresolver
go mod tidy
go mod vendor
go mod verify
go generate
go get
go mod tidy
go mod vendor
go mod verify

cd $CURRENT_DIR
