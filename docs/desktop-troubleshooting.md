# 데스크톱 모드 문제 해결

- 확인 날짜: 2026-10-13
- 상태: 0.5.0(Xfce, lightdm)의 화면은 Docker(Xvfb, Xephyr)와 QEMU(virtio-gpu)에서만 시험했다. 실기, NVIDIA 화면, VMware, VirtualBox는 미검증이다.

데스크톱 모드는 `opereta mode desktop` 으로 켠다. 로그인 화면은 lightdm(lightdm-gtk-greeter)이고, 로그인하면 Xfce(`startxfce4`) 세션이 Xorg 위에서 시작한다. **라이브 세션은 로그인 화면 없이 라이브 사용자로 자동 로그인한다.** 설치된 시스템은 로그인 화면을 보여 준다. `opereta mode server` 로 서버 모드로 돌아간다.

## 로그인 화면이 안 뜨거나 검은 화면이다

1. 서비스 상태와 로그를 본다.

   ```sh
   systemctl status lightdm.service
   sudo journalctl -b -u lightdm.service --no-pager | tail -50
   sudo tail -50 /var/log/lightdm/lightdm.log /var/log/lightdm/x-0.log
   ```
2. X 서버 로그에서 드라이버 오류(`(EE)`)를 찾는다.

   ```sh
   sudo grep -E '\(EE\)|\(WW\)' /var/log/Xorg.0.log /var/log/lightdm/x-0.log | head -30
   ```
3. 여러 번 실패하면 lightdm 이 start limit 에 걸려 모드 전환이 거절된다. 되돌린다.

   ```sh
   sudo systemctl reset-failed lightdm.service
   opereta mode desktop
   ```
4. 가상 머신에서는 Xorg 의 `modesetting` 드라이버가 virtio-gpu, bochs, vmwgfx 로 동작한다. 3D 가속이 없어도 소프트웨어 렌더링(llvmpipe)으로 뜬다. VMware 에서 안 되면 위 로그를 첨부해 알려 주세요.

## 라이브 세션이 자동 로그인하지 않게 하려면 (진단용)

라이브 세션의 자동 로그인은 live-config 가 부팅 때 `/etc/lightdm/lightdm.conf` 의 `autologin-user=` 에 라이브 사용자를 써서 켠다. 이미지 안의 파일과 설치된 시스템에는 자동 로그인이 없다.

- 부팅할 때 커널 명령줄에 `noautologin`(또는 `live-config.noautologin`)을 더하면 자동 로그인이 꺼진다.
- 이미 부팅한 뒤에는 다음으로 로그인 화면을 불러낸다.

```sh
sudo sed -i -E 's/^(autologin-user(-timeout)?=.*)$/#\1/' /etc/lightdm/lightdm.conf
sudo systemctl restart lightdm.service     # 로그인 화면이 나온다
```

라이브 사용자의 비밀번호는 `live` 다.

## 로그인 직후 세션이 꺼진다

1. 세션 오류는 사용자 홈의 `~/.xsession-errors` 와 저널에 있다. 세션이 꺼진 뒤 다른 가상 터미널(Ctrl+Alt+F3)에서 로그인해 본다.

   ```sh
   tail -50 ~/.xsession-errors
   sudo journalctl -b --no-pager | grep -iE 'xfce|xfwm|xfconf|dbus' | tail -50
   ```
2. 사용자 설정이 원인일 수 있다. 설정을 옮겨 두고 다시 로그인한다.

   ```sh
   mv ~/.config/xfce4 ~/.config/xfce4.bak
   mv ~/.cache/sessions ~/.cache/sessions.bak 2>/dev/null
   ```

## 화면 구성이 이상하다 (배경, 패널, 테마)

- Opereta 의 기본값은 `/etc/xdg/opereta/xfce4/` 에 있고 `/etc/X11/Xsession.d/55opereta-xdg` 가 `XDG_CONFIG_DIRS` 맨 앞에 둔다. 확인: 세션에서 `echo $XDG_CONFIG_DIRS` 가 `/etc/xdg/opereta:/etc/xdg` 로 시작해야 한다.
- 사용자가 바꾼 설정은 `~/.config/xfce4/` 에 저장되어 기본값보다 우선한다. 기본값으로 되돌리려면 그 폴더를 지우고 다시 로그인한다.
- 배경 화면은 첫 로그인에 `opereta-session-setup` 이 모든 모니터에 적용한다. 표식 파일 `~/.config/opereta/session-setup-done` 을 지우고 다시 로그인하면 다시 적용한다.
- GTK 테마 `Opereta`, 창 관리자 테마 `Opereta`, 아이콘 `Adwaita` 는 설정 관리자(모양, 창 관리자)에서 바꿀 수 있다.

