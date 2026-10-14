#!/usr/bin/env bash
# 전용 디렉터리의 CUDA 런타임이 시스템 라이브러리 없이 libggml-cuda.so 에서 찾아지는지 확인한다.
#
#   verify-cuda-bundle.sh <dir that contains libggml-cuda.so and the bundled libraries>
#
# 동적 링커를 ld.so.cache 없이 실행해서(--inhibit-cache) 이 디렉터리 밖의 CUDA 라이브러리가 쓰이지 않음을 보인다.
# libcuda.so.1(드라이버)만 "not found" 여도 된다. GPU 드라이버는 이 패키지에 없다.
set -euo pipefail

die() { echo "verify-cuda-bundle: ERROR: $*" >&2; exit 1; }

dir="${1:-}"
[[ -d "${dir}" ]] || die "usage: $0 <dir>"
dir="$(cd "${dir}" && pwd -P)"
module="${dir}/libggml-cuda.so"
[[ -f "${module}" ]] || die "libggml-cuda.so is missing in ${dir}"

ldso=""
for c in /lib64/ld-linux-x86-64.so.2 /lib/ld-linux-aarch64.so.1 /lib/x86_64-linux-gnu/ld-linux-x86-64.so.2 /lib/aarch64-linux-gnu/ld-linux-aarch64.so.1; do
    if [[ -x "${c}" ]]; then ldso="${c}"; break; fi
done
[[ -n "${ldso}" ]] || die "no dynamic linker found"

out="$("${ldso}" --inhibit-cache --library-path "" --list "${module}" 2>&1 || true)"
echo "${out}"
for lib in libcudart.so.13 libcublas.so.13 libcublasLt.so.13; do
    path="$(awk -v s="${lib}" '$1 == s && $2 == "=>" {print $3; exit}' <<<"${out}")"
    [[ -n "${path}" && "${path}" != "not" ]] || die "${lib} was not resolved"
    resolved="$(readlink -f "${path}")"
    [[ "${resolved}" == "${dir}/"* ]] || die "${lib} resolved outside the private directory: ${resolved}"
done
others="$(grep -F 'not found' <<<"${out}" | grep -v -F 'libcuda.so.1' || true)"
[[ -z "${others}" ]] || die "unresolved libraries: ${others}"
echo "verify-cuda-bundle: ok (CUDA runtime resolved only inside ${dir})"
