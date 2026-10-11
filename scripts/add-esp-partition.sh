#!/usr/bin/env bash
# 입력 ISO 에 EFI System Partition 을 GPT 파티션으로 덧붙인 새 ISO 를 만든다.
# 사용법: scripts/add-esp-partition.sh <입력.iso> <출력.iso>
#
# 방식 (근거는 build/README.md 의 "ESP 파티션" 절에 있다)
#   1. xorriso -report_el_torito 로 UEFI 부트 항목의 이미지 경로를 얻는다 (live-build 기준 /boot/grub/efi.img).
#   2. 그 이미지를 -append_partition 으로 덧붙이고 타입을 EFI System GUID 로 준다.
#      -boot_image any appended_part_as=gpt 가 덧붙인 파티션을 GPT 에 기록한다.
#   3. -boot_image any replay 가 기존 El Torito 항목을 그대로 다시 만든다. ISO 9660 트리는 복사된다.
#   4. 출력 ISO 를 다시 읽어 검증한다. 하나라도 어긋나면 출력을 지우고 0 이 아닌 코드로 끝난다.
#
# 입력 ISO 는 읽기만 한다. 임시 파일은 mktemp -d 아래에 두고 종료할 때 지운다.
set -euo pipefail

readonly EFI_SYSTEM_TYPE_GUID='C12A7328-F81F-11D2-BA4B-00A0C93EC93B'
readonly ESP_PARTITION_NUMBER=2
readonly SQUASHFS_ISO_PATH='/live/filesystem.squashfs'
readonly ESP_BOOTLOADER_MTOOLS_PATH='::/EFI/boot/bootx64.efi'

die() {
    echo "add-esp-partition: ERROR: $*" >&2
    exit 1
}

usage() {
    echo "usage: $0 <input.iso> <output.iso>" >&2
    exit 2
}

if [ "$#" -ne 2 ]; then
    usage
fi
IN="$1"
OUT="$2"

for cmd in xorriso sfdisk mdir mcopy cmp sha256sum awk sed dd stat mktemp; do
    command -v "${cmd}" >/dev/null 2>&1 || die "required command not found: ${cmd}"
done

if [ ! -f "${IN}" ] || [ ! -r "${IN}" ]; then
    die "input ISO is not a readable file: ${IN}"
fi
[ ! -e "${OUT}" ] || die "output already exists, refusing to overwrite: ${OUT}"
OUT_DIR="$(dirname -- "${OUT}")"
[ -d "${OUT_DIR}" ] || die "output directory does not exist: ${OUT_DIR}"

WORK="$(mktemp -d)"
OUT_CREATED=0

