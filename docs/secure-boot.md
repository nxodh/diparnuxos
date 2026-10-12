# Secure Boot와 커널 모듈 서명

- 확인 날짜: 2026-10-11
- 상태: Phase 1 빌드 키 제거와 텍스트 안내, Phase 3 `lightlix-mok` 보조 도구 구현(실기 미검증)

> Phase 1은 `9900-lightlix-nvidia.hook.chroot`에서 빌드 키를 제거하고 완성된 ISO를 다시 검사한다. `lightlix-gpu-check`가 Secure Boot 상태와 모듈 로드 실패를 영어 ASCII 텍스트로 콘솔과 로그인 프롬프트 위에 안내한다(실기 화면은 미검증). 현재 라이브 ISO에서 NVIDIA를 사용하려면 Secure Boot를 끈다. 기기별 키 생성·재서명·등록 화면은 후속 단계에서 구현한다.

Lightlix는 UEFI 전용이다. Secure Boot가 켜진 기기에서 부팅하는 경우를 고려해야 한다. 이 문서는 커널 설정, 서명 키, 기기별 등록 흐름, 대안, 라이브 세션 안내를 정리한다. 방침 중 일부는 사용자가 아직 확정하지 않았다(아래 7절).

---

## 1. Debian 커널의 lockdown과 모듈 서명 요구

Debian 13 커널 6.12.111(security 저장소 기준)의 설정은 다음과 같다. 출처는 `linux-headers-6.12.111+deb13-amd64` 패키지의 `.config`다(검증 기록 check 29).

- `CONFIG_MODULE_SIG=y`: 모듈 서명 검증 기능이 켜져 있다. 서명 강제는 아래 lockdown이 걸릴 때 적용된다.
- `CONFIG_SECURITY_LOCKDOWN_LSM=y`: lockdown LSM을 쓴다.
- `CONFIG_LOCK_DOWN_IN_EFI_SECURE_BOOT=y`: Secure Boot가 켜지면 lockdown이 자동으로 걸린다.

결과: Secure Boot가 켜진 상태에서는 **신뢰되지 않은 키로 서명된 모듈이 로드되지 않는다.** DKMS로 빌드한 NVIDIA 모듈도 같다. 신뢰된 키로 서명하거나, 서명 강제를 끄는 방법만 있다.

- Secure Boot 켜짐 + 신뢰되지 않은 모듈: 로드 거부.
- RTX 50(Blackwell)은 오픈 커널 모듈만 지원한다(595.91.07 README). 대안 모듈이 없으므로 Secure Boot가 켜져 있으면 RTX 50에서 NVIDIA 드라이버가 올라오지 않는다.

근거: 검증 기록 check 29. https://security.debian.org/debian-security/pool/updates/main/l/linux/linux-headers-6.12.111+deb13-amd64_6.12.111-1_amd64.deb . 라이브 빌드의 Secure Boot 설정은 `--uefi-secure-boot auto`가 기본이다(검증 기록 check 29, live-build 설정 설명).

---

## 2. chroot DKMS 빌드가 만드는 서명 키를 ISO에서 제거

### 문제

DKMS는 서명 키가 없으면 자동으로 만든다.

- 키 파일 위치: `/var/lib/dkms/mok.key`(개인키), `/var/lib/dkms/mok.pub`(공개키).
- 생성 명령(DKMS 내부): `openssl req -new -x509 -nodes -days 36500 -subj "/CN=DKMS module signing key"` 형태다(검증 기록 check 29가 확인한 스크립트).

이미지를 chroot에서 빌드하면 DKMS가 이 키를 **이미지 빌드 중에 한 번** 만든다. 그 이미지를 받은 모든 사용자가 같은 개인키를 갖게 된다. 개인키가 공개되면 누구나 같은 키로 서명한 모듈을 만들 수 있다. 그러므로 이미지에 이 키를 남기지 않는다.

### 조치 (Phase 1)

- 이미지 빌드 마지막 단계에서 `/var/lib/dkms/mok.key`와 `/var/lib/dkms/mok.pub`을 지운다.
- 키를 지운 뒤에도 DKMS가 다시 키를 만들지 않도록 확인한다. 빌드 검증에서 이 두 파일이 없는지 확인한다.
- 설치된 시스템에서 키를 새로 만드는 흐름은 3절에서 다룬다.

