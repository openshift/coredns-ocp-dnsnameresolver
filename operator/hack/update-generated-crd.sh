#!/bin/bash
set -euo pipefail

function install_crd {
  if [[ -z "${SKIP_COPY+1}" ]]; then
    local SRC="$1"
    local DST="$2"
    if ! cmp -s "$DST" "$SRC"; then
      cp -f "$SRC" "$DST"
    fi
  fi
}

# Can't rely on associative arrays for old Bash versions (e.g. OSX)
install_crd \
  "vendor/github.com/openshift/api/network/v1alpha1/0000_70_dnsnameresolver_00-techpreview.crd.yaml" \
  "pkg/manifests/assets/0000_70_dnsnameresolver_00-techpreview.crd.yaml"

install_crd \
  "vendor/github.com/openshift/api/network/v1alpha1/0000_70_dnsnameresolver_00-techpreview.crd.yaml" \
  "config/crd/bases/network.openshift.io.openshift.io_dnsnameresolvers.yaml"