cleanup() {
    local rc=$?
    rm -rf -- "${WORK}"
    # 실패했는데 출력이 만들어졌다면 검증을 통과하지 못한 ISO 이므로 지운다.
    if [ "${rc}" -ne 0 ] && [ "${OUT_CREATED}" -eq 1 ]; then
        rm -f -- "${OUT}"
    fi
    exit "${rc}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 외부 명령의 출력은 로그 파일에 남긴다. 실패하면 로그를 보여 주고 중단한다.
run_logged() {
    local name="$1"
    shift
    if ! "$@" >"${WORK}/${name}.log" 2>&1; then
        cat "${WORK}/${name}.log" >&2
        die "${name} failed"
    fi
}

# -report_el_torito plain 로그에서 UEFI 부트 항목의 이미지 경로를 UEFI_PATH 에 넣는다.
# 항목은 "El Torito boot img : N  UEFI ..." 와 "El Torito img path : N  /path" 두 줄로 나뉘므로 번호로 잇는다.
UEFI_PATH=''
uefi_boot_path() {
    local report="$1"
    local -a nums paths
    mapfile -t nums < <(awk '$1 == "El" && $2 == "Torito" && $3 == "boot" && $4 == "img" && $5 == ":" && $7 == "UEFI" { print $6 }' "${report}")
    [ "${#nums[@]}" -eq 1 ] || die "expected exactly one UEFI El Torito entry in ${report}, found ${#nums[@]}"
    mapfile -t paths < <(awk -v n="${nums[0]}" '$1 == "El" && $2 == "Torito" && $3 == "img" && $4 == "path" && $5 == ":" && $6 == n { print $7 }' "${report}")
    [ "${#paths[@]}" -eq 1 ] || die "expected exactly one path for UEFI El Torito entry ${nums[0]}, found ${#paths[@]}"
    UEFI_PATH="${paths[0]}"
}

# -report_el_torito 로그의 "Volume id" 줄에서 볼륨 ID 를 VOLID 에 넣는다.
VOLID=''
read_volid() {
    local report="$1"
    local -a ids
    mapfile -t ids < <(sed -n "s/^Volume id *: '\(.*\)'\$/\1/p" "${report}")
    [ "${#ids[@]}" -eq 1 ] || die "expected exactly one volume id line in ${report}, found ${#ids[@]}"
    VOLID="${ids[0]}"
    [ -n "${VOLID}" ] || die "volume id is empty in ${report}"
}

# 1. 입력 ISO 의 UEFI 부트 이미지 경로와 볼륨 ID 를 얻는다.
run_logged report-in xorriso -indev "${IN}" -report_el_torito plain
uefi_boot_path "${WORK}/report-in.log"
EFI_IMG_PATH="${UEFI_PATH}"
[[ "${EFI_IMG_PATH}" == /* ]] || die "UEFI boot image path is not absolute: ${EFI_IMG_PATH}"
read_volid "${WORK}/report-in.log"
IN_VOLID="${VOLID}"
echo "add-esp-partition: input UEFI boot image: ${EFI_IMG_PATH}"
echo "add-esp-partition: input volume id: ${IN_VOLID}"

# 2. EFI 부트 이미지를 꺼낸다. 이 파일이 ESP 파티션의 내용이 된다.
EFI_IMG="${WORK}/efi.img"
run_logged extract-efi xorriso -osirrox on -indev "${IN}" -extract "${EFI_IMG_PATH}" "${EFI_IMG}"
[ -s "${EFI_IMG}" ] || die "extracted EFI boot image is empty: ${EFI_IMG_PATH}"

# 3. 출력 ISO 를 만든다. 입력 ISO 는 -indev 로만 읽는다.
echo "add-esp-partition: writing ${OUT}"
OUT_CREATED=1
run_logged build xorriso \
    -indev "${IN}" \
    -outdev "${OUT}" \
    -boot_image any replay \
    -boot_image any appended_part_as=gpt \
    -append_partition "${ESP_PARTITION_NUMBER}" "${EFI_SYSTEM_TYPE_GUID}" "${EFI_IMG}" \
    -changes_pending yes \
    -end
[ -s "${OUT}" ] || die "output ISO was not written: ${OUT}"

# 검증 (a): GPT 가 있고 EFI System 타입 파티션이 정확히 하나 있다.
run_logged sfdisk-dump sfdisk -d "${OUT}"
grep -qx 'label: gpt' "${WORK}/sfdisk-dump.log" || die "output ISO has no GPT"
mapfile -t esp_lines < <(grep "type=${EFI_SYSTEM_TYPE_GUID}," "${WORK}/sfdisk-dump.log" || true)
[ "${#esp_lines[@]}" -eq 1 ] || die "expected exactly one EFI System partition in output GPT, found ${#esp_lines[@]}"
read -r ESP_START ESP_SIZE < <(printf '%s\n' "${esp_lines[0]}" | sed -n 's/.* start= *\([0-9][0-9]*\), size= *\([0-9][0-9]*\), type=.*/\1 \2/p') || true
[[ "${ESP_START:-}" =~ ^[0-9]+$ ]] || die "could not read the start of the EFI System partition"
[[ "${ESP_SIZE:-}" =~ ^[0-9]+$ ]] || die "could not read the size of the EFI System partition"
echo "add-esp-partition: EFI System partition: start=${ESP_START} size=${ESP_SIZE} sectors (512 bytes)"

# 검증 (b): ESP 영역이 FAT 이고 EFI/boot/bootx64.efi 가 들어 있으며 원본 EFI 이미지와 같다.
ESP_IMG="${WORK}/esp.img"
run_logged extract-esp dd if="${OUT}" of="${ESP_IMG}" bs=512 skip="${ESP_START}" count="${ESP_SIZE}" status=none
EFI_IMG_BYTES="$(stat -c %s "${EFI_IMG}")"
[ "${EFI_IMG_BYTES}" -le "$(( ESP_SIZE * 512 ))" ] || die "EFI System partition is smaller than the EFI boot image"
cmp -s -n "${EFI_IMG_BYTES}" "${ESP_IMG}" "${EFI_IMG}" || die "EFI System partition content differs from the EFI boot image"
mdir -i "${ESP_IMG}" :: >/dev/null 2>&1 || die "EFI System partition is not a FAT filesystem"
mdir -b -i "${ESP_IMG}" ::/EFI/boot >"${WORK}/esp-boot-dir.txt" 2>/dev/null || die "EFI/boot directory not found in EFI System partition"
grep -qxF "${ESP_BOOTLOADER_MTOOLS_PATH}" "${WORK}/esp-boot-dir.txt" || die "${ESP_BOOTLOADER_MTOOLS_PATH} not found in EFI System partition"
mcopy -i "${ESP_IMG}" "${ESP_BOOTLOADER_MTOOLS_PATH}" "${WORK}/bootx64-esp.efi" || die "could not read bootx64.efi from EFI System partition"
mcopy -i "${EFI_IMG}" "${ESP_BOOTLOADER_MTOOLS_PATH}" "${WORK}/bootx64-src.efi" || die "could not read bootx64.efi from source EFI image"
cmp -s "${WORK}/bootx64-esp.efi" "${WORK}/bootx64-src.efi" || die "bootx64.efi differs between EFI System partition and source EFI image"
rm -f -- "${ESP_IMG}" "${WORK}/bootx64-esp.efi" "${WORK}/bootx64-src.efi"

# 검증 (c): 루트 파일시스템(squashfs)의 sha256 이 입력과 같다.
IN_SQUASHFS="${WORK}/in-filesystem.squashfs"
OUT_SQUASHFS="${WORK}/out-filesystem.squashfs"
run_logged extract-in-squashfs xorriso -osirrox on -indev "${IN}" -extract "${SQUASHFS_ISO_PATH}" "${IN_SQUASHFS}"
run_logged extract-out-squashfs xorriso -osirrox on -indev "${OUT}" -extract "${SQUASHFS_ISO_PATH}" "${OUT_SQUASHFS}"
IN_SQUASHFS_SHA="$(sha256sum "${IN_SQUASHFS}" | awk '{ print $1 }')"
OUT_SQUASHFS_SHA="$(sha256sum "${OUT_SQUASHFS}" | awk '{ print $1 }')"
if [ -z "${IN_SQUASHFS_SHA}" ] || [ "${IN_SQUASHFS_SHA}" != "${OUT_SQUASHFS_SHA}" ]; then
    die "${SQUASHFS_ISO_PATH} sha256 differs: input=${IN_SQUASHFS_SHA} output=${OUT_SQUASHFS_SHA}"
fi
echo "add-esp-partition: ${SQUASHFS_ISO_PATH} sha256 unchanged: ${OUT_SQUASHFS_SHA}"
rm -f -- "${IN_SQUASHFS}" "${OUT_SQUASHFS}"

# 검증 (d): El Torito UEFI 항목이 여전히 있고 같은 이미지를 가리킨다.
run_logged report-out xorriso -indev "${OUT}" -report_el_torito plain
uefi_boot_path "${WORK}/report-out.log"
[ "${UEFI_PATH}" = "${EFI_IMG_PATH}" ] || die "output UEFI El Torito path is '${UEFI_PATH}', expected '${EFI_IMG_PATH}'"

# 검증 (e): 볼륨 ID 가 입력과 같다.
read_volid "${WORK}/report-out.log"
[ "${VOLID}" = "${IN_VOLID}" ] || die "output volume id is '${VOLID}', expected '${IN_VOLID}'"

echo "add-esp-partition: ok: ${OUT}"
