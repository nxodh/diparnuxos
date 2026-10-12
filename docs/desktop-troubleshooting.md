# 데스크톱 모드 문제 해결

- 확인 날짜: 2026-10-12
- 상태: QEMU(virtio-gpu)에서만 시험했다. 실기와 VMware·VirtualBox 는 미검증이다.

데스크톱 모드는 `lightlix mode desktop` 으로 켠다. greetd(tuigreet)가 로그인 화면을 보여 주고, 로그인하면 `lightlix-session`(labwc 래퍼)이 데스크톱 세션을 시작한다.

## 로그인 직후 세션이 꺼진다

1. 세션 래퍼의 로그를 본다. labwc 의 출력이 여기에 남는다(저널이 없어도 읽을 수 있다).

   ```sh
   cat /run/user/$(id -u)/labwc.log          # 세션이 꺼진 뒤 다른 가상 터미널에서 같은 계정으로 로그인해 본다
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

## 로그 명령

`journalctl` 은 `systemd` 패키지의 `/usr/bin/journalctl` 이다. 저널은 램을 아끼려고 휘발성(`Storage=volatile`, 16 MB)이며 `/var/log` 에 파일이 없다. 일반 사용자는 `systemd-journal` 그룹이 아니면 `sudo journalctl -b` 로 읽어야 한다.
