#!/usr/bin/env bash
# libggml-cuda.so 가 쓰는 CUDA 런타임(cudart, cublas, cublasLt)을 llama 패키지의 전용 디렉터리에 원본 그대로 넣는다.
#
#   bundle-cuda-runtime.sh --module <libggml-cuda.so> --stage <dir> --doc <dir> --license NAME=PATH [--license ...]
#
# NVIDIA CUDA EULA(1.1.2, 부속서 A)가 재배포를 허용하는 조건을 지키기 위한 것이다.
#   - 재배포가 허용된 파일만 넣는다: libcudart.so, libcublas.so, libcublasLt.so (아래 ALLOW).
#   - 응용 프로그램(llama-server)만 접근하게 한다: 시스템 검색 경로(/usr/lib, ld.so.cache)에 두지 않고 응용 옆의
#     전용 디렉터리에 둔다. RUNPATH($ORIGIN)로만 찾아진다.
#   - 수정하지 않는다: 파일을 그대로 복사한다. strip, patchelf 를 쓰지 않는다(패키징 규칙이 제외한다).
#   - 수령자에게 계약서를 제공한다: 원본 패키지의 copyright(계약서 전문)를 문서 디렉터리에 넣는다.
# 허용 목록 밖의 CUDA 라이브러리가 필요하면 실패한다.
set -euo pipefail

die() { echo "bundle-cuda-runtime: ERROR: $*" >&2; exit 1; }
log() { echo "bundle-cuda-runtime: $*"; }

# 재배포가 허용된 소네임(EULA 부속서 A 의 Linux 목록과 일치해야 한다).
ALLOW=(libcudart.so.13 libcublas.so.13 libcublasLt.so.13)

module="" stage="" doc=""
licenses=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --module) module="${2:-}"; shift 2 ;;
        --stage) stage="${2:-}"; shift 2 ;;
        --doc) doc="${2:-}"; shift 2 ;;
        --license) licenses+=("${2:-}"); shift 2 ;;
        *) die "unknown argument: $1" ;;
    esac
done
[[ -f "${module}" ]] || die "--module is required and must exist: ${module}"
[[ -d "${stage}" ]] || die "--stage is required and must exist: ${stage}"
[[ -n "${doc}" ]] || die "--doc is required"
[[ ${#licenses[@]} -gt 0 ]] || die "at least one --license NAME=PATH is required"
command -v ldd >/dev/null 2>&1 || die "ldd is missing"

ldd_out="$(ldd "${module}" 2>&1 || true)"

# 허용 목록 밖의 CUDA 툴킷 라이브러리가 필요하면 거절한다(경로에 /cuda 가 있는 것).
while read -r name _ path _; do
    [[ -n "${path}" && "${path}" == /* ]] || continue
    case "${path}" in
        */cuda-*/* | */cuda/*)
            ok=0
            for a in "${ALLOW[@]}"; do [[ "${name}" == "${a}" ]] && ok=1; done
            [[ "${ok}" -eq 1 ]] || die "the module needs a CUDA library that is not on the redistribution list: ${name} (${path})"
            ;;
    esac
done <<<"${ldd_out}"

mkdir -p "${doc}/cuda-licenses"
manifest="${doc}/cuda-runtime-files.txt"
: > "${manifest}"
for soname in "${ALLOW[@]}"; do
    path="$(awk -v s="${soname}" '$1 == s && $2 == "=>" && $3 ~ /^\// {print $3; exit}' <<<"${ldd_out}")"
    [[ -n "${path}" ]] || die "${soname} is not resolved by ldd for ${module}: ${ldd_out}"
    real="$(readlink -f "${path}")"
    [[ -f "${real}" ]] || die "${soname} does not resolve to a file: ${path}"
    # 소네임이 일반 파일이면 소네임으로, 링크면 실제 파일 이름으로 복사하고 소네임 링크를 만든다.
    if [[ "$(basename "${path}")" == "$(basename "${real}")" ]]; then
        target="${stage}/${soname}"
        cp "${real}" "${target}"
    else
        target="${stage}/$(basename "${real}")"
        cp "${real}" "${target}"
        ln -sfn "$(basename "${real}")" "${stage}/${soname}"
    fi
    cmp -s "${real}" "${target}" || die "the copied file differs from the original: ${soname}"
    printf '%s  %s  %s  %s\n' "$(sha256sum "${real}" | cut -d' ' -f1)" "$(wc -c < "${real}" | tr -d ' ')" "$(basename "${target}")" "${real}" >> "${manifest}"
    log "bundled ${soname} -> $(basename "${target}")"
done

for entry in "${licenses[@]}"; do
    name="${entry%%=*}"
    src="${entry#*=}"
    [[ -n "${name}" && "${name}" != "${entry}" ]] || die "invalid --license value: ${entry}"
    [[ -s "${src}" ]] || die "license text is missing or empty: ${src}"
    grep -qi 'license agreement' "${src}" || die "the license file does not look like the NVIDIA agreement: ${src}"
    cp -L "${src}" "${doc}/cuda-licenses/${name}.copyright"
done

cat > "${doc}/NOTICE.cuda-runtime" <<'EOF'
NVIDIA CUDA runtime libraries in this package

The shared libraries libcudart, libcublas and libcublasLt in this directory are NVIDIA software, distributed
unmodified with llama-server under the NVIDIA CUDA Toolkit End User License Agreement. The agreement is in
cuda-licenses/ next to this file.

They are installed in this private directory so that only llama-server (this application) uses them. They are not
registered with the system linker and must not be used by other programs. The NVIDIA driver (libcuda.so) is a
separate package with its own license.
EOF
log "licenses and notice written to ${doc}"
