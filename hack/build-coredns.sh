#!/bin/bash

# The script can be called with one argument. If the value of the
# argument is "upstream", then upstream coredns/coredns repo will
# used to build the executable. Otherwise, if the value is not
# passed or the value is not equal to "upstream", then downstream
# openshift/coredns repo will be used.

set -euo pipefail

PLUGIN_PATH=$(readlink -f "$(dirname "$0")/..")

# Get current branch name. If the current branch name does not match
# the pattern "release-*", then use master branch.
BRANCH_TAG=$(git rev-parse --abbrev-ref HEAD)
if [[ "${BRANCH_TAG}" != release-* ]]
then
    BRANCH_TAG="master"
fi

# Create a temporary directory for cloning coredns repo.
# The directory will be deleted after the execution of script.
BASE_PATH=$(mktemp -d)
trap 'chmod -R u+w "${BASE_PATH}"; rm -rf "${BASE_PATH}"' EXIT

cd "${BASE_PATH}"
COREDNS_URL="https://github.com/openshift/coredns"
if  [ ! -z "${1-}" ] && [ "${1}" = "upstream" ]
then
    COREDNS_URL="https://github.com/coredns/coredns"
    BRANCH_TAG="v$(curl -s https://raw.githubusercontent.com/openshift/coredns/${BRANCH_TAG}/coremain/version.go | grep CoreVersion | grep -Po '\d+\.\d+\.\d+')"
fi
echo "Cloning from ${COREDNS_URL}"
git clone "${COREDNS_URL}"
cd "${BASE_PATH}"/coredns
echo "Checking out branch/tag ${BRANCH_TAG}"
git checkout ${BRANCH_TAG}

# Add the "ocp_dnsnameresolver" plugin to the cloned coredns repo.
"${PLUGIN_PATH}"/hack/add-plugin.sh "${BASE_PATH}"/coredns "${PLUGIN_PATH}"

cd "${BASE_PATH}"/coredns

# Build the coredns executable.
go build -o coredns .

# Copy it to the local directory.
cp coredns "${PLUGIN_PATH}"
