#!/usr/bin/env bash
# ISO 에 들어간 패키지의 대응 소스를 받아 out/sources/ 에 묶는다. 호스트에는 아무것도 설치하지 않는다.
#
#   scripts/collect-sources-in-docker.sh [package-manifest.tsv] [out-dir]
#
# 입력은 make iso 가 만든 out/package-manifest.tsv 다. 결과는 out/sources/ (opereta-<버전>-sources-NN.tar, .tsv, README).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"
MANIFEST="${1:-${ROOT}/out/package-manifest.tsv}"
OUT="${2:-${ROOT}/out/sources}"

die() { echo "collect-sources-in-docker: $*" >&2; exit 2; }
command -v docker >/dev/null 2>&1 || die "docker is required"
[[ -s "${MANIFEST}" ]] || die "package manifest not found: ${MANIFEST} (run make iso first)"
mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"
MANIFEST="$(cd "$(dirname "${MANIFEST}")" && pwd)/$(basename "${MANIFEST}")"
rm -f "${OUT}"/opereta-*-sources* "${OUT}/SOURCES-README.txt"

docker run --rm --init -v "${ROOT}/scripts:/scripts:ro" -v "${MANIFEST}:/manifest.tsv:ro" -v "${OUT}:/out" \
    -e DEBIAN_SUITE="${DEBIAN_SUITE}" -e DEBIAN_AREAS="${DEBIAN_AREAS}" -e DEBIAN_MIRROR="${DEBIAN_MIRROR}" \
    -e DEBIAN_SECURITY_MIRROR="${DEBIAN_SECURITY_MIRROR}" -e DEBIAN_SNAPSHOT="${DEBIAN_SNAPSHOT:-}" \
    -e DEBIAN_SNAPSHOT_URL="${DEBIAN_SNAPSHOT_URL}" \
    "debian:${DEBIAN_SUITE}" bash -euc '
bash /scripts/collect-sources-run.sh --manifest /manifest.tsv --out /out --prefix "opereta-'"${OPERETA_VERSION}"'-sources"
chown -R "$(stat -c %u:%g /out)" /out
'
echo "collect-sources-in-docker: ${OUT}"
ls -l "${OUT}"
