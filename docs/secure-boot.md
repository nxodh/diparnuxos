# Secure Boot와 커널 모듈 서명

- 확인 날짜: 2026-10-11
- 상태: Phase 1 빌드 키 제거와 텍스트 안내 구현. 기기별 MOK 등록은 미구현

> Phase 1은 `9900-lightlix-nvidia.hook.chroot`에서 빌드 키를 제거하고 완성된 ISO를 다시 검사한다. `lightlix-gpu-check`가 Secure Boot 상태와 모듈 로드 실패를 텍스트로 안내한다. 현재 라이브 ISO에서 NVIDIA를 사용하려면 Secure Boot를 끈다. 기기별 키 생성·재서명·등록 화면은 후속 단계에서 구현한다.

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

- DKMS 설정 파일에서 키 경로를 바꾸는 옵션이 있는지는 이번 조사에서 확인하지 않았다. Phase 1에서 DKMS 문서로 확인한다.
- DKMS 키 자동 생성 동작의 출처: https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md ("Private key and certificate are auto generated the first time DKMS is run and placed in /var/lib/dkms").

---

## 3. 기기별 MOK 키 생성과 등록 흐름 (Phase 3 예정)

MOK(Machine Owner Key)는 기기 소유자가 UEFI에 등록하는 키다. 등록된 키로 서명한 모듈은 Secure Boot가 켜진 상태에서도 로드된다.

흐름 (초안):

1. **설치 시 또는 첫 부팅 시 기기별 키를 만든다.** 키는 그 기기 안에서만 만들고 이미지에 넣지 않는다.
2. **DKMS로 NVIDIA 모듈을 그 키로 다시 서명한다.** DKMS가 그 키를 쓰도록 설정한다. 설정 방식은 Phase 3에서 확인한다.
3. **공개키를 MOK 목록에 등록한다.** 검증자는 `mokutil --import`로 등록하는 흐름을 제안했다(검증 기록 decision_changes 2). 등록하려면 재부팅 후 UEFI 화면에서 등록 요청을 확인해야 한다. 이 확인 절차를 사용자에게 안내한다.
4. **재부팅 후 등록을 확인하고 모듈을 로드한다.**

조사 기록: NVIDIA 드라이버 README는 "a module signed by a newly generated key cannot be loaded into a kernel that requires signed modules until its key is trusted"라고 적는다. 즉 새 키로 서명한 모듈은 그 키를 신뢰하기 전까지 로드되지 않는다(검증 기록 check 29가 인용, URL은 검증 기록에 없음).

### 확인이 필요한 것

- `mokutil`의 명령 옵션과 등록 화면 동작은 이번 조사에서 확인하지 않았다. `mokutil` 패키지는 trixie main 0.7.2-1로 있다(https://api.ftp-master.debian.org/madison?package=mokutil&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on ). man 페이지로 옵션을 확인한 뒤 Phase 3에서 쓴다.
- 등록 절차 중 기기 제조사별 UEFI 차이는 확인하지 않았다.

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
- **방침(초안)**: 화면 대신 텍스트 콘솔에 안내를 띄운다. 안내 내용은 다음과 같다.
  - Secure Boot가 켜져 있어 NVIDIA 드라이버를 올릴 수 없다는 사실.
  - BIOS/UEFI에서 Secure Boot를 끄는 방법.
  - 시리얼 콘솔 또는 텍스트 콘솔로 상태를 확인할 수 있다는 사실.

조사 근거: 검증 기록 check 28(드라이버 없이 RTX 50 화면 없음)과 check 29(Secure Boot에서 서명 강제). 텍스트 안내는 Phase 1에서 구현했다. GUI 안내 화면은 Phase 3이다.

직렬 콘솔은 `console=ttyS0,115200`으로 열린다. 이 설정의 세부는 빌드 스크립트와 `tests/qemu` 쪽 문서에서 다룬다.

---

## 6. 검증 항목

Phase 1 및 Phase 3 수용 기준:

1. 이미지에 `/var/lib/dkms/mok.key`, `/var/lib/dkms/mok.pub`이 없다.
2. Secure Boot가 꺼진 상태에서 NVIDIA 모듈이 로드된다(RTX 30, 40, 50 각각).
3. Secure Boot가 켜진 상태에서 텍스트 안내가 나온다(RTX 50).
4. 기기별 MOK 등록 흐름을 한 번 끝까지 실행해 모듈이 로드된다(Phase 3).
5. 커널 업데이트 후 DKMS 재빌드와 서명이 된다.

---

## 7. 열린 결정

- 현재 라이브 ISO는 Secure Boot를 끄는 흐름을 사용한다. 기기별 MOK 등록을 제공하는 후속 단계의 방침은 해당 단계에서 정한다.
- DKMS 키 제거 후 재생성 방지 방법은 Phase 1에서 확인한다(2절).
- `mokutil` 옵션과 등록 화면 동작 확인(3절).
- 기기 제조사별 UEFI 차이(3절).
