# 업데이트와 롤백

- 확인 날짜: 2026-10-14(0.6.0: OS 업데이트 채널 추가)
- 상태: 구현됨. 가짜 저장소에서 실제 apt 와 dpkg 로 흐름을 확인했고, 설치된 시스템에서 상태 조회와 확인(`--check`)은 QEMU 에서 확인했다. **실제 Debian 커널 갱신과 실기 롤백은 미검증이다.**

```sh
opereta update --check      # 무엇이 바뀌는지만 본다
opereta update              # 설치한다. 끝나면 재부팅을 안내한다
opereta update --reboot     # 성공하면 바로 재부팅한다
opereta update --status     # 마지막 갱신의 상태
opereta update --rollback   # 마지막 갱신 전의 커널과 드라이버로 되돌린다
```

Control Center 의 시스템 페이지에도 같은 동작이 있다. 라이브 세션에서는 쓸 수 없다(`live_session`). 자동으로 설치하는 것은 없다. `unattended-upgrades` 는 설치하지 않고 커널과 드라이버 패키지는 hold 되어 있다.

## OS 업데이트(Opereta 의 새 버전)

0.6.0 부터 설치된 시스템이 Opereta 의 새 릴리스를 받는다. 릴리스 자산이 서명된 APT 저장소이고(평평한 형식, [releases.md](releases.md)), 이미지의 `opereta-archive-keyring` 이 그 저장소를 소스로 켜 둔다([apt-repository.md](apt-repository.md)). 새 버전을 받는 흐름은 아래의 기존 갱신과 같다.

```sh
opereta update --check
# 12 package changes
# opereta: 0.6.0 -> 0.6.1
# run 'opereta update' to install
opereta update
```

- **OS 버전**은 `opereta-base` 의 버전이다. `opereta update --status` 가 설치된 버전과 채널 상태를 보여 준다. Control Center 의 시스템 페이지는 "Opereta 0.6.1 업데이트가 있습니다" 로 알린다.
- **채널이 꺼져 있을 때**: 이미지가 저장소 주소나 서명 키 없이 만들어졌으면 소스가 `Enabled: no` 다. 이때 확인이 "최신"이라고 해도 Debian 과 NVIDIA 저장소만 본 것이다. `opereta update --check` 가 "update channel is off" 를 알리고 Control Center 도 경고로 보여 준다.
- **서명 검증**: 저장소 색인은 `Signed-By` 의 공개 키로만 검증된다. 변조된 색인이나 다른 키로 서명한 색인은 apt 가 거절한다(`make test-apt-repo-docker`).
- **정식 릴리스만**: 저장소 주소는 "최신 정식 릴리스"를 가리킨다. prerelease 는 채널에 나가지 않는다.
- **OS 버전만 바뀐 갱신**(커널과 드라이버가 같다)은 재부팅이 필요 없다. 데몬과 Control Center 는 패키지 갱신 뒤 다시 시작된다.
- **롤백의 한계**: 평평한 저장소에는 이전 버전이 없다. 이번 갱신에서 드라이버 정책 패키지(`opereta-nvidia`)가 바뀌었는데 이전 버전을 받을 수 없으면 `--rollback` 은 `rollback_unavailable` 로 거절하고 아무것도 바꾸지 않는다. GRUB 메뉴에서 이전 커널을 고르는 길은 남는다.

## 무엇을 갱신하나

- 커널(`linux-image-*`, `linux-headers-*`)과 NVIDIA 오픈 드라이버 계열, CUDA 런타임, `dkms`
- Opereta 패키지(`opereta-*`)와 그 밖의 보안 갱신. Opereta 패키지는 OS 업데이트 채널(위)에서 온다. 패키지 제거가 필요한 계획은 거절한다(`unexpected_removal`).

드라이버 버전은 `opereta-nvidia` 패키지가 핀으로 고정한다. 그래서 드라이버는 **새 `opereta-nvidia` 가 올라와야만** 바뀐다(같은 브랜치 안에서만, 브랜치 변경은 별도 릴리스다). 이 도구는 갱신하는 동안에만 정확한 버전 핀을 비켜 둔다.

## 순서와 안전 장치

