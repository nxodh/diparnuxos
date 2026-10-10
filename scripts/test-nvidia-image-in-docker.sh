#!/usr/bin/env bash
# Debian 컨테이너에서 완성된 ISO 의 NVIDIA 구성 검사를 실행한다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
ISO="${1:-$ROOT/out/lightlix-${LIGHTLIX_VERSION}-amd64.iso}"
[[ -f "$ISO" ]] || { echo "ERROR: ISO 가 없다: $ISO" >&2; exit 2; }
iso_dir="$(cd "$(dirname "$ISO")" && pwd)"
docker run --rm -v "$ROOT:/src:ro" -v "$iso_dir:/iso:ro" \
    "debian:$DEBIAN_SUITE" bash -euc '
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq
        apt-get install -y -qq --no-install-recommends xorriso squashfs-tools >/dev/null
        exec bash /src/tests/nvidia/image.sh "/iso/$1"
    ' _ "$(basename "$ISO")"
