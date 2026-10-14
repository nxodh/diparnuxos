#!/usr/bin/env bash
# 컨테이너(debian:<suite>) 안에서 소스 수집기를 실행한다(scripts/collect-sources-in-docker.sh 와 tests/sources/test.sh 가 부른다).
#
#   collect-sources-run.sh --setup-only
#   collect-sources-run.sh --manifest FILE --out DIR --prefix NAME [수집기 인자...]
#
# 환경 변수: DEBIAN_SUITE, DEBIAN_AREAS, DEBIAN_MIRROR, DEBIAN_SECURITY_MIRROR, DEBIAN_SNAPSHOT, DEBIAN_SNAPSHOT_URL
# (opereta.conf 의 값. 이미지를 만들 때 쓴 저장소와 같은 곳의 소스 색인을 쓴다.)
set -euo pipefail

die() { echo "collect-sources-run: $*" >&2; exit 2; }
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

suite="${DEBIAN_SUITE:?}" areas="${DEBIAN_AREAS:?}"
mirror="${DEBIAN_MIRROR:?}" security="${DEBIAN_SECURITY_MIRROR:?}"
if [[ -n "${DEBIAN_SNAPSHOT:-}" ]]; then
    mirror="${DEBIAN_SNAPSHOT_URL:?}/debian/${DEBIAN_SNAPSHOT}"
    security="${DEBIAN_SNAPSHOT_URL}/debian-security/${DEBIAN_SNAPSHOT}"
    printf 'Acquire::Check-Valid-Until "false";\n' > /etc/apt/apt.conf.d/90snapshot
fi

export DEBIAN_FRONTEND=noninteractive
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat > /etc/apt/sources.list.d/opereta-src.sources <<EOF
Types: deb deb-src
URIs: ${mirror}
Suites: ${suite} ${suite}-updates
Components: ${areas}
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp

Types: deb deb-src
URIs: ${security}
Suites: ${suite}-security
Components: ${areas}
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
EOF
apt-get update -qq > /dev/null
apt-get install -y -qq --no-install-recommends python3 dpkg-dev ca-certificates > /dev/null

if [[ "${1:-}" == "--setup-only" ]]; then
    exit 0
fi
exec python3 "${here}/collect-sources.py" "$@"
