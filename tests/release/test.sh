#!/usr/bin/env bash
# scripts/make-release-assets.sh 를 가짜 ISO 와 임시 서명 키로 확인한다. 컨테이너 안에서만 실행한다.
set -Eeuo pipefail
trap 'echo "FAIL: release assets test at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ -f /.dockerenv ]] || { echo "run this inside the test container (scripts/test-release-in-docker.sh)" >&2; exit 2; }
export DEBIAN_FRONTEND=noninteractive LC_ALL=C
SRC=/src
WORK=/tmp/release-test
rm -rf "$WORK"; mkdir -p "$WORK"/{out/debs,gnupg,build,bin}
chmod 700 "$WORK/gnupg"
# shellcheck source=/dev/null
. "$SRC/opereta.conf"
ISO="opereta-${OPERETA_VERSION}-amd64.iso"

pass() { echo "ok: $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }

# 가짜 ISO 와 체크섬, 패키지 두 개.
head -c 4096 /dev/urandom > "$WORK/out/$ISO"
(cd "$WORK/out" && sha256sum "$ISO" > "$ISO.sha256")
mkdeb() {
    local d="$WORK/build/$1"
    mkdir -p "$d/DEBIAN" "$d/usr/share/$1"
    printf 'Package: %s\nVersion: %s\nArchitecture: all\nMaintainer: Test <test@example.invalid>\nDescription: test\n' "$1" "$OPERETA_VERSION" > "$d/DEBIAN/control"
    dpkg-deb --build "$d" "$WORK/out/debs/$1_${OPERETA_VERSION}_all.deb" > /dev/null
}
mkdeb opereta-base
mkdeb opereta-daemon

# 임시 서명 키와, 그 공개 키(이미지에 들어갈 키링 역할).
export GNUPGHOME="$WORK/gnupg"
gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Opereta Test <test@example.invalid>' ed25519 sign 1d > /dev/null 2>&1
KEY="$(gpg --batch --with-colons --list-secret-keys | awk -F: '/^fpr:/ {print $10; exit}')"
gpg --batch --export "$KEY" > "$WORK/pub.pgp"
unset GNUPGHOME
mkdir -p "$WORK/gnupg-other"; chmod 700 "$WORK/gnupg-other"
GNUPGHOME="$WORK/gnupg-other" gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Other <other@example.invalid>' ed25519 sign 1d > /dev/null 2>&1
GNUPGHOME="$WORK/gnupg-other" gpg --batch --export > "$WORK/other.pgp"

# 컨테이너 래퍼 대신 build-apt-repo.sh 를 직접 부르는 빌더.
cat > "$WORK/bin/builder.sh" <<'B'
#!/usr/bin/env bash
set -euo pipefail
exec bash /src/scripts/build-apt-repo.sh --flat --debs "$1" --out "$2/tree" --key "$OPERETA_APT_KEY_ID" --gnupghome "$OPERETA_APT_GNUPGHOME"
B
export OPERETA_OUT="$WORK/out" RELEASE_DIR="$WORK/release" OPERETA_APT_BUILDER="$WORK/bin/builder.sh"

# 1) 키가 없으면 ISO 와 체크섬만 모으고 경고한다. 저장소 파일은 만들지 않는다.
out="$(bash "$SRC/scripts/make-release-assets.sh" 2>&1)"
grep -q 'OS 업데이트 저장소를 만들지 않았다' <<< "$out" || die "no warning without a signing key"
[[ -s "$RELEASE_DIR/$ISO" && -s "$RELEASE_DIR/$ISO.sha256" && -s "$RELEASE_DIR/SHA256SUMS" ]] || die "ISO assets are missing"
[[ ! -e "$RELEASE_DIR/InRelease" && ! -e "$RELEASE_DIR/Packages.xz" ]] || die "an unsigned repository was published"
(cd "$RELEASE_DIR" && sha256sum -c SHA256SUMS > /dev/null) || die "SHA256SUMS does not verify"
pass "without a signing key only the ISO is collected"

# 2) 키가 있으면 서명된 평평한 저장소와 공개 키가 함께 모인다.
export OPERETA_APT_GNUPGHOME="$WORK/gnupg" OPERETA_APT_KEY_ID="$KEY" OPERETA_PUBLIC_KEY="$WORK/pub.pgp"
bash "$SRC/scripts/make-release-assets.sh" > /dev/null
for f in "$ISO" "$ISO.sha256" InRelease Release Release.gpg Packages Packages.xz opereta-base_${OPERETA_VERSION}_all.deb opereta-daemon_${OPERETA_VERSION}_all.deb opereta-archive-keyring.pgp SHA256SUMS; do
    [[ -s "$RELEASE_DIR/$f" ]] || die "missing release asset $f"
done
(cd "$RELEASE_DIR" && sha256sum -c SHA256SUMS > /dev/null) || die "SHA256SUMS does not verify"
grep -q ' opereta-base_' "$RELEASE_DIR/SHA256SUMS" || die "the packages are not in SHA256SUMS"
! grep -q ' SHA256SUMS$' "$RELEASE_DIR/SHA256SUMS" || die "SHA256SUMS lists itself"
gpg --batch --no-default-keyring --keyring "$WORK/verify.kbx" --import "$WORK/pub.pgp" > /dev/null 2>&1
gpg --batch --no-default-keyring --keyring "$WORK/verify.kbx" --verify "$RELEASE_DIR/InRelease" > /dev/null 2>&1 || die "InRelease does not verify with the public key"
pass "a signed flat repository is collected with the ISO"

# 3) 이미지에 들어갈 공개 키가 서명 키와 다르면 거절한다(설치된 시스템이 저장소를 믿지 못한다).
if OPERETA_PUBLIC_KEY="$WORK/other.pgp" bash "$SRC/scripts/make-release-assets.sh" > /dev/null 2>&1; then
    die "a signing key that differs from the keyring was accepted"
fi
pass "a signing key that differs from the shipped keyring is refused"

# 4) 공개 키가 없으면 거절한다.
if OPERETA_PUBLIC_KEY="$WORK/missing.pgp" bash "$SRC/scripts/make-release-assets.sh" > /dev/null 2>&1; then
    die "a signed release without the public key in the repository was accepted"
fi
pass "a missing public key is refused"

# 5) ISO 가 없거나 체크섬이 틀리면 거절한다.
unset OPERETA_APT_GNUPGHOME OPERETA_APT_KEY_ID OPERETA_PUBLIC_KEY
printf 'x' >> "$WORK/out/$ISO"
if bash "$SRC/scripts/make-release-assets.sh" > /dev/null 2>&1; then die "a corrupted ISO was accepted"; fi
rm -f "$WORK/out/$ISO"
if bash "$SRC/scripts/make-release-assets.sh" > /dev/null 2>&1; then die "a missing ISO was accepted"; fi
pass "a missing or corrupted ISO is refused"
echo "release assets: all checks passed"
