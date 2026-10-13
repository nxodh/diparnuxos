# shellcheck shell=sh
# 라이브 세션의 로그인 셸에서 한 번 안내한다. 설치 경로는 /etc/profile.d/lightlix-live-hint.sh 다.
# 라이브 세션은 쓰는 파일이 램에 쌓이고 apt 목록이 없다. 사용자가 자주 겪는 두 가지를 먼저 알려 준다.
if [ -e /run/live ] && [ -t 1 ] && [ -z "${LIGHTLIX_HINT_SHOWN:-}" ]; then
    LIGHTLIX_HINT_SHOWN=1
    export LIGHTLIX_HINT_SHOWN
    printf '%s\n' \
        "Lightlix live session:" \
        "  - Files you write or download here are kept in memory and disappear on reboot." \
        "  - Package lists are not included. Run 'sudo apt-get update' before 'apt-get install'." \
        "  - Install to a disk: 'sudo lightlix-install run' (console) or 'lightlix-installer' (desktop)." \
        "  - Network: 'lightlix network' and 'lightlix network wifi connect <name>'."
fi
