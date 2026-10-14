#!/usr/bin/env bash
# packages/*/debian 이 있는 패키지를 dpkg-buildpackage 로 빌드하고, 결과 .deb 를 build/config/packages.chroot/ 에 넣는다.
# 패키지는 임시 디렉터리에 복사해서 빌드하므로 저장소 안에 .buildinfo, .changes, debian 부산물이 남지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPERETA_CONF="${OPERETA_CONF:-${ROOT}/opereta.conf}"
export OPERETA_CONF
DEST="${ROOT}/build/config/packages.chroot"

die() {
    echo "build-debs: $*" >&2
    exit 1
}

[ -r "${OPERETA_CONF}" ] || die "cannot read opereta.conf: ${OPERETA_CONF}"
# shellcheck source=/dev/null
. "${OPERETA_CONF}"
[ -n "${OPERETA_VERSION:-}" ] || die "OPERETA_VERSION is missing in opereta.conf"

for cmd in dpkg-buildpackage dpkg-parsechangelog; do
    command -v "${cmd}" >/dev/null 2>&1 || die "${cmd} is missing (dpkg-dev is required)"
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
    if [ "${version}" != "${OPERETA_VERSION}" ]; then
        die "${name}: debian/changelog version (${version}) differs from OPERETA_VERSION (${OPERETA_VERSION})"
    fi

    src="${WORK}/src-${name}"
    cp -a "${pkgdir}" "${src}"
    # Go 모듈은 한 곳에서 관리하며 패키지의 임시 사본에만 넣는다.
    if [ "${name}" = opereta-daemon ]; then
        cp -a "${ROOT}/daemon" "${src}/daemon"
    fi
    # 키링 패키지는 저장소 루트의 공개 키 디렉터리를 쓴다.
    if [ "${name}" = opereta-archive-keyring ] && [ -d "${ROOT}/keys" ]; then
        cp -a "${ROOT}/keys" "${src}/keys"
    fi
    # 브랜딩 패키지는 저장소 루트의 브랜드 이미지(assets/brand)를 쓴다.
    # 데스크톱 패키지는 생성된 xfwm4 테마 이미지를 쓴다.
    if [ "${name}" = opereta-branding ] || [ "${name}" = opereta-desktop ]; then
        cp -a "${ROOT}/assets/brand" "${src}/assets"
    fi
    # Control Center 소스도 패키지의 임시 사본에만 넣는다.
    if [ "${name}" = opereta-control-center ]; then
        cp -a "${ROOT}/gui" "${src}/gui"
    fi
    (cd "${src}" && dpkg-buildpackage -us -uc -b)

    shopt -s nullglob
    debs=("${WORK}"/*.deb)
    shopt -u nullglob
    [ "${#debs[@]}" -gt 0 ] || die "${name}: no .deb was built"

    for deb in "${debs[@]}"; do
        base="$(basename "${deb}")"
        # 같은 패키지의 이전 버전이 남아 있으면 지운다.
        rm -f "${DEST}/${base%%_*}"_*.deb
        mv -f "${deb}" "${DEST}/${base}"
        echo "build-debs: ${DEST}/${base}"
    done
    built=$((built + 1))
done

[ "${built}" -gt 0 ] || die "no packages/*/debian directories found"
echo "build-debs: built ${built} packages"
