#!/bin/sh
# 저장소 주소와 같은 버전의 런타임 의존성을 설정 파일에서 만든다.
set -eu
# shellcheck source=/dev/null
. "$1"
sed "/^Homepage:/d; /^Vcs-Git:/d; /^Vcs-Browser:/d; s/^Depends:.*/Depends: \${misc:Depends}, \${shlibs:Depends}, systemd, nftables, ca-certificates, opereta-nvidia (= ${OPERETA_VERSION}), opereta-llm (= ${OPERETA_VERSION})/" debian/control > debian/control.new
sed "/^Rules-Requires-Root:/a Homepage: ${OPERETA_HOME_URL}\nVcs-Git: ${OPERETA_HOME_URL}.git\nVcs-Browser: ${OPERETA_HOME_URL}" debian/control.new > debian/control
rm debian/control.new
