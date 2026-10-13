# 상시 가동(절전과 종료 금지)

- 확인 날짜: 2026-10-13
- 상태: 0.5.2 에 구현했다. 단위 시험과 GUI 시험은 통과했다. **실제 노트북에서 덮개를 닫는 시험은 하지 않았다**(QEMU 에는 덮개 센서가 없다). 데몬이 실행 중인 logind 에 속성과 가능 여부를 물어 확인하는 것이 자동 시험의 범위다.

노트북을 서버로 쓸 때 덮개를 닫거나 몇 시간을 켜 두어도 **절전이나 종료로 서버가 멈추지 않게** 하는 설정이다. 기본으로 켜져 있다.

## 무엇을 막는가

| 원인 | 설정 | 값 |
|---|---|---|
| 덮개를 닫음(배터리, 전원 연결, 도킹) | `logind` `HandleLidSwitch*` | `ignore` |
| 절전 키, 최대 절전 키 | `HandleSuspendKey`, `HandleHibernateKey` | `ignore` |
| 오래 둠(유휴) | `IdleAction` | `ignore` |
| 명령, 데스크톱, 앱이 절전을 요청 | `sleep.conf` `AllowSuspend`, `AllowHibernation`, `AllowHybridSleep`, `AllowSuspendThenHibernate` | `no` |
| Xfce 전원 관리자의 덮개·유휴·버튼·배터리 동작 | `xfce4-power-manager` 기본값 | 아무것도 안 함, 유휴 없음 |

시스템 수준(systemd)에서 막으므로 데스크톱 세션이 없어도, 어떤 프로그램이 절전을 요청해도 거절된다.

## 사용자가 직접 끄는 것은 막지 않는다

상시 가동은 **절전과 최대 절전, 덮개와 유휴로 인한 동작**만 막는다. 사용자가 일부러 끄는 길은 모두 그대로다.

| 방법 | 동작 |
|---|---|
| 시작 메뉴 → 종료, 다시 시작 | 동작한다 |
| 터미널: `sudo poweroff`, `sudo shutdown -h now`, `sudo reboot` | 동작한다 |
| 전원 버튼을 눌러서 끔 | 동작한다(Diparnux 가 이 설정을 바꾸지 않는다) |
| 전원 버튼 길게 누름 | 하드웨어 강제 종료다 |

즉 막는 대상은 "사용자가 의도하지 않은 절전과 종료"이고 "사용자가 의도한 종료"는 막지 않는다.

설정 파일은 두 개다. `/usr/lib/systemd/logind.conf.d/50-diparnux-always-on.conf` 와 `/usr/lib/systemd/sleep.conf.d/50-diparnux-always-on.conf`. 끄면 `/etc/systemd/` 의 같은 이름 자리에 `/dev/null` 로 연결된 링크가 생겨 위 파일을 덮는다(systemd 의 표준 방법).

## 사용법

```sh
diparnux power          # 상태: 상시 가동 여부, 덮개, 전원(AC/배터리), 잔량, 가동 시간, 통과하지 못한 검사
diparnux power off      # 상시 가동을 끈다(덮개를 닫으면 절전한다)
diparnux power on       # 다시 켠다(기본)
```

제어 센터의 시스템 페이지 "상시 가동" 에서도 같은 상태를 보고 켜고 끌 수 있다.

상태는 파일의 존재가 아니라 **실행 중인 logind 에 물어 얻는다**. `HandleLidSwitch` 등의 속성이 `ignore` 이고 `CanSuspend`, `CanHibernate`, `CanHybridSleep`, `CanSuspendThenHibernate` 가 `no`(또는 하드웨어가 없어 `na`)인지 확인한다. 다른 설정이 값을 덮고 있으면 `diparnux power` 가 "failed checks" 로 어느 항목인지 알려 준다.

## 한계(소프트웨어로는 막을 수 없는 것)

| 한계 | 설명 |
|---|---|
| 배터리 방전 | 전원 어댑터 없이 배터리로만 돌면 배터리가 다 되었을 때 꺼진다. **서버로 쓸 때는 어댑터를 연결해 둔다.** 배터리로 돌 때는 상태 화면에 안내가 나온다 |
| 발열 | 덮개를 닫으면 통풍이 나빠지는 노트북이 많다. 오래 부하를 주면 온도가 올라가 CPU/GPU 가 속도를 낮추거나 하드웨어 보호로 꺼질 수 있다. 책상 위에서 환기구를 막지 않게 둔다 |
| 펌웨어 | 일부 노트북은 펌웨어(BIOS)가 덮개를 닫을 때 자체적으로 절전하는 설정이 있다. 그런 모델은 BIOS 설정에서 해당 항목을 꺼야 한다 |
| 전원 버튼 길게 누름 | 하드웨어 강제 종료(보통 4초 이상)는 소프트웨어가 막을 수 없고 막지도 않는다 |
| Wi-Fi 절전 | 무선 어댑터의 절전 모드는 지연을 늘릴 수 있다. 서버로 쓸 때는 유선 연결을 권한다 |

## 확인 방법(실기)

1. 서버를 시작하고 다른 컴퓨터에서 접속을 유지한다.
2. 노트북 덮개를 닫는다. 접속과 응답이 이어져야 한다.
3. 몇 시간 둔 뒤에도 같은 컴퓨터가 응답하고 `diparnux power` 의 가동 시간이 이어져 있어야 한다.
4. 문제가 있으면 `diparnux power` 의 "failed checks" 와 `journalctl -b -u systemd-logind` 를 확인한다.