1. 라이브 세션이 아닌지, `/boot` 와 `/` 의 여유 공간, dpkg 잠금, 인덱스 새로 받기(`apt-get update`).
2. 계획을 만든다. 보호 대상 패키지가 지워지면 거절한다.
3. 지금 쓰는 커널을 저장된 GRUB 기본값으로 먼저 고정한다. 이후 새 커널이 설치되어도 기본값은 바뀌지 않는다.
4. 새 커널과 헤더를 먼저 설치하고 오픈 모듈(`nvidia`, `nvidia-modeset`, `nvidia-uvm`, `nvidia-drm`)이 새 커널에 빌드되었는지 확인한다(파일 위치, 버전, 라이선스, initramfs). 실패하면 새 커널을 지우고 이전 커널을 그대로 둔다(`dkms_failed`).
5. 드라이버와 나머지 갱신을 설치한다. 드라이버 설치가 실패하면 갱신 전 버전으로 되돌린다.
6. 모든 설치된 커널에 최종 드라이버가 빌드되었는지 확인한다.
7. `grub-reboot` 로 새 커널을 **한 번만** 부팅하도록 건다. 재부팅은 사용자가 정한다.
8. 재부팅 뒤 `opereta-update-verify.service` 가 확인한다: 계획한 커널로 부팅했는지, 모듈 파일이 맞는지, 적재된 모듈이 오픈 모듈의 목표 버전인지, GPU 진단이 문제 없는지(갱신 전에 쓸 수 있던 GPU 가 못 쓰게 된 경우만 실패로 본다. 예를 들어 Secure Boot 가 모듈을 막아 갱신 전부터 못 쓰던 GPU 는 갱신을 막지 않는다). 통과하면 기본 커널을 새 커널로 확정하고 오래된 커널을 정리한다(현재 + 직전 1개 보존).

새 커널이 부팅하지 못하면 `grub-reboot` 가 한 번만 유효하므로 다음 부팅은 저장된 이전 커널로 돌아간다. 부팅했지만 확인이 실패하면 기본값은 바뀌지 않는다. 다음 재부팅은 이전 커널이다.

드라이버만 바뀐 갱신(커널이 같은 경우)은 새 모듈이 재부팅 뒤에만 적재된다. 그 사이에는 사용자 공간 라이브러리와 적재된 모듈의 버전이 어긋나므로 가능한 빨리 재부팅한다(`reboot required`).

## 롤백

`opereta update --rollback` 은 마지막 갱신의 기록(`/var/lib/opereta/update/rollback.json`)으로 드라이버 계열 패키지를 이전 버전으로 되돌리고 기본 커널을 이전 커널로 바꾼다. 재부팅이 필요하다.

- 이전 버전이 저장소에서 사라졌으면 `rollback_unavailable` 로 거절하고 아무것도 바꾸지 않는다. NVIDIA 저장소는 같은 브랜치의 이전 버전을 보관한다(2026-10-12 확인: 595.71.05 와 595.91.07). Opereta 저장소가 이전 버전을 남기는 pool 형식이면 이전 버전을 받을 수 있다. 릴리스 자산으로 쓰는 평평한 형식에는 이전 버전이 없다(위).
- 이전 커널이 이미 정리되었다면 거절한다.
- GRUB 메뉴에서 이전 커널을 직접 고르는 경로는 항상 남는다(메뉴는 평면이고 복구 항목은 없다).

## 파일과 상태

| 경로 | 내용 |
|---|---|
| `/var/lib/opereta/update/state.json` | 마지막 작업의 상태(`idle`, `running`, `pending_boot`, `committed`, `failed`, `rolled_back`), 계획, 이벤트 |
| `/var/lib/opereta/update/check.json` | 마지막 `--check` 의 결과 |
| `/var/lib/opereta/update/rollback.json` | 롤백에 쓰는 갱신 전 버전 목록 |
| `/var/lib/opereta/update/history/` | 끝난 작업의 기록 |
| `/etc/default/grub.d/20-opereta.cfg` | `GRUB_DEFAULT=saved`, 하위 메뉴와 복구 항목 끄기 |
| `/usr/lib/opereta/opereta-update` | root 로 도는 헬퍼. 데몬이 transient 유닛(`opereta-update-run.service`)으로 부른다 |

## 오류 코드

| 코드 | 뜻 |
|---|---|
| `live_session` | 라이브 세션이다 |
| `busy` | 다른 갱신 작업이 돌고 있다 |
| `pending_reboot` | 이전 갱신이 재부팅 확인을 기다린다 |
| `disk_space` | `/boot` 나 `/` 의 여유가 모자란다 |
| `network` | 패키지 인덱스를 새로 받지 못했다 |
| `dpkg_locked` | 다른 패키지 관리자가 돌고 있다 |
| `unexpected_removal` | 계획이 보호 대상 패키지를 지우려 한다 |
| `kernel_install_failed`, `dkms_failed` | 새 커널 설치나 모듈 빌드가 실패했다(이전 커널 유지) |
| `upgrade_failed` | 나머지 갱신이 실패했다 |
| `verify_failed` | 재부팅 뒤 확인이 실패했다(기본값 유지) |
| `rollback_unavailable` | 되돌릴 기록이나 이전 버전이 없다 |
