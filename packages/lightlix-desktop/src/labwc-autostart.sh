#!/bin/sh
# labwc 가 설정을 읽은 뒤 셸 스크립트로 실행한다.
# Control Center 는 첫 부팅 마법사가 끝나지 않았을 때만 창을 연다. 끝났으면 바로 종료한다.
if command -v lightlix-control-center >/dev/null 2>&1; then
    lightlix-control-center --autostart &
fi
