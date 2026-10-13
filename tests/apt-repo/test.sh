#!/usr/bin/env bash
# build-apt-repo.sh 가 만든 저장소를 실제 apt 가 서명 검증하며 쓰는지 확인한다. 컨테이너 안에서만 실행한다.
set -Eeuo pipefail
trap 'echo "FAIL: apt repo test at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ -f /.dockerenv ]] || { echo "run this inside the test container (scripts/test-apt-repo-in-docker.sh)" >&2; exit 2; }
export DEBIAN_FRONTEND=noninteractive LC_ALL=C
SRC=/src
WORK=/tmp/aptrepo
rm -rf "$WORK"; mkdir -p "$WORK"/{debs,gnupg,gnupg2,build}
chmod 700 "$WORK/gnupg" "$WORK/gnupg2"

pass() { echo "ok: $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }

mkdeb() { # NAME VERSION ARCH
    local d="$WORK/build/$1_$2"
    rm -rf "$d"; mkdir -p "$d/DEBIAN"
    {
        echo "Package: $1"; echo "Version: $2"; echo "Architecture: $3"
        echo "Maintainer: Test <test@example.invalid>"; echo "Description: test $1"
    } > "$d/DEBIAN/control"
    mkdir -p "$d/usr/share/$1"; echo "$2" > "$d/usr/share/$1/version"
    dpkg-deb --build "$d" "$WORK/debs/$1_$2_$3.deb" > /dev/null
}

mkdeb diparnux-foo 1.0 all
mkdeb diparnux-foo 1.1 all
ARCH="$(dpkg --print-architecture)"
mkdeb diparnux-bar 2.0 "$ARCH"
mkdeb evil-pkg 9.9 all

# 서명 키(임시)를 만든다.
export GNUPGHOME="$WORK/gnupg"
gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Diparnux Test <test@example.invalid>' ed25519 sign 1d > /dev/null 2>&1
KEY="$(gpg --batch --with-colons --list-secret-keys | awk -F: '/^fpr:/ {print $10; exit}')"
gpg --batch --export "$KEY" > "$WORK/good.pgp"
GNUPGHOME="$WORK/gnupg2" gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Other <other@example.invalid>' ed25519 sign 1d > /dev/null 2>&1
GNUPGHOME="$WORK/gnupg2" gpg --batch --export > "$WORK/other.pgp"

bash "$SRC/scripts/build-apt-repo.sh" --debs "$WORK/debs" --out "$WORK/tree" --key "$KEY" --gnupghome "$WORK/gnupg"
for f in dists/diparnux/InRelease dists/diparnux/Release dists/diparnux/Release.gpg dists/diparnux/main/binary-$ARCH/Packages dists/diparnux/main/binary-$ARCH/Packages.xz; do
    [[ -s "$WORK/tree/$f" ]] || die "missing $f"
done
grep -q '^Origin: Diparnux$' "$WORK/tree/dists/diparnux/Release" || die "Origin is not Diparnux"
grep -q '^Package: diparnux-foo$' "$WORK/tree/dists/diparnux/main/binary-$ARCH/Packages" || die "Architecture all package is not indexed"
[[ "$(grep -c '^Package: diparnux-foo$' "$WORK/tree/dists/diparnux/main/binary-$ARCH/Packages")" == 2 ]] || die "both versions must be indexed"
pass "repository layout and index"

# 클라이언트 설정: 키링과 소스 파일(deb822), 우선순위.
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
mkdir -p /usr/share/keyrings /etc/apt/preferences.d
cp "$WORK/good.pgp" /usr/share/keyrings/diparnux-test.pgp
cat > /etc/apt/sources.list.d/diparnux.sources <<EOF
Types: deb
URIs: file://$WORK/tree
Suites: diparnux
Components: main
Architectures: $ARCH
Signed-By: /usr/share/keyrings/diparnux-test.pgp
EOF
cp "$SRC/packages/diparnux-archive-keyring/src/10-diparnux.pref" /etc/apt/preferences.d/10-diparnux.pref

apt-get update -q > /dev/null
pass "apt verifies the signature and updates"
[[ "$(apt-cache policy diparnux-foo | awk '/Candidate:/ {print $2}')" == 1.1 ]] || die "newest version is not the candidate"
apt-cache madison diparnux-foo | grep -q ' 1.0 ' || die "the older version is not available for rollback"
pass "both versions are available"
apt-get install -y -q diparnux-foo diparnux-bar > /dev/null
[[ "$(cat /usr/share/diparnux-foo/version)" == 1.1 ]] || die "wrong version installed"
apt-get install -y -q --allow-downgrades diparnux-foo=1.0 > /dev/null
[[ "$(cat /usr/share/diparnux-foo/version)" == 1.0 ]] || die "downgrade failed"
pass "install and downgrade"

# 우선순위: diparnux-* 가 아닌 패키지는 이 저장소에서 받지 않는다.
if apt-get install -y -q evil-pkg > /dev/null 2>&1; then die "a package outside diparnux-* was installable from the Diparnux repository"; fi
[[ "$(apt-cache policy evil-pkg | awk '/Candidate:/ {print $2}')" == "(none)" ]] || die "evil-pkg has a candidate"
pass "the pin keeps other packages out"

# 변조: 색인을 바꾸면 해시가 맞지 않아 update 가 실패한다.
cp -a "$WORK/tree" "$WORK/tree.bak"
printf 'Package: diparnux-evil\nVersion: 1\nArchitecture: all\n\n' | xz > "$WORK/tree/dists/diparnux/main/binary-$ARCH/Packages.xz"
rm -f "$WORK/tree/dists/diparnux/main/binary-$ARCH/Packages"
rm -rf /var/lib/apt/lists/*
if apt-get update -q > /dev/null 2>&1; then
    # 일부 apt 는 오류를 경고로만 내므로 후보가 생기지 않았는지도 확인한다.
    if apt-cache policy diparnux-evil 2>/dev/null | grep -q 'Candidate: 1'; then die "a tampered index was accepted"; fi
fi
pass "a tampered index is rejected"
rm -rf "$WORK/tree"; mv "$WORK/tree.bak" "$WORK/tree"

# 다른 키로 서명한 것으로 바꾸면 거절한다.
cp "$WORK/other.pgp" /usr/share/keyrings/diparnux-test.pgp
rm -rf /var/lib/apt/lists/*
if apt-get update -q > /dev/null 2>&1; then
    apt-cache policy diparnux-foo | grep -q 'Candidate: 1.1' && die "an index signed by an unknown key was accepted"
fi
pass "a repository signed by another key is rejected"
echo "apt repository: all checks passed"
