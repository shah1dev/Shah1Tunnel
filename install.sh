#!/usr/bin/env bash
set -Eeuo pipefail
# Local convenience wrapper; the canonical one-command installer is S1T-START.sh.
exec bash "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/S1T-START.sh"
