#!/usr/bin/env bash
# GitHub 릴리스에 올릴 파일을 out/release/ 에 모은다. 릴리스를 만드는 것은 .github/workflows/release.yml 이다.
#
#   scripts/make-release-assets.sh
#
# 입력(make iso 의 결과): out/opereta-<버전>-amd64.iso, 그 .sha256, out/debs/*.deb
# 출력: out/release/
#   opereta-<버전>-amd64.iso, .iso.sha256        ISO 와 체크섬(OPERETA_PUBLISH_ISO=yes 일 때만. 기본은 올리지 않는다)
#   InRelease, Release, Release.gpg, Packages, Packages.xz, opereta-*.deb   OS 업데이트 저장소(평평한 형식)
#   opereta-archive-keyring.pgp                  저장소 공개 키(수동 검증용)
#   SHA256SUMS                                   위 모든 파일의 체크섬
#
# 저장소는 서명 키가 있을 때만 만든다(OPERETA_APT_GNUPGHOME, OPERETA_APT_KEY_ID). 키가 없으면 ISO 와 체크섬만 모으고
# 이 릴리스에서는 OS 업데이트를 받을 수 없다는 경고를 낸다. 서명되지 않은 저장소는 만들지 않는다.
# 서명 개인 키는 이 저장소에 두지 않는다(docs/releases.md).
# 환경 변수: OPERETA_RELEASE_ISO(opereta.conf 의 OPERETA_PUBLISH_ISO 를 덮어쓴다, yes/no), OPERETA_OUT(입력과 출력의 기준, 기본 out), RELEASE_DIR, OPERETA_PUBLIC_KEY(기본 keys/opereta-archive-keyring.pgp),
#            OPERETA_APT_BUILDER(저장소를 만드는 스크립트. 시험용)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"
OUT="${OPERETA_OUT:-${ROOT}/out}"
REL="${RELEASE_DIR:-${OUT}/release}"
ISO="opereta-${OPERETA_VERSION}-amd64.iso"
PUBLISH_ISO="${OPERETA_RELEASE_ISO:-${OPERETA_PUBLISH_ISO:-no}}"
[[ "${PUBLISH_ISO}" == yes || "${PUBLISH_ISO}" == no ]] || { echo "make-release-assets: OPERETA_PUBLISH_ISO must be yes or no" >&2; exit 1; }
# GitHub 릴리스 자산은 파일 하나가 2 GiB 미만이어야 한다.
MAX_ASSET=$((2 * 1024 * 1024 * 1024 - 1))

die() { echo "make-release-assets: $*" >&2; exit 1; }
warn() { echo "::warning::make-release-assets: $*"; }

# ISO 는 항상 만들었어야 한다(패키지가 같은 빌드에서 나온다). 올릴 때만 크기와 체크섬을 요구한다.
[[ -s "${OUT}/${ISO}" ]] || die "ISO 가 없다: ${OUT}/${ISO} (먼저 make iso)"
[[ -s "${OUT}/${ISO}.sha256" ]] || die "ISO 체크섬이 없다: ${OUT}/${ISO}.sha256"
(cd "${OUT}" && sha256sum -c "${ISO}.sha256" > /dev/null) || die "ISO 체크섬이 맞지 않는다"

