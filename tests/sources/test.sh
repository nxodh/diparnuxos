#!/usr/bin/env bash
# 소스 수집기를 작은 실제 패키지(hello)와 가짜 항목으로 확인한다. 컨테이너 안에서만 실행한다(네트워크 필요).
set -Eeuo pipefail
trap 'echo "FAIL: sources test at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ -f /.dockerenv ]] || { echo "run this inside the test container" >&2; exit 2; }
SRC=/src
W=/tmp/sources-test
rm -rf "$W"; mkdir -p "$W"
pass() { echo "ok: $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }

bash "$SRC/scripts/collect-sources-run.sh" --setup-only
ver="$(apt-cache showsrc hello | awk '/^Version:/ {print $2; exit}')"
[[ -n "$ver" ]] || die "no source version for hello"
tab=$'\t'

# 1) Debian 패키지는 받고, 이 프로젝트와 NVIDIA 의 패키지는 이유와 함께 건너뛴다.
{
    printf 'hello%s%s%samd64%shello%s%s%sDebian Developer <x@example.invalid>\n' "$tab" "$ver" "$tab" "$tab" "$tab" "$ver" "$tab"
    printf 'opereta-base%s0.6.1%sall%sopereta-base%s0.6.1%sOpereta Developers <noreply@users.noreply.github.com>\n' "$tab" "$tab" "$tab" "$tab" "$tab"
    printf 'libcuda-fake%s1.0%samd64%slibcuda-fake%s1.0%sNVIDIA CORPORATION <cudatools@nvidia.com>\n' "$tab" "$tab" "$tab" "$tab" "$tab"
} > "$W/manifest.tsv"
python3 "$SRC/scripts/collect-sources.py" --manifest "$W/manifest.tsv" --out "$W/out" --prefix test-sources > "$W/log.txt" 2>&1 || { cat "$W/log.txt" >&2; die "collection failed"; }
[[ -s "$W/out/test-sources-01.tar" && -s "$W/out/test-sources.tsv" && -s "$W/out/SOURCES-README.txt" ]] || die "outputs are missing"
tar -tf "$W/out/test-sources-01.tar" | grep -q '^hello/hello_.*\.dsc$' || die "the .dsc of hello is not in the archive"
tar -tf "$W/out/test-sources-01.tar" | grep -q '^hello/hello_.*\.orig\.tar\.' || die "the upstream tarball of hello is not in the archive"
if tar -tf "$W/out/test-sources-01.tar" | grep -q -E 'opereta-base|libcuda-fake'; then die "a non-Debian package was collected"; fi
grep -q 'opereta-base 0.6.1: opereta' "$W/out/SOURCES-README.txt" || die "the skipped project package is not recorded"
grep -q 'libcuda-fake 1.0: nvidia' "$W/out/SOURCES-README.txt" || die "the skipped NVIDIA package is not recorded"
[[ ! -d "$W/out/_work" ]] || die "the work directory was left behind"
pass "Debian source collected exactly; project and NVIDIA packages are skipped with a reason"

# 2) 없는 소스 버전이 있으면 실패한다(조용히 건너뛰지 않는다).
printf 'hello%s9.9.9-bogus%samd64%shello%s9.9.9-bogus%sDebian Developer <x@example.invalid>\n' "$tab" "$tab" "$tab" "$tab" "$tab" > "$W/bad.tsv"
if python3 "$SRC/scripts/collect-sources.py" --manifest "$W/bad.tsv" --out "$W/out2" --prefix bad > "$W/bad.log" 2>&1; then die "a missing source version was accepted"; fi
grep -q 'hello=9.9.9-bogus' "$W/bad.log" || die "the failure does not name the package"
pass "a source version that cannot be obtained fails the collection"

# 3) 소스 패키지 하나가 한 묶음의 한도보다 크면 쪼개지 않고 알린다(한도를 0 으로 해서 확인한다).
python3 "$SRC/scripts/collect-sources.py" --manifest "$W/manifest.tsv" --out "$W/out3" --prefix split --max-part-mib 0 > "$W/split.log" 2>&1 \
    && die "a source package larger than a part must fail" || true
grep -q 'larger than a part' "$W/split.log" || die "oversized source package was not reported"
pass "an oversized source package is reported instead of being split"
echo "sources: all checks passed"
