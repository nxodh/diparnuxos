#!/usr/bin/env bash
# packages/*/debian 이 있는 패키지를 dpkg-buildpackage 로 빌드하고, 결과 .deb 를 build/config/packages.chroot/ 에 넣는다.
# 패키지는 임시 디렉터리에 복사해서 빌드하므로 저장소 안에 .buildinfo, .changes, debian 부산물이 남지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LIGHTLIX_CONF="${LIGHTLIX_CONF:-${ROOT}/lightlix.conf}"
export LIGHTLIX_CONF
DEST="${ROOT}/build/config/packages.chroot"

die() {
    echo "build-debs: $*" >&2
    exit 1
}

[ -r "${LIGHTLIX_CONF}" ] || die "lightlix.conf 를 읽을 수 없다: ${LIGHTLIX_CONF}"
# shellcheck source=/dev/null
. "${LIGHTLIX_CONF}"
[ -n "${LIGHTLIX_VERSION:-}" ] || die "lightlix.conf 에 LIGHTLIX_VERSION 이 없다."

for cmd in dpkg-buildpackage dpkg-parsechangelog; do
    command -v "${cmd}" >/dev/null 2>&1 || die "${cmd} 가 없다 (dpkg-dev 패키지를 설치하라)."
done

mkdir -p "${DEST}"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

built=0
for pkgdir in "${ROOT}"/packages/*/; do
    pkgdir="${pkgdir%/}"
    [ -d "${pkgdir}/debian" ] || continue

    name="$(basename "${pkgdir}")"
    version="$(dpkg-parsechangelog -l "${pkgdir}/debian/changelog" --show-field Version)"
    if [ "${version}" != "${LIGHTLIX_VERSION}" ]; then
        die "${name}: debian/changelog 버전(${version})이 LIGHTLIX_VERSION(${LIGHTLIX_VERSION})과 다르다."
    fi

    src="${WORK}/src-${name}"
    cp -a "${pkgdir}" "${src}"
    (cd "${src}" && dpkg-buildpackage -us -uc -b)

    shopt -s nullglob
    debs=("${WORK}"/*.deb)
    shopt -u nullglob
    [ "${#debs[@]}" -gt 0 ] || die "${name}: .deb 가 만들어지지 않았다."

    for deb in "${debs[@]}"; do
        base="$(basename "${deb}")"
        # 같은 패키지의 이전 버전이 남아 있으면 지운다.
        rm -f "${DEST}/${base%%_*}"_*.deb
        mv -f "${deb}" "${DEST}/${base}"
        echo "build-debs: ${DEST}/${base}"
    done
    built=$((built + 1))
done

[ "${built}" -gt 0 ] || die "빌드할 패키지가 없다 (packages/*/debian 을 찾지 못했다)."
echo "build-debs: 패키지 ${built} 개 빌드 완료"
