#!/bin/bash

# The script can be called with one argument. If the value of the
# argument is "upstream", then upstream coredns/coredns repo will
# used to build the executable. Otherwise, if the value is not
# passed or the value is not equal to "upstream", then downstream
# openshift/coredns repo will be used.

set -euo pipefail

PLUGIN_PATH=$(readlink -f "$(dirname "$0")/..")

# Create a temporary directory for cloning coredns repo.
# The directory will be deleted after the execution of script.
BASE_PATH=$(mktemp -d)
trap 'chmod -R u+w "${BASE_PATH}"; rm -rf "${BASE_PATH}"' EXIT

cd "${BASE_PATH}"
COREDNS_URL="https://github.com/openshift/coredns"
if  [ ! -z "${1-}" ] && [ "${1}" = "upstream" ]
then
    COREDNS_URL="https://github.com/coredns/coredns"
fi
echo "Cloning from ${COREDNS_URL}"
git clone "${COREDNS_URL}"

# Add the "ocp_dnsnameresolver" plugin to the cloned coredns repo.
"${PLUGIN_PATH}"/hack/add-plugin.sh "${BASE_PATH}"/coredns "${PLUGIN_PATH}"

cd "${BASE_PATH}"/coredns

# Build the coredns executable.
go build -o coredns .

# Copy it to the local directory.
cp coredns "${PLUGIN_PATH}"
