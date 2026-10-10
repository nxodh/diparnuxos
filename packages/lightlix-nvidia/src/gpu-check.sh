#!/usr/bin/env bash
# GPU 와 드라이버 상태를 기록한다. 실패해도 부팅은 계속하며 CPU 폴백 대상으로 안내한다.
# 이 단계에는 추론 엔진이 없다. 종료 코드 0 은 GPU 준비, 1 은 CPU 폴백 대상이다.
set -euo pipefail
export LC_ALL=C
CONF="${LIGHTLIX_NVIDIA_CONF:-/usr/share/lightlix/nvidia.conf}"
SYSFS="${LIGHTLIX_SYSFS:-/sys}"
VERSION_FILE="${LIGHTLIX_NVIDIA_VERSION_FILE:-/proc/driver/nvidia/version}"
RUNTIME="${LIGHTLIX_RUNTIME_DIR:-/run/lightlix}"
NVIDIA_DRIVER_VERSION=""
if [[ -r "$CONF" ]]; then
    # shellcheck source=/dev/null
    . "$CONF"
fi
mkdir -p "$RUNTIME"

report() {
    local state="$1" message="$2" rc="$3"
    printf 'LIGHTLIX_GPU_STATUS=%s\n' "$state" > "$RUNTIME/gpu-status.new"
    chmod 644 "$RUNTIME/gpu-status.new"
    mv "$RUNTIME/gpu-status.new" "$RUNTIME/gpu-status"
    printf 'Lightlix GPU [%s]: %s\n' "$state" "$message"
    if [[ "$rc" != 0 ]]; then
        echo 'Lightlix: CPU 폴백 대상입니다. LLM 추론 엔진은 Phase 2에서 추가합니다.'
    fi
    exit "$rc"
}

secure_boot=unknown
if command -v mokutil >/dev/null 2>&1; then
    sb="$(mokutil --sb-state 2>&1 || true)"
    case "$sb" in
        *'SecureBoot enabled'*) secure_boot=enabled ;;
        *'SecureBoot disabled'*) secure_boot=disabled ;;
    esac
fi
printf 'Lightlix Secure Boot: %s\n' "$secure_boot"

# PCI 디스플레이 장치만 센다. NVIDIA 오디오/USB 장치를 GPU 로 오인하지 않는다.
gpu_count=0
for device in "$SYSFS"/bus/pci/devices/*; do
    [[ -r "$device/vendor" && -r "$device/class" ]] || continue
    [[ "$(cat "$device/vendor")" == 0x10de ]] || continue
    case "$(cat "$device/class")" in
        0x03*) gpu_count=$((gpu_count + 1)) ;;
    esac
done
if (( gpu_count == 0 )); then
    report no_gpu 'NVIDIA GPU가 없습니다.' 1
fi

if [[ ! -r "$VERSION_FILE" ]]; then
    if ! timeout 30 modprobe nvidia > "$RUNTIME/module-load" 2>&1; then
        cat "$RUNTIME/module-load" >&2
        if [[ "$secure_boot" == enabled ]]; then
            report secure_boot 'Secure Boot가 켜진 상태에서 모듈 로드에 실패했습니다. 라이브 ISO의 DKMS 키는 기기에 등록되어 있지 않습니다. UEFI 설정에서 Secure Boot를 끈 뒤 다시 부팅하세요. 자세한 원인은 journalctl -k에서 확인하세요. 기기별 MOK 등록은 후속 단계에서 구현합니다.' 1
        fi
        report module_unloaded 'NVIDIA 모듈을 로드하지 못했습니다. journalctl -k와 modinfo nvidia로 원인을 확인하세요.' 1
    fi
fi
[[ -r "$VERSION_FILE" ]] || report module_unloaded 'NVIDIA 모듈의 버전 파일이 없습니다.' 1
grep -q 'Open Kernel Module' "$VERSION_FILE" || report proprietary_module '오픈 커널 모듈이 아닙니다. 독점 모듈은 사용할 수 없습니다.' 1
if [[ -n "$NVIDIA_DRIVER_VERSION" ]] && ! grep -Fq "$NVIDIA_DRIVER_VERSION" "$VERSION_FILE"; then
    report version_mismatch '로드된 모듈이 이미지에 고정된 드라이버 버전과 다릅니다.' 1
fi
command -v nvidia-smi >/dev/null 2>&1 || report driver_missing 'nvidia-smi가 없습니다. 드라이버 패키지 구성을 확인하세요.' 1
if ! timeout 20 nvidia-smi --query-gpu=name,compute_cap,driver_version --format=csv,noheader,nounits > "$RUNTIME/gpu-query" 2> "$RUNTIME/gpu-query-error"; then
    cat "$RUNTIME/gpu-query-error" >&2
    if grep -qi 'driver/library version mismatch\|API mismatch' "$RUNTIME/gpu-query-error"; then
        report version_mismatch '커널 모듈과 사용자 공간 라이브러리 버전이 다릅니다. 같은 버전으로 복구한 뒤 재부팅하세요.' 1
    fi
    report query_failed 'nvidia-smi GPU 조회에 실패했습니다. journalctl -k를 확인하세요.' 1
fi

count=0
bad=0
while IFS=, read -r name cap version; do
    [[ -n "$name" ]] || continue
    name="$(printf '%s' "$name" | sed 's/^ *//;s/ *$//')"
    cap="${cap//[[:space:]]/}"
    version="${version//[[:space:]]/}"
    printf 'Lightlix GPU: %s compute_cap=%s driver=%s\n' "$name" "$cap" "$version"
    [[ -z "$NVIDIA_DRIVER_VERSION" || "$version" == "$NVIDIA_DRIVER_VERSION" ]] || report version_mismatch 'nvidia-smi가 보고한 드라이버 버전이 이미지 설정과 다릅니다.' 1
    case "$cap" in 8.6|8.9|12.0) ;; *) bad=$((bad + 1)) ;; esac
    [[ "$name" =~ GeForce\ RTX\ (30|40|50)[0-9][0-9] ]] || bad=$((bad + 1))
    count=$((count + 1))
done < "$RUNTIME/gpu-query"
(( count > 0 )) || report query_failed 'nvidia-smi가 GPU를 보고하지 않았습니다.' 1
(( bad == 0 )) || report unsupported_gpu '대상은 GeForce RTX 30/40/50이며 compute_cap은 8.6, 8.9, 12.0입니다. 감지된 GPU 중 대상 밖의 장치가 있습니다.' 1

# 향후 CUDA 와 Wayland 를 쓸 경로도 준비한다. GUI 기동 자체는 Phase 3에서 검증한다.
timeout 20 modprobe nvidia-uvm || report module_unloaded 'nvidia-uvm을 로드하지 못했습니다.' 1
timeout 20 modprobe nvidia-drm || report module_unloaded 'nvidia-drm을 로드하지 못했습니다.' 1
report ready 'NVIDIA 오픈 모듈과 대상 GPU를 확인했습니다. 실기 CUDA 추론과 화면 동작은 별도 검증이 필요합니다.' 0
