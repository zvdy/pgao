#!/usr/bin/env bash
# Tear down the observability kind cluster spun up by up.sh.
set -euo pipefail
CLUSTER="${CLUSTER:-pgao-obs}"
if kind get clusters | grep -qx "$CLUSTER"; then
  kind delete cluster --name "$CLUSTER"
else
  echo "kind cluster $CLUSTER not present"
fi
