#!/bin/sh
# labwc 가 설정을 읽은 뒤 셸 스크립트로 실행한다.
# 배경(swaybg), 패널(yambar), Control Center 순서로 띄운다. 각 프로그램이 없으면 건너뛴다.
# 표준 오류는 줄 수를 제한해 사용자 런타임 디렉터리(램)의 파일에 남긴다. 오류가 반복되어도 파일이 커지지 않는다.
bounded_log() {
    # bounded_log 이름 명령...: 출력을 $XDG_RUNTIME_DIR/<이름>.log 에 처음 200줄만 남기고 나머지는 버린다.
    name="$1"; shift
    ( "$@" 2>&1 | cut -c1-300 | sed -n '1,200p' >> "${XDG_RUNTIME_DIR:-/tmp}/$name.log" ) &
}

# 배경: 어두운 단색 가운데에 흑백 마크. 이미지가 작아 램을 거의 쓰지 않는다.
if command -v swaybg >/dev/null 2>&1; then
    if [ -r /usr/share/lightlix/wallpaper.png ]; then
        bounded_log swaybg swaybg -c '#0d0d0d' -i /usr/share/lightlix/wallpaper.png -m center
    else
        bounded_log swaybg swaybg -c '#0d0d0d'
    fi
fi
# 패널(yambar). 설정 파일이 없거나 실행 파일이 없으면 건너뛴다.
if command -v yambar >/dev/null 2>&1 && [ -r /etc/xdg/yambar/config.yml ]; then
    bounded_log yambar yambar -c /etc/xdg/yambar/config.yml
fi
# Control Center 는 첫 부팅 마법사가 끝나지 않았을 때만 창을 연다. 끝났으면 바로 종료한다.
if command -v lightlix-control-center >/dev/null 2>&1; then
    lightlix-control-center --autostart &
fi
