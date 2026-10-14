#!/usr/bin/env bash
# llama.cpp 의 llama-server 를 고정 커밋에서 빌드하고 opereta-llm .deb 를 /out 에 만든다.
# 컨테이너 안에서 실행한다(scripts/build-llama-in-docker.sh 가 호출). 저장소는 /src 에 읽기 전용으로 마운트된다.
#
# 빌드는 한 벌이다. CUDA 백엔드는 GGML_BACKEND_DL=ON 으로 dlopen 되는 모듈(libggml-cuda.so)이 되므로,
# CUDA 라이브러리나 GPU 가 없는 시스템에서는 로드에 실패하고 CPU 백엔드로 돈다.
set -euo pipefail

SRC="${OPERETA_SRC:-/src}"
OUT="${OPERETA_OUT:-/out}"
WORK="${LLAMA_WORK:-/var/tmp/llama-build}"
LLAMA_CUDA="${LLAMA_CUDA:-ON}"
BUILD_KEY="${LLAMA_BUILD_KEY:-}"

die() {
    echo "llama/build: ERROR: $*" >&2
    exit 1
}

log() {
    echo "llama/build: $*"
}

case "${LLAMA_CUDA}" in
    ON | OFF) ;;
    *) die "LLAMA_CUDA must be ON or OFF (got: ${LLAMA_CUDA})" ;;
esac

[ -r "${SRC}/opereta.conf" ] || die "cannot read ${SRC}/opereta.conf"
# shellcheck source=/dev/null
. "${SRC}/opereta.conf"

for cmd in git cmake readelf ldd dpkg-buildpackage nproc; do
    command -v "${cmd}" >/dev/null 2>&1 || die "${cmd} is missing from the builder image"
done
ARCH="$(dpkg --print-architecture)"

# 1. 소스: 고정 커밋만 받는다. 자동 생성 tarball 은 해시가 고정되지 않으므로 쓰지 않는다.
rm -rf "${WORK}"
SRCDIR="${WORK}/llama.cpp"
mkdir -p "${SRCDIR}"
git -C "${SRCDIR}" init --quiet
git -C "${SRCDIR}" fetch --depth 1 --quiet "${LLAMA_CPP_REPO}" "${LLAMA_CPP_COMMIT}"
git -C "${SRCDIR}" -c advice.detachedHead=false checkout --quiet FETCH_HEAD
ACTUAL_COMMIT="$(git -C "${SRCDIR}" rev-parse HEAD)"
[ "${ACTUAL_COMMIT}" = "${LLAMA_CPP_COMMIT}" ] || die "fetched commit ${ACTUAL_COMMIT} differs from LLAMA_CPP_COMMIT ${LLAMA_CPP_COMMIT}"
log "source: ${LLAMA_CPP_REPO} ${LLAMA_CPP_TAG} at ${ACTUAL_COMMIT}"

# 2. CMake 옵션.
# GGML_NATIVE=OFF 는 항상 명시한다. 기본값은 ON(-march=native)이고, SOURCE_DATE_EPOCH 가 있으면 OFF 로 바뀌어 SIMD 가 꺼진다.
# 웹 UI 는 끈다. 기본 설정은 빌드 중 네트워크에서 dist.tar.gz 를 내려받는다.
CMAKE_OPTS=(
    -DCMAKE_BUILD_TYPE=Release
    -DGGML_NATIVE=OFF
    -DGGML_BACKEND_DL=ON
    -DGGML_CPU_ALL_VARIANTS=ON
    -DLLAMA_BUILD_TESTS=OFF
    -DLLAMA_BUILD_EXAMPLES=OFF
    -DLLAMA_BUILD_APP=OFF
    -DLLAMA_BUILD_UI=OFF
    -DLLAMA_USE_PREBUILT_UI=OFF
    -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON
    "-DCMAKE_INSTALL_RPATH=\$ORIGIN"
)
if [ "${LLAMA_CUDA}" = ON ]; then
    NVCC_PATH="$(command -v nvcc || true)"
    [ -n "${NVCC_PATH}" ] || die "LLAMA_CUDA=ON but nvcc is not on PATH"
    export CUDACXX="${NVCC_PATH}"
    CMAKE_OPTS+=(
        -DGGML_CUDA=ON
        '-DCMAKE_CUDA_ARCHITECTURES=86-real;89-real;120a-real'
    )
fi

# 3. 빌드. llama-server 타깃만 만든다. cmake --install 은 부분 빌드에서 실패하므로 쓰지 않는다.
BUILD_DIR="${SRCDIR}/build"
cmake -S "${SRCDIR}" -B "${BUILD_DIR}" "${CMAKE_OPTS[@]}"
cmake --build "${BUILD_DIR}" --target llama-server -j"$(nproc)"

BIN="${BUILD_DIR}/bin"
[ -x "${BIN}/llama-server" ] || die "llama-server was not built"

