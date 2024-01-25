#!/bin/bash

set -euo pipefail

PLUGIN_PATH=$(readlink -f "$(dirname "$0")/..")

# Create a temporary directory for cloning openshift/coredns repo.
# The directory will be deleted after the execution of script.
BASE_PATH=$(mktemp -d)
trap 'chmod -R u+w "${BASE_PATH}"; rm -rf "${BASE_PATH}"' EXIT

cd "${BASE_PATH}"
# Clone openshift/coredns repo if not already cloned.
if [ ! -d coredns ]
then
    git clone https://github.com/openshift/coredns
fi

# Add the "ocp_dnsnameresolver" plugin to the cloned openshift/coredns repo.
"${PLUGIN_PATH}"/hack/add-plugin.sh "${BASE_PATH}"/coredns "${PLUGIN_PATH}"

cd "${BASE_PATH}"/coredns

# Build the coredns executable.
GOFLAGS=-mod=vendor go build -o coredns .

# Copy it to the local directory.
cp coredns "${PLUGIN_PATH}"
