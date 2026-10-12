#!/bin/sh
# 데몬과 같은 버전을 요구하도록 의존성을 설정 파일의 버전으로 고정한다.
set -eu
# shellcheck source=/dev/null
. "$1"
sed "s/lightlix-daemon\$/lightlix-daemon (= ${LIGHTLIX_VERSION})/" debian/control > debian/control.new
mv debian/control.new debian/control