# 4. 모으기. 패키지 루트 아래 LLAMA_INSTALL_DIR 에 실행 파일과 모든 .so 를 평평하게 둔다.
# 백엔드 모듈은 실행 파일 옆에서만 찾으므로 하위 디렉터리를 만들지 않는다. 심볼릭 링크는 보존한다.
PKGROOT="${WORK}/pkgroot"
STAGE="${PKGROOT}${LLAMA_INSTALL_DIR}"
mkdir -p "${STAGE}"
cp -P "${BIN}/llama-server" "${STAGE}/"
shopt -s nullglob
for f in "${BIN}"/*.so*; do
    cp -P "${f}" "${STAGE}/"
done
shopt -u nullglob

shopt -s nullglob
CPU_MODULES=("${STAGE}"/libggml-cpu-*.so)
shopt -u nullglob
[ "${#CPU_MODULES[@]}" -ge 1 ] || die "no libggml-cpu-*.so backend module was staged"

DOC="${PKGROOT}/usr/share/doc/opereta-llm"
mkdir -p "${DOC}"
cp "${SRCDIR}/LICENSE" "${DOC}/LICENSE.llama.cpp"
bash "${SRC}/llama/copy-vendor-licenses.sh" "${SRCDIR}" "${DOC}"

# CUDA 런타임(cudart, cublas)은 이 패키지에 넣지 않는다. NVIDIA CUDA EULA 는 그 파일들을 응용 프로그램 전용으로만
# 재배포하게 하므로, 이미지는 재배포하지 않고 사용자의 컴퓨터가 NVIDIA 저장소에서 직접 받아 설치한다
# (opereta gpu runtime install, docs/licenses.md 2.3, 10.5). 런타임이 없으면 libggml-cuda.so 는 적재되지 않고
# CPU 백엔드로 돈다.

# 5. 검증. 하나라도 어긋나면 실패한다.
# (a) 실행 파일과 모든 ELF 의 RUNPATH 가 $ORIGIN 이다. 빌드 경로가 남아 있으면 안 된다.
verify_runpath() {
    local f="$1" rp
    rp="$(readelf -d "${f}" | grep -E '\((RUNPATH|RPATH)\)' || true)"
    [ -n "${rp}" ] || die "no RUNPATH in ${f}"
    grep -Fq "[\$ORIGIN]" <<<"${rp}" || die "RUNPATH of ${f} is not \$ORIGIN: ${rp}"
}
ELF_COUNT=0
for f in "${STAGE}"/*; do
    if [ ! -f "${f}" ] || [ -L "${f}" ]; then
        continue
    fi
    readelf -h "${f}" >/dev/null 2>&1 || continue
    verify_runpath "${f}"
    ELF_COUNT=$((ELF_COUNT + 1))
done
[ "${ELF_COUNT}" -ge 2 ] || die "expected llama-server and shared objects, found ${ELF_COUNT} ELF files"
log "check (a): RUNPATH is \$ORIGIN for ${ELF_COUNT} ELF files"

# (b) CUDA 빌드면 libggml-cuda.so 가 있고 CUDA 13 런타임과 cuBLAS 에 링크되어 있다. CPU 빌드면 모듈이 없어야 한다.
if [ "${LLAMA_CUDA}" = ON ]; then
    [ -f "${STAGE}/libggml-cuda.so" ] || die "libggml-cuda.so was not built"
    NEEDED="$(readelf -d "${STAGE}/libggml-cuda.so" | grep NEEDED || true)"
    for lib in libcudart.so.13 libcublas.so.13; do
        grep -Fq "[${lib}]" <<<"${NEEDED}" || die "libggml-cuda.so does not need ${lib}"
    done
    log "check (b): libggml-cuda.so needs libcudart.so.13 and libcublas.so.13"
else
    [ ! -e "${STAGE}/libggml-cuda.so" ] || die "CUDA is off but libggml-cuda.so is present"
    log "check (b): CUDA is off, no libggml-cuda.so"
fi

# 빌드 디렉터리를 지운 뒤 다른 경로로 복사해서 실행한다. 빌드 트리에 기대는 실행 파일이 있으면 여기서 실패한다.
rm -rf "${BUILD_DIR}"
RELOC="${WORK}/relocated"
mkdir -p "${RELOC}"
cp -a "${STAGE}/." "${RELOC}/"

# (c) 복사한 트리에서 --version 이 0 으로 끝난다.
VERSION_OUT="$("${RELOC}/llama-server" --version 2>&1)" || die "llama-server --version failed in the relocated tree"
log "check (c): llama-server --version exits 0: $(head -n 1 <<<"${VERSION_OUT}")"

# (d) --list-devices 가 0으로 끝나고 장치 목록 머리글을 출력한다. 빌드 컨테이너에는 GPU 가 없다.
DEVICES_OUT="$("${RELOC}/llama-server" --list-devices 2>&1)" || die "llama-server --list-devices failed"
grep -Fq 'Available devices:' <<<"${DEVICES_OUT}" || die "--list-devices did not print 'Available devices:'"
log "check (d): --list-devices exits 0 and prints 'Available devices:'"

# (e) 스테이징 트리에 컴파일러, 정적 라이브러리, 헤더가 없다.
BAD_FILES="$(find "${PKGROOT}" \( -name nvcc -o -name '*.a' -o -name '*.h' -o -name '*.hpp' -o -name '*.cuh' \) -print)"
[ -z "${BAD_FILES}" ] || die "development files in the staged tree: ${BAD_FILES}"
log "check (e): no nvcc, static libraries or headers in the staged tree"

# 빌드 정보: 커밋, CMake 옵션, 도구 버전, 그리고 ldd 와 readelf 출력.
INFO="${DOC}/build-info.txt"
{
    echo "opereta-llm build-info"
    echo "built_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "opereta_version=${OPERETA_VERSION}"
    echo "llama_cpp_repo=${LLAMA_CPP_REPO}"
    echo "llama_cpp_tag=${LLAMA_CPP_TAG}"
    echo "llama_cpp_commit=${ACTUAL_COMMIT}"
    echo "llama_cuda=${LLAMA_CUDA}"
    echo "architecture=${ARCH}"
    echo "build_key=${BUILD_KEY}"
    echo "cmake_options:"
    printf '  %s\n' "${CMAKE_OPTS[@]}"
    echo "cmake=$(cmake --version | head -n 1)"
    echo "compiler=$(g++ --version | head -n 1)"
    echo "llama-server --version: ${VERSION_OUT}"
    if [ "${LLAMA_CUDA}" = ON ]; then
        echo "nvcc=$(nvcc --version | tail -n 1)"
        echo "cuda packages:"
        dpkg-query -W -f='  ${Package} ${Version}\n' 'cuda-*' 'libcublas*' 'libnvvm*' 2>/dev/null || true
    fi
    echo "--- readelf -d and ldd (build container) ---"
    for f in "${STAGE}"/*; do
        if [ ! -f "${f}" ] || [ -L "${f}" ]; then
        continue
    fi
        readelf -h "${f}" >/dev/null 2>&1 || continue
        echo "## $(basename "${f}")"
        readelf -d "${f}"
        ldd "${f}" || true
    done
} > "${INFO}"

# 6. 패키지. llama/pkg/debian 을 임시 소스 트리로 복사하고 버전과 의존성을 채운다.
PKGSRC="${WORK}/pkg/opereta-llm-${OPERETA_VERSION}"
mkdir -p "${PKGSRC}"
cp -a "${SRC}/llama/pkg/debian" "${PKGSRC}/debian"

if [ "${LLAMA_CUDA}" = ON ]; then
    NVIDIA_DEP=", opereta-nvidia"
else
    NVIDIA_DEP=""
fi
sed -i "s/@OPERETA_NVIDIA_DEPENDS@/${NVIDIA_DEP}/" "${PKGSRC}/debian/control"
sed -i "s|@OPERETA_HOME_URL@|${OPERETA_HOME_URL}|g" "${PKGSRC}/debian/control"

{
    cat "${SRC}/llama/pkg/debian/copyright"
    echo
    echo "# Upstream LICENSE of llama.cpp ${LLAMA_CPP_TAG} (commit ${ACTUAL_COMMIT}), reproduced verbatim:"
    sed -e 's/^$/ ./' -e 's/^/ /' "${SRCDIR}/LICENSE"
} > "${PKGSRC}/debian/copyright"

cat > "${PKGSRC}/debian/changelog" <<EOF
opereta-llm (${OPERETA_VERSION}) trixie; urgency=medium

  * Build llama-server from llama.cpp ${LLAMA_CPP_TAG} (${ACTUAL_COMMIT}).

 -- Opereta Developers <noreply@users.noreply.github.com>  $(date -R -u)
EOF

cp -a "${PKGROOT}" "${PKGSRC}/staging"

(cd "${PKGSRC}" && dpkg-buildpackage -us -uc -b)

DEB_NAME="opereta-llm_${OPERETA_VERSION}_${ARCH}.deb"
DEB="${WORK}/pkg/${DEB_NAME}"
[ -f "${DEB}" ] || die "dpkg-buildpackage did not produce ${DEB_NAME}"

mkdir -p "${OUT}"
rm -f "${OUT}/BUILD_KEY" "${OUT}"/opereta-llm_*.deb
mv "${DEB}" "${OUT}/${DEB_NAME}"
printf '%s\n' "${BUILD_KEY}" > "${OUT}/BUILD_KEY"
log "wrote ${OUT}/${DEB_NAME}"
