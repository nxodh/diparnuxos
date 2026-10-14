#!/bin/sh
# llama 실행 사용자로 장치 권한을 검사한다. 장치가 없으면 CPU 경로를 허용한다.
set -eu
# 명시적인 CPU 계획은 존재하는 GPU 장치 권한에 의존하지 않는다.
[ "${LLAMA_ARG_N_GPU_LAYERS:-}" = 0 ] && exit 0
found=0
check_device() {
    device="$1"
    if [ ! -r "$device" ] || [ ! -w "$device" ]; then
        echo "Opereta GPU permissions: read/write access denied: $device" >&2
        exit 1
    fi
}
# 데몬이 선택한 숫자 인덱스만 검사한다. 선택하지 않은 GPU의 권한으로 막지 않는다.
if [ -n "${CUDA_VISIBLE_DEVICES:-}" ]; then
    remaining="${CUDA_VISIBLE_DEVICES},"
    while [ -n "$remaining" ]; do
        index="${remaining%%,*}"
        remaining="${remaining#*,}"
        case "$index" in
            ''|*[!0-9]*) echo "Opereta GPU permissions: invalid device index: $index" >&2; exit 1 ;;
        esac
        check_device "/dev/nvidia$index"
        found=1
    done
else
    for device in /dev/nvidia[0-9]*; do
        [ -e "$device" ] || continue
        check_device "$device"
        found=1
    done
fi
[ "$found" -eq 1 ] || exit 0
for device in /dev/nvidiactl /dev/nvidia-uvm; do
    if [ ! -r "$device" ] || [ ! -w "$device" ]; then
        echo "Opereta GPU permissions: device missing or read/write access denied: $device" >&2
        exit 1
    fi
done
