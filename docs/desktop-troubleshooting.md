# 데스크톱 모드 문제 해결

- 확인 날짜: 2026-10-12
- 상태: QEMU(virtio-gpu)에서만 시험했다. 실기와 VMware·VirtualBox 는 미검증이다.

데스크톱 모드는 `lightlix mode desktop` 으로 켠다. greetd(tuigreet)가 로그인 화면을 보여 주고, 로그인하면 `lightlix-session`(labwc 래퍼)이 데스크톱 세션을 시작한다.

## 로그인 직후 세션이 꺼진다

1. labwc 의 출력은 저널(`lightlix-labwc` 식별자)에 있다. 세션이 꺼진 뒤 다른 가상 터미널에서 로그인해 본다.

   ```sh
   sudo journalctl -b -t lightlix-labwc --no-pager | tail -50
   cat /run/user/$(id -u)/labwc.log          # 가상화 종류와 렌더러 값(짧은 시작 기록)
   ```
2. 직접 실행해 오류를 화면에서 본다.

   ```sh
   sudo systemctl stop greetd.service
   labwc                                      # 오류가 그대로 출력된다
   WLR_RENDERER=pixman WLR_NO_HARDWARE_CURSORS=1 labwc    # 소프트웨어 렌더링으로 시험
   ```
3. 가상 머신에서는 래퍼가 소프트웨어 렌더러(`WLR_RENDERER=pixman`)와 소프트웨어 커서(`WLR_NO_HARDWARE_CURSORS=1`)를 기본으로 쓴다(`systemd-detect-virt --vm` 으로 판별). 실기에서 같은 값이 필요하면 `/etc/xdg/labwc/environment` 에 한 줄씩 적는다.
4. 여러 번 실패하면 greetd 가 start limit 에 걸려 모드 전환이 거절된다. 되돌린다.

   ```sh
   sudo systemctl reset-failed greetd.service
   lightlix mode desktop
   ```

## `EFI loader partition unknown`

systemd 의 `systemd-gpt-auto-generator` 가 GRUB 로 부팅한 시스템에서 내는 안내 문구다. 서비스 단위가 다시 읽힐 때 콘솔에 나올 수 있으며 데스크톱 문제의 원인이 아니다.

## 웹 브라우저(Chrome, Chromium)를 설치하려면

라이브 세션은 apt 목록이 없어서 먼저 목록을 받아야 한다. 의존 패키지까지 한 번에 설치되게 `apt` 로 설치한다.

```sh
sudo apt-get update
sudo apt-get install -y chromium                              # Debian 의 Chromium
# Google Chrome:
curl -LO https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
sudo apt-get install -y ./google-chrome-stable_current_amd64.deb   # dpkg -i 가 아니라 apt 로 설치해야 의존성이 해결된다
```

- 데스크톱 세션(`lightlix mode desktop` 뒤 로그인)의 터미널(`foot`)에서 실행한다. 텍스트 콘솔(tty)에서는 `WAYLAND_DISPLAY` 가 없어 창을 열 수 없다.
- 세션 래퍼가 `XDG_SESSION_TYPE=wayland` 를 정한다(0.4.1 이전에는 비어 있었다). 확인: `echo $XDG_SESSION_TYPE $WAYLAND_DISPLAY`.
- Wayland 로 띄우려면 `chromium --ozone-platform=wayland`, XWayland 로 띄우면 `DISPLAY` 가 필요하다.
- 라이브 세션에서는 설치한 패키지와 내려받은 파일이 램에 쌓인다. 브라우저는 수백 MB 를 쓴다.

## 로그 명령

`journalctl` 은 `systemd` 패키지의 `/usr/bin/journalctl` 이다. 저널은 램을 아끼려고 휘발성(`Storage=volatile`, 16 MB)이며 `/var/log` 에 파일이 없다. 일반 사용자는 `systemd-journal` 그룹이 아니면 `sudo journalctl -b` 로 읽어야 한다.