### 확인이 필요한 것

- DKMS 설정 파일에서 키 경로를 바꾸는 옵션이 있는지는 확인하지 않았다. 현재 방식은 빌드 뒤 두 파일을 지우는 것이다. 최종 ISO의 squashfs와 initrd에 키 파일이 없는지는 `tests/nvidia/image.sh`가 검사한다.
- DKMS 키 자동 생성 동작의 출처: https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md ("Private key and certificate are auto generated the first time DKMS is run and placed in /var/lib/dkms").

---

## 3. 기기별 MOK 키 생성과 등록 흐름 (Phase 3 구현, 실기 미검증)

MOK(Machine Owner Key)는 기기 소유자가 UEFI에 등록하는 키다. 등록된 키로 서명한 모듈은 Secure Boot가 켜진 상태에서도 로드된다.

`lightlix-mok`(`/usr/sbin/lightlix-mok`, `lightlix-nvidia` 패키지)가 아래 흐름을 맡는다. 설치된 시스템에서 root로 `lightlix-mok enroll`을 실행한다.

1. **키는 DKMS가 이 기기에서 만든다.** 이미지에서 빌드 키를 지웠으므로 DKMS가 처음 서명할 때 `/var/lib/dkms/mok.key`, `mok.pub`을 새로 만든다(DKMS 3.2.2 `framework.conf`: 파일이 없으면 둘 다 다시 만든다). 키는 기기마다 다르다.
2. **NVIDIA 모듈을 그 키로 다시 빌드하고 설치한다.** `dkms status`의 `nvidia*` 항목마다 현재 커널용으로 `dkms build --force`와 `dkms install --force`를 실행한다.
3. **공개키를 MOK로 가져오기 예약한다.** `mokutil --import /var/lib/dkms/mok.pub`. 일회용 비밀번호를 사용자가 정한다.
4. **재부팅 후 MokManager에서 등록한다.** "Enroll MOK", "Continue", 비밀번호 입력, 재부팅 순서를 안내한다. 도구가 끝에 같은 문구를 출력한다.

`lightlix-mok status`는 `secure_boot`, `machine_key`(`missing`, `enrolled`, `not_enrolled`, `unknown`), `session`(`live`, `installed`)을 한 줄씩 출력한다.

**라이브 세션에서는 거절한다.** 라이브 세션은 재부팅하면 만든 키가 사라진다. 펌웨어에 등록된 MOK는 남지만 개인키가 없어 새 모듈을 서명할 수 없다. 따라서 이 흐름은 설치기(Phase 4)가 생긴 뒤의 설치된 시스템에서만 의미가 있다.

### 확인한 것과 하지 못한 것

- 확인: 도구의 분기(라이브 거절, root 요구, Secure Boot 꺼짐, 이미 등록됨, 빌드·설치·가져오기 순서, 빌드 실패, 키 없음, NVIDIA 모듈 없음)를 명령 스텁으로 검사한다(`tests/nvidia/mok.sh`).
- 확인하지 못함: 실제 `mokutil --import`와 MokManager 화면 동작, 제조사별 UEFI 차이, 재부팅 뒤 모듈 로드. QEMU의 OVMF로 Secure Boot 켜짐 부팅은 만들 수 있으나 MokManager 상호작용 자동화는 이번에 하지 않았다. 실기 또는 설치기가 있는 환경에서 확인해야 한다.
- 조사 기록: NVIDIA 드라이버 README는 "a module signed by a newly generated key cannot be loaded into a kernel that requires signed modules until its key is trusted"라고 적는다(검증 기록 check 29가 인용).

---

## 4. Secure Boot를 끄는 대안

기기별 MOK 흐름이 복잡하므로 v1에서는 다음 대안을 쓸 수 있다.

- 사용자가 BIOS/UEFI 설정에서 Secure Boot를 끈다.
- 설치기나 첫 부팅 화면에서 Secure Boot가 켜져 있으면 끄는 방법을 텍스트로 안내한다.
- 서명 강제를 끄는 것(커널 명령줄 등)은 쓰지 않는다. lockdown을 우회하는 방법이며, 보안 모델을 깨뜨린다.