rm -rf "${REL}"
mkdir -p "${REL}"
if [[ "${PUBLISH_ISO}" == yes ]]; then
    size="$(wc -c < "${OUT}/${ISO}" | tr -d ' ')"
    [[ "${size}" -le "${MAX_ASSET}" ]] || die "ISO 가 릴리스 자산 한도(2 GiB)를 넘는다: ${size} 바이트"
    # GPL 등이 요구하는 대응 소스를 같은 곳에 올린다(GPLv2 3a). 소스가 없으면 ISO 를 올리지 않는다.
    shopt -s nullglob
    src_parts=("${OUT}/sources/opereta-${OPERETA_VERSION}-sources-"*.tar)
    shopt -u nullglob
    [[ ${#src_parts[@]} -gt 0 ]] || die "대응 소스가 없다: ${OUT}/sources (make sources). 소스 없이 ISO 를 올리지 않는다"
    for f in "${OUT}/sources/opereta-${OPERETA_VERSION}-sources.tsv" "${OUT}/sources/SOURCES-README.txt"; do
        [[ -s "${f}" ]] || die "소스 목록 파일이 없다: ${f}"
    done
    for p in "${src_parts[@]}"; do
        psize="$(wc -c < "${p}" | tr -d ' ')"
        [[ "${psize}" -le "${MAX_ASSET}" ]] || die "소스 묶음이 릴리스 자산 한도(2 GiB)를 넘는다: ${p}"
    done
    cp -f "${OUT}/${ISO}" "${OUT}/${ISO}.sha256" "${REL}/"
    cp -f "${src_parts[@]}" "${OUT}/sources/opereta-${OPERETA_VERSION}-sources.tsv" "${OUT}/sources/SOURCES-README.txt" "${REL}/"
    [[ -s "${OUT}/package-manifest.tsv" ]] || die "패키지 목록이 없다: ${OUT}/package-manifest.tsv"
    cp -f "${OUT}/package-manifest.tsv" "${REL}/opereta-${OPERETA_VERSION}-packages.tsv"
    # 이 프로젝트의 소스(같은 릴리스의 태그). 저장소 안에서 실행할 때만 만든다.
    if git -C "${ROOT}" rev-parse --git-dir > /dev/null 2>&1; then
        git -C "${ROOT}" archive --format=tar.gz --prefix="operetaos-${OPERETA_VERSION}/" -o "${REL}/opereta-${OPERETA_VERSION}-source.tar.gz" HEAD
    fi
else
    echo "make-release-assets: ISO 는 릴리스에 올리지 않는다(OPERETA_PUBLISH_ISO=no)"
fi

if [[ -n "${OPERETA_APT_GNUPGHOME:-}" && -n "${OPERETA_APT_KEY_ID:-}" ]]; then
    [[ -d "${OUT}/debs" ]] || die "패키지 디렉터리가 없다: ${OUT}/debs"
    pub="${OPERETA_PUBLIC_KEY:-${ROOT}/keys/opereta-archive-keyring.pgp}"
    [[ -s "${pub}" ]] || die "공개 키가 저장소에 없다: keys/opereta-archive-keyring.pgp. 이 키가 없는 이미지는 저장소를 믿지 않는다"
    # 이미지에 들어간 공개 키와 서명 키가 같은 키인지 본다. 다르면 설치된 시스템이 이 저장소를 거절한다.
    if command -v gpg > /dev/null 2>&1; then
        want="$(gpg --batch --with-colons --show-keys "${pub}" | awk -F: '/^fpr:/ {print $10; exit}')"
        have="$(GNUPGHOME="${OPERETA_APT_GNUPGHOME}" gpg --batch --with-colons --list-keys "${OPERETA_APT_KEY_ID}" | awk -F: '/^fpr:/ {print $10; exit}')"
        [[ -n "${want}" && "${want}" == "${have}" ]] || die "서명 키(${have:-없음})가 keys/opereta-archive-keyring.pgp(${want:-없음})와 다르다"
    else
        warn "gpg 가 없어 공개 키와 서명 키가 같은지 확인하지 못했다"
    fi
    # 저장소 빌더. 기본은 컨테이너 래퍼이고 컨테이너 안의 시험은 build-apt-repo.sh 를 직접 부르는 것으로 바꾼다.
    OPERETA_APT_LAYOUT=flat bash "${OPERETA_APT_BUILDER:-${ROOT}/scripts/build-apt-repo-in-docker.sh}" "${OUT}/debs" "${OUT}/apt"
    tree="${OUT}/apt/tree"
    for f in InRelease Release Release.gpg Packages Packages.xz; do
        [[ -s "${tree}/${f}" ]] || die "저장소 파일이 없다: ${f}"
    done
    cp -f "${tree}"/InRelease "${tree}"/Release "${tree}"/Release.gpg "${tree}"/Packages "${tree}"/Packages.xz "${REL}/"
    cp -f "${tree}"/*.deb "${REL}/"
    cp -f "${pub}" "${REL}/opereta-archive-keyring.pgp"
    echo "make-release-assets: OS 업데이트 저장소를 만들었다($(find "${tree}" -maxdepth 1 -name '*.deb' | wc -l | tr -d ' ')개 패키지)"
else
    warn "서명 키가 없어 OS 업데이트 저장소를 만들지 않았다. 설치된 시스템은 이 릴리스로 업데이트할 수 없다."
fi
[[ -n "$(find "${REL}" -mindepth 1 -maxdepth 1 -type f)" ]] || die "릴리스에 올릴 파일이 없다(ISO 도 저장소도 없음). 서명 키를 설정하거나 OPERETA_PUBLISH_ISO=yes 로 한다"

# 체크섬 파일은 대상 디렉터리 밖에서 만든 뒤 옮긴다(자기 자신을 읽지 않게).
sums="$(mktemp)"
(cd "${REL}" && find . -maxdepth 1 -type f -printf '%P\n' | LC_ALL=C sort | xargs sha256sum) > "${sums}"
mv "${sums}" "${REL}/SHA256SUMS"
echo "make-release-assets: ${REL}"
(cd "${REL}" && ls -l)
