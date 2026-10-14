#!/usr/bin/env bash
# CUDA 런타임 번들링(llama/bundle-cuda-runtime.sh, llama/verify-cuda-bundle.sh)과 패키징 규칙을 합성 라이브러리로 확인한다.
# 컨테이너 안에서만 실행한다(scripts/test-llama-bundle-in-docker.sh). 진짜 NVIDIA 파일은 쓰지 않는다. 같은 소네임과
# RUNPATH 구성을 가진 가짜 라이브러리를 만들어서, 번들링이 지켜야 하는 성질을 본다.
#   1. 허용된 소네임만 복사하고 원본과 같다(수정 없음). 소네임 링크가 만들어진다.
#   2. dpkg-buildpackage 를 거친 뒤에도 NVIDIA 라이브러리가 바뀌지 않는다(strip 이 건드리지 않는다).
#   3. 시스템 링커 캐시 없이 전용 디렉터리에서만 찾아진다.
#   4. 허용 목록 밖의 CUDA 라이브러리가 필요하면, 계약서가 없으면, 거절한다.
set -Eeuo pipefail
trap 'echo "FAIL: bundle test at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ -f /.dockerenv ]] || { echo "run this inside the test container" >&2; exit 2; }
export DEBIAN_FRONTEND=noninteractive LC_ALL=C
SRC=/src
W=/tmp/bundle-test
rm -rf "$W"; mkdir -p "$W"/{cuda/targets/x86_64-linux/lib,stage,doc,lic,other}
pass() { echo "ok: $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }

CUDA="$W/cuda/targets/x86_64-linux/lib"
# 디버그 정보와 심볼이 있는 라이브러리여서 strip 하면 내용이 바뀐다. 가짜 NVIDIA 라이브러리.
# RUNPATH 는 리터럴 $ORIGIN 이어야 한다(셸이 풀면 안 된다).
# shellcheck disable=SC2016
ORIGIN_RPATH='-Wl,-rpath,$ORIGIN'
mk() { # <soname-base> <version> [needed-soname]
    local base="$1" ver="$2" dep="${3:-}" major real
    major="${ver%%.*}"
    real="$CUDA/$base.so.$ver"
    echo "int ${base//[^a-z0-9]/_}_fn(void){return 1;}" > "$W/$base.c"
    if [[ -n "$dep" ]]; then
        gcc -g -O0 -shared -fPIC -Wl,-soname,"$base.so.$major" "$ORIGIN_RPATH" -o "$real" "$W/$base.c" -L"$CUDA" -Wl,--no-as-needed "-l:$dep"
    else
        gcc -g -O0 -shared -fPIC -Wl,-soname,"$base.so.$major" -o "$real" "$W/$base.c"
    fi
    ln -sfn "$base.so.$ver" "$CUDA/$base.so.$major"
}
mk libcudart 13.2.86
mk libcublasLt 13.4.1.3
mk libcublas 13.4.1.3 libcublasLt.so.13
mk libnvrtc 13.2.1          # 허용 목록에 없는 CUDA 라이브러리
# 모듈(우리가 빌드한 libggml-cuda.so 역할). RUNPATH 는 $ORIGIN, NEEDED 는 CUDA 런타임.
echo 'int ggml_cuda_fn(void){return 2;}' > "$W/ggml.c"
gcc -g -shared -fPIC "$ORIGIN_RPATH" -o "$W/stage/libggml-cuda.so" "$W/ggml.c" -L"$CUDA" -Wl,--no-as-needed -l:libcudart.so.13 -l:libcublas.so.13
# 빌드 컨테이너처럼 시스템 링커 캐시에 CUDA 디렉터리가 등록되어 있다.
echo "$CUDA" > /etc/ld.so.conf.d/cuda-fake.conf
ldconfig
printf 'NVIDIA Software License Agreement and CUDA Supplement\nEnd User License Agreement\n' > "$W/lic/cudart.copyright"
cp "$W/lic/cudart.copyright" "$W/lic/cublas.copyright"

# 1. 번들링
bash "$SRC/llama/bundle-cuda-runtime.sh" --module "$W/stage/libggml-cuda.so" --stage "$W/stage" --doc "$W/doc" \
    --license "cuda-cudart-13-2=$W/lic/cudart.copyright" --license "libcublas-13-2=$W/lic/cublas.copyright"
for f in libcudart.so.13.2.86 libcublas.so.13.4.1.3 libcublasLt.so.13.4.1.3; do
    cmp -s "$CUDA/$f" "$W/stage/$f" || die "$f is not identical to the original"
done
for l in libcudart.so.13 libcublas.so.13 libcublasLt.so.13; do
    [[ -L "$W/stage/$l" ]] || die "soname link missing: $l"
done
[[ ! -e "$W/stage/libnvrtc.so.13" && ! -e "$W/stage/libnvrtc.so.13.2.1" ]] || die "a library outside the redistribution list was copied"
[[ -s "$W/doc/cuda-licenses/cuda-cudart-13-2.copyright" && -s "$W/doc/NOTICE.cuda-runtime" && -s "$W/doc/cuda-runtime-files.txt" ]] || die "licenses or notice missing"
pass "only the allowed libraries are copied, unmodified, with soname links, licenses and notice"

# 2. 시스템 링커 캐시 없이 전용 디렉터리에서만 찾아진다.
bash "$SRC/llama/verify-cuda-bundle.sh" "$W/stage" > /dev/null
pass "the CUDA runtime resolves only inside the private directory"
# 번들이 비면 시스템 캐시(위에서 등록함)로 대신 찾아지면 안 된다. 즉 검사가 시스템 CUDA 를 쓰지 않는다.
mkdir -p "$W/empty"; cp "$W/stage/libggml-cuda.so" "$W/empty/"
if bash "$SRC/llama/verify-cuda-bundle.sh" "$W/empty" > /dev/null 2>&1; then die "verification passed although the runtime was not bundled"; fi
pass "verification does not fall back to the system CUDA"

# 3. 실제 패키징 규칙(strip, shlibdeps 제외)을 거쳐도 NVIDIA 라이브러리는 바뀌지 않는다.
apt-get update -qq > /dev/null
apt-get install -y -qq --no-install-recommends build-essential debhelper dpkg-dev > /dev/null
P="$W/pkg/opereta-llm-0.0.0"
mkdir -p "$P"
cp -a "$SRC/llama/pkg/debian" "$P/debian"
sed -i 's/@OPERETA_NVIDIA_DEPENDS@//; s|@OPERETA_HOME_URL@|https://example.invalid|g' "$P/debian/control"
printf 'opereta-llm (0.0.0) trixie; urgency=medium\n\n  * test\n\n -- Test <noreply@users.noreply.github.com>  Mon, 01 Jan 2024 00:00:00 +0000\n' > "$P/debian/changelog"
mkdir -p "$P/staging/usr/lib/opereta/llama" "$P/staging/usr/share/doc/opereta-llm"
cp -a "$W/stage/." "$P/staging/usr/lib/opereta/llama/"
cp -a "$W/doc/." "$P/staging/usr/share/doc/opereta-llm/"
(cd "$P" && dpkg-buildpackage -us -uc -b > "$W/build.log" 2>&1) || { tail -30 "$W/build.log" >&2; die "dpkg-buildpackage failed"; }
dpkg-deb -x "$W/pkg/opereta-llm_0.0.0_$(dpkg --print-architecture).deb" "$W/unpacked"
for f in libcudart.so.13.2.86 libcublas.so.13.4.1.3 libcublasLt.so.13.4.1.3; do
    cmp -s "$CUDA/$f" "$W/unpacked/usr/lib/opereta/llama/$f" || die "packaging modified $f (strip must skip NVIDIA libraries)"
done
# 우리가 만든 모듈은 strip 된다(규칙이 모든 strip 을 끈 것이 아니다).
if cmp -s "$W/stage/libggml-cuda.so" "$W/unpacked/usr/lib/opereta/llama/libggml-cuda.so"; then die "our own module was not stripped"; fi
# 패키지가 시스템 링커 경로에 아무것도 등록하지 않는다.
if dpkg-deb -c "$W/pkg/opereta-llm_0.0.0_$(dpkg --print-architecture).deb" | grep -E 'etc/ld.so.conf.d|usr/lib/[^/]+/lib(cudart|cublas)'; then die "the package registers CUDA libraries on a system path"; fi
if dpkg-deb -I "$W/pkg/opereta-llm_0.0.0_$(dpkg --print-architecture).deb" | grep -qi triggers; then
    ctl="$(dpkg-deb -e "$W/pkg/opereta-llm_0.0.0_$(dpkg --print-architecture).deb" "$W/ctl" && cat "$W/ctl/triggers" 2>/dev/null || true)"
    [[ -z "$ctl" ]] || die "the package has linker triggers: $ctl"
fi
pass "packaging leaves the NVIDIA libraries byte-identical and registers nothing"

# 4. 거절 조건
mkdir -p "$W/bad"; cp "$W/stage/libggml-cuda.so" "$W/bad/"
gcc -g -shared -fPIC "$ORIGIN_RPATH" -o "$W/bad/libggml-cuda.so" "$W/ggml.c" -L"$CUDA" -Wl,--no-as-needed -l:libcudart.so.13 -l:libcublas.so.13 -l:libnvrtc.so.13
if bash "$SRC/llama/bundle-cuda-runtime.sh" --module "$W/bad/libggml-cuda.so" --stage "$W/bad" --doc "$W/baddoc" \
    --license "cuda-cudart-13-2=$W/lic/cudart.copyright" > /dev/null 2>&1; then die "a CUDA library outside the redistribution list was accepted"; fi
pass "a CUDA library that is not on the redistribution list is refused"
if bash "$SRC/llama/bundle-cuda-runtime.sh" --module "$W/stage/libggml-cuda.so" --stage "$W/stage" --doc "$W/doc2" \
    --license "cuda-cudart-13-2=$W/lic/missing.copyright" > /dev/null 2>&1; then die "a missing license text was accepted"; fi
printf 'not a license\n' > "$W/lic/wrong.copyright"
if bash "$SRC/llama/bundle-cuda-runtime.sh" --module "$W/stage/libggml-cuda.so" --stage "$W/stage" --doc "$W/doc3" \
    --license "cuda-cudart-13-2=$W/lic/wrong.copyright" > /dev/null 2>&1; then die "a file that is not the NVIDIA agreement was accepted"; fi
pass "a missing or wrong license text is refused"
echo "cuda bundle: all checks passed"