장단점:

- 장점: 구현이 단순하고, DKMS 키 문제가 줄어든다.
- 단점: 사용자가 기기 설정을 직접 바꿔야 한다. 일부 기기에서는 끄는 메뉴를 찾기 어렵다.

이 방식을 택하면 "Secure Boot를 끄는 것을 요구한다"는 조건을 README와 설치 안내에 적는다.

---

## 5. 라이브 세션: Secure Boot가 켜진 RTX 50

- RTX 50은 오픈 커널 모듈만 쓸 수 있다.
- Secure Boot가 켜진 라이브 세션에서는 NVIDIA 모듈이 서명 검증을 통과하지 못한다.
- 그러면 RTX 50에서 드라이버가 올라오지 않고 DRM 장치가 생기지 않는다. 6.12 커널에는 simpledrm이 없고 nouveau는 Blackwell을 모른다(decisions.md D8). 그래서 화면이 나오지 않는다.
- **구현(Phase 1)**: 화면 대신 텍스트로 안내한다. `lightlix-gpu-check`가 진단 결과를 영어 ASCII로 쓰고, 같은 내용을 콘솔(`StandardOutput=journal+console`)과 로그인 프롬프트 위(`/run/issue.d/50-lightlix-gpu.issue`)에 낸다. 로그인 뒤에는 `/run/motd.d/50-lightlix-gpu`에도 나온다.
  - Secure Boot가 켜져 있고 modprobe 오류가 `Key was rejected by service`이면 `secure_boot`(종료 2)이다. 문구는 UEFI 설정에서 Secure Boot를 끄고 다시 부팅하라고 적는다.
  - 그 밖의 모듈 로드 실패는 `module_unloaded`(종료 2)이다. Secure Boot가 켜져 있었다면 메시지에 그 사실을 덧붙인다.
  - 판정 조건과 상태 파일 키는 [drivers.md](drivers.md)의 "GPU 진단 계약" 절에 있다.
- **확인 상태**: 리뷰 시점의 구현(이전 스크립트)은 QEMU/OVMF에서 Secure Boot를 켠 채 ISO가 부팅됐고, `modprobe nvidia`가 `Key was rejected by service`로 거부됐다. 이 확인은 QEMU에서 이뤄졌고 실기 펌웨어에서는 미검증이다. 이번 구현(영어 문구, issue/motd 출력, 판정 조건 변경)은 QEMU에서 아직 확인하지 않았다.

조사 근거: 검증 기록 check 28(드라이버 없이 RTX 50 화면 없음)과 check 29(Secure Boot에서 서명 강제). GUI 안내 화면은 Phase 3이다.

직렬 콘솔은 `console=ttyS0,115200`으로 열린다. 이 설정의 세부는 빌드 스크립트와 `tests/qemu` 쪽 문서에서 다룬다.

---

## 6. 검증 항목

Phase 1 및 Phase 3 수용 기준:

1. 이미지에 `/var/lib/dkms/mok.key`, `/var/lib/dkms/mok.pub`이 없다.
2. Secure Boot가 꺼진 상태에서 NVIDIA 모듈이 로드된다(RTX 30, 40, 50 각각).
3. Secure Boot가 켜진 상태에서 영어 안내가 콘솔과 로그인 프롬프트 위에 나온다(RTX 50). 이번 구현은 QEMU/OVMF에서 아직 확인하지 않았고, 실기 화면은 미검증이다.
4. 기기별 MOK 등록 흐름을 한 번 끝까지 실행해 모듈이 로드된다(Phase 3).
5. 커널 업데이트 후 DKMS 재빌드와 서명이 된다.

---

## 7. 열린 결정

- 현재 라이브 ISO는 Secure Boot를 끄는 흐름을 사용한다. 기기별 MOK 등록을 제공하는 후속 단계의 방침은 해당 단계에서 정한다.
- Phase 1 CI에서 최종 ISO의 DKMS 키 부재를 확인했다([검증 기록](verification-phase-1.md)). 기기별 등록과 Secure Boot 상태의 실기 모듈 로드는 미검증이다.
- `mokutil` 옵션과 등록 화면 동작 확인(3절).
- 기기 제조사별 UEFI 차이(3절).