## 부팅 항목 (BIOS 에서 Opereta 가 안 보인다, Windows 만 뜬다) (0.6.3)

```sh
opereta boot            # 지금 부팅한 항목, 부팅 순서, 문제와 해결 방법
opereta boot next opereta     # 다음 한 번만 Opereta 로 시작(영구 순서는 그대로)
opereta boot next windows     # 다음 한 번만 Windows 로 시작
opereta boot auto on          # 끄거나 다시 시작할 때마다 다음 시작을 Opereta 로 지정(설치 USB 로 시작하려면 먼저 끈다)
opereta boot repair           # GRUB 을 다시 설치하고 'opereta' 항목을 만든다
opereta boot cleanup          # 이전 이름(Diparnux 등)의 ESP 폴더와 부팅 항목을 지운다
```

- **UEFI 항목은 파티션이 아니라 이름(`opereta`)으로 보인다.** BIOS 의 부팅 장치 화면에서 디스크의 파티션이 따로 나오지 않는다.
- 일부 펌웨어는 재부팅 때 `BootOrder` 를 자체 순서(USB, Windows, 그 밖의 항목)로 다시 쓰고 모르는 항목은 설정 화면에서 숨긴다. 이 경우 **순서를 바꿔도 되돌아간다.** `opereta boot` 가 "펌웨어가 순서를 되돌렸다"고 알려 준다. 일회성 부팅 메뉴(F11, F12, Esc 등 제조사마다 다르다)에는 `opereta` 가 보이는 경우가 많고, `BootNext`(`opereta boot next`)는 지키는 펌웨어가 많다.
- 제어 센터의 "시작" 페이지에서 같은 일을 할 수 있다.
- BitLocker 가 켜진 Windows 가 있으면 부팅 구성을 바꾸기 전에 복구 키를 확인한다.
- 설치 직후 시계가 틀렸다면 BIOS 의 시각이 맞는지 확인한다. Windows 와 함께 설치하면 하드웨어 시계를 현지 시간으로 둔다.

## 한글 입력

fcitx5 와 한글 입력기가 들어 있다. 첫 로그인에 영문 자판과 한글이 있는 프로필을 만든다. 입력 전환 키는 fcitx5 설정(트레이의 키보드 아이콘 → 설정)에서 바꾼다. 입력이 안 되면 `echo $GTK_IM_MODULE $XMODIFIERS` 가 `fcitx @im=fcitx` 인지 본다.

## `EFI loader partition unknown`

systemd 의 `systemd-gpt-auto-generator` 가 GRUB 로 부팅한 시스템에서 내는 안내 문구다. 서비스 단위가 다시 읽힐 때 콘솔에 나올 수 있으며 데스크톱 문제의 원인이 아니다.

## 웹 브라우저

Firefox ESR 이 이미지에 들어 있다(메뉴 또는 `firefox-esr`). Chrome, Chromium 을 따로 설치하려면 라이브 세션은 apt 목록이 없어서 먼저 목록을 받아야 한다. 의존 패키지까지 한 번에 설치되게 `apt` 로 설치한다.

```sh
sudo apt-get update
sudo apt-get install -y chromium                              # Debian 의 Chromium
# Google Chrome:
curl -LO https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
sudo apt-get install -y ./google-chrome-stable_current_amd64.deb   # dpkg -i 가 아니라 apt 로 설치해야 의존성이 해결된다
```

- 데스크톱 세션(`opereta mode desktop` 뒤 로그인)의 터미널에서 실행한다. 텍스트 콘솔(tty)에서는 `DISPLAY` 가 없어 창을 열 수 없다.
- 라이브 세션에서는 설치한 패키지와 내려받은 파일이 램에 쌓인다. 브라우저는 수백 MB 를 쓴다.

## 로그 명령

`journalctl` 은 `systemd` 패키지의 `/usr/bin/journalctl` 이다. 저널은 램을 아끼려고 휘발성(`Storage=volatile`, 16 MB)이며 `/var/log` 에는 lightdm 과 Xorg 의 로그 파일만 있다. 일반 사용자는 `systemd-journal` 그룹이 아니면 `sudo journalctl -b` 로 읽어야 한다.
