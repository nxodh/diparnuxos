#!/bin/sh
# labwc 가 설정을 읽은 뒤 셸 스크립트로 실행한다.
# Control Center 는 첫 부팅 마법사가 끝나지 않았을 때만 창을 연다. 끝났으면 바로 종료한다.
# 패널(yambar). 설정 파일이 없거나 실행 파일이 없으면 건너뛴다.
if command -v yambar >/dev/null 2>&1 && [ -r /etc/lightlix/yambar.yml ]; then
    # 시작 실패를 진단할 수 있게 표준 오류를 사용자 런타임 디렉터리의 파일에 남긴다.
    yambar -c /etc/lightlix/yambar.yml 2>>"${XDG_RUNTIME_DIR:-/tmp}/yambar.log" &
fi
if command -v lightlix-control-center >/dev/null 2>&1; then
    lightlix-control-center --autostart &
fi
