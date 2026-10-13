#!/usr/bin/env bash
# Diparnux 자체 APT 저장소를 만든다. 입력은 .deb 디렉터리이고 출력은 서명된 저장소 트리다.
#
#   build-apt-repo.sh --debs DIR --out DIR --key KEYID [--gnupghome DIR] [--passphrase-file FILE]
#                     [--suite diparnux] [--component main]
#
# apt-ftparchive 로 색인을 만들고 gpg 로 Release 를 서명한다(InRelease 와 Release.gpg). 서명 개인 키는 이 저장소에
# 두지 않는다. 사용자가 만든 GNUPGHOME 을 --gnupghome 으로 준다. 이 스크립트는 Docker 컨테이너 안에서 쓰도록 만들었다
# (scripts/build-apt-repo-in-docker.sh). 같은 입력이면 같은 색인이 나온다(Date 필드만 다르다).
set -euo pipefail

die() { echo "build-apt-repo: $*" >&2; exit 2; }

debs="" out="" key="" gnupghome="" passfile="" suite="diparnux" component="main"
origin="Diparnux" label="Diparnux"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --debs) debs="${2:-}"; shift 2 ;;
        --out) out="${2:-}"; shift 2 ;;
        --key) key="${2:-}"; shift 2 ;;
        --gnupghome) gnupghome="${2:-}"; shift 2 ;;
        --passphrase-file) passfile="${2:-}"; shift 2 ;;
        --suite) suite="${2:-}"; shift 2 ;;
        --component) component="${2:-}"; shift 2 ;;
        *) die "unknown argument: $1" ;;
    esac
done
[[ -n "$debs" && -d "$debs" ]] || die "--debs DIR is required"
[[ -n "$out" ]] || die "--out DIR is required"
[[ -n "$key" ]] || die "--key KEYID is required (the signing key is never stored in this repository)"
[[ "$suite" =~ ^[a-z][a-z0-9-]*$ ]] || die "invalid suite: $suite"
[[ "$component" =~ ^[a-z][a-z0-9-]*$ ]] || die "invalid component: $component"
for tool in apt-ftparchive gpg dpkg-deb xz; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
if [[ -n "$gnupghome" ]]; then
    export GNUPGHOME="$gnupghome"
fi

shopt -s nullglob
files=("$debs"/*.deb)
shopt -u nullglob
[[ ${#files[@]} -gt 0 ]] || die "no .deb files in $debs"

rm -rf "$out"
mkdir -p "$out/pool/$component"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 같은 패키지의 여러 버전은 모두 pool 에 둔다(롤백이 이전 버전을 설치할 수 있게). 이름은 Debian 의 pool 규칙을 따른다.
for deb in "${files[@]}"; do
    name="$(dpkg-deb -f "$deb" Package)"
    prefix="${name:0:1}"
    [[ "$name" == lib* ]] && prefix="${name:0:4}"
    mkdir -p "$out/pool/$component/$prefix/$name"
    cp -f "$deb" "$out/pool/$component/$prefix/$name/"
done

# 대상 아키텍처는 .deb 가 가진 값(amd64 와 all)이다.
mapfile -t archs < <(for deb in "${files[@]}"; do dpkg-deb -f "$deb" Architecture; done | sort -u | grep -v '^all$' || true)
[[ ${#archs[@]} -gt 0 ]] || archs=(amd64)
arch_list="${archs[*]}"

mkdir -p "$out/dists/$suite/$component"
for arch in "${archs[@]}"; do
    d="$out/dists/$suite/$component/binary-$arch"
    mkdir -p "$d"
    (cd "$out" && apt-ftparchive --arch "$arch" packages "pool/$component") > "$d/Packages"
    xz -9e -k -f "$d/Packages"
done

cat > "$work/release.conf" <<CONF
APT::FTPArchive::Release::Origin "$origin";
APT::FTPArchive::Release::Label "$label";
APT::FTPArchive::Release::Suite "$suite";
APT::FTPArchive::Release::Codename "$suite";
APT::FTPArchive::Release::Architectures "$arch_list";
APT::FTPArchive::Release::Components "$component";
APT::FTPArchive::Release::Description "Diparnux packages (signed)";
CONF
(cd "$out/dists/$suite" && APT_CONFIG="$work/release.conf" apt-ftparchive release .) > "$work/Release"
mv "$work/Release" "$out/dists/$suite/Release"

gpg_args=(--batch --yes --default-key "$key")
if [[ -n "$passfile" ]]; then
    [[ -r "$passfile" ]] || die "cannot read the passphrase file"
    gpg_args+=(--pinentry-mode loopback --passphrase-file "$passfile")
fi
gpg "${gpg_args[@]}" --clearsign -o "$out/dists/$suite/InRelease" "$out/dists/$suite/Release"
gpg "${gpg_args[@]}" --armor --detach-sign -o "$out/dists/$suite/Release.gpg" "$out/dists/$suite/Release"

echo "build-apt-repo: ${#files[@]} packages, suite $suite, architectures $arch_list"
echo "build-apt-repo: repository tree at $out"
