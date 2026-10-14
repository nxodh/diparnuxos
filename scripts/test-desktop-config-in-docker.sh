#!/usr/bin/env bash
# Xfce 데스크톱 구성을 컨테이너에서 확인한다. 화면이 없어서 Xvfb 를 쓴다.
#   1. 설정 파일 문법(XML, 셸), 패널 배치의 플러그인이 실제로 설치되는지
#   2. opereta-branding 과 opereta-desktop 패키지를 빌드해 apt 로 설치한다(Depends 해석 확인)
#   3. Xvfb 에서 Xsession 으로 Xfce 세션을 띄우고 xfwm4, xfce4-panel, xfdesktop 이 뜨는지, Opereta 기본값이 적용되는지 본다
# 화면 캡처는 OUT_DIR(기본: 없음)이 있으면 그곳에 PNG 로 남긴다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_DESKTOP_TEST_IMAGE:-debian:trixie}"
OUT_DIR="${OUT_DIR:-}"
command -v docker >/dev/null 2>&1 || { echo "test-desktop-config: docker is required" >&2; exit 2; }

mount_out=()
if [ -n "${OUT_DIR}" ]; then
    mkdir -p "${OUT_DIR}"
    mount_out=(-v "${OUT_DIR}:/out")
fi

docker run --rm --init --platform linux/amd64 -v "${ROOT}:/src:ro" ${mount_out[@]+"${mount_out[@]}"} \
    -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends build-essential dpkg-dev debhelper fakeroot libxml2-utils python3 xvfb xauth scrot procps x11-utils xdotool fcitx5 fcitx5-hangul fcitx5-frontend-gtk3 >/dev/null

D=/src/packages/opereta-desktop/src
echo "== 1. static checks"
for f in $D/opereta-panel-net $D/opereta-session-setup $D/xsession/55opereta-xdg; do
  sh -n "$f"; echo "ok: syntax $(basename "$f")"
done
find $D/xdg -name "*.xml" -print0 | xargs -0 xmllint --noout
echo "ok: xml well-formed"
# 패널 배치: plugin-ids 의 번호마다 plugin-N 이 있어야 한다.
python3 - <<PY
import re, sys
s = open("$D/xdg/xfce4/panel/default.xml").read()
ids = re.findall(r"<value type=\"int\" value=\"(\d+)\"/>", s.split("<property name=\"plugins\"")[0].split("plugin-ids")[1])
defs = set(re.findall(r"name=\"plugin-(\d+)\" type=\"string\"", s))
missing = [i for i in ids if i not in defs]
if missing or not ids:
    sys.exit("panel plugin ids without a definition: %s" % missing)
print("ok: panel layout has", len(ids), "plugins")
PY
# genmon 출력 형식: opereta 명령이 없으면 Offline.
out="$(sh $D/opereta-panel-net)"
printf "%s\n" "$out" | grep -qx "<txt>Offline</txt>" || { echo "FAIL: panel script output: $out" >&2; exit 1; }
printf "%s\n" "$out" | grep -q "<txtclick>opereta-control-center --page network</txtclick>" || { echo "FAIL: panel click action" >&2; exit 1; }
echo "ok: panel script"

echo "== 2. build and install packages"
mkdir -p /tmp/build && cd /tmp/build
cp -a /src/packages/opereta-desktop ./opereta-desktop
cp -a /src/packages/opereta-branding ./opereta-branding
cp -a /src/assets/brand ./opereta-branding/assets
cp -a /src/assets/brand ./opereta-desktop/assets
for p in opereta-branding opereta-desktop; do
  (cd $p && dpkg-buildpackage -us -uc -b >/tmp/build-$p.log 2>&1) || { echo "FAIL: building $p"; tail -30 /tmp/build-$p.log; exit 1; }
done
ls /tmp/build/*.deb
apt-get install -y -qq --no-install-recommends /tmp/build/opereta-branding_*.deb /tmp/build/opereta-desktop_*.deb >/dev/null
echo "ok: packages installed"
for f in /etc/xdg/opereta/xfce4/panel/default.xml /etc/X11/Xsession.d/55opereta-xdg /usr/share/lightdm/lightdm.conf.d/50-opereta.conf \
         /usr/share/lightdm/lightdm-gtk-greeter.conf.d/50-opereta.conf /usr/bin/opereta-panel-net; do
  test -e "$f" || { echo "FAIL: missing $f" >&2; exit 1; }
done
echo "ok: installed files"
# Debian 이 싣는 /etc/xdg 파일을 이 패키지가 덮지 않았는지(소유 패키지가 다르다).
for f in /etc/xdg/xfce4/panel/default.xml /etc/xdg/xfce4/xfconf/xfce-perchannel-xml/xsettings.xml; do
  owner="$(dpkg -S "$f" | cut -d: -f1)"
  [ "$owner" != opereta-desktop ] || { echo "FAIL: opereta-desktop owns $f" >&2; exit 1; }
done
# 패널 배치가 쓰는 플러그인이 설치되어 있다.
for p in whiskermenu tasklist separator systray genmon pulseaudio power-manager-plugin clock actions; do
  ls /usr/share/xfce4/panel/plugins/$p.desktop >/dev/null 2>&1 || { echo "FAIL: panel plugin not installed: $p" >&2; exit 1; }
done
echo "ok: panel plugins installed"
# 설치된 이미지의 lightdm.conf 는 자동 로그인이 꺼져 있다(라이브 세션에서는 live-config 가 부팅 때만 켠다).
if grep -Eq "^[[:space:]]*autologin-user=.+" /etc/lightdm/lightdm.conf; then echo "FAIL: autologin is on in the installed lightdm.conf" >&2; exit 1; fi
echo "ok: no autologin in an installed system"

echo "== 3. Xfce session under Xvfb"
useradd -m -s /bin/bash tester
export DISPLAY=:99
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
sleep 3
su tester -c "export DISPLAY=:99; cd ~; dbus-run-session -- /etc/X11/Xsession startxfce4 >/tmp/session.log 2>&1 &"
ok=0
for i in $(seq 1 60); do
  if pgrep -u tester -x xfwm4 >/dev/null && pgrep -u tester -x xfce4-panel >/dev/null && pgrep -u tester -x xfdesktop >/dev/null; then ok=1; break; fi
  sleep 1
done
sleep 5
if [ "$ok" != 1 ]; then echo "FAIL: Xfce session did not start"; ps -eo pid,user,args | grep -E "xfce|xfwm|dbus" | head -20; tail -40 /tmp/session.log; exit 1; fi
echo "ok: xfwm4, xfce4-panel and xfdesktop are running"
pid="$(pgrep -u tester -x xfce4-panel | head -n 1)"
penv() { su tester -c "tr \"\\0\" \"\\n\" < /proc/$pid/environ"; }
bus="$(penv | sed -n "s/^DBUS_SESSION_BUS_ADDRESS=//p")"
q() { su tester -c "DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus xfconf-query $*" 2>&1; }
theme="$(q -c xsettings -p /Net/ThemeName)"
[ "$theme" = Opereta ] || { echo "FAIL: xsettings /Net/ThemeName=$theme (expected Opereta; XDG_CONFIG_DIRS not applied?)"; exit 1; }
icons="$(q -c xsettings -p /Net/IconThemeName)"
[ "$icons" = Adwaita ] || { echo "FAIL: icon theme=$icons"; exit 1; }
wm="$(q -c xfwm4 -p /general/theme)"
[ "$wm" = Opereta ] || { echo "FAIL: xfwm4 theme=$wm"; exit 1; }
echo "ok: Opereta defaults applied (GTK theme, icons, window manager theme)"
plugins="$(su tester -c "DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus xfconf-query -c xfce4-panel -l" | grep -c "^/plugins/plugin-[0-9]*$" || true)"
echo "info: panel plugins configured: $plugins"
env_session="$(penv | grep -E "^(XDG_CURRENT_DESKTOP|XDG_SESSION_DESKTOP|XDG_CONFIG_DIRS)=" || true)"
echo "$env_session"
su tester -c "export DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus; nohup xfce4-terminal --geometry=70x12+120+80 >/tmp/term.log 2>&1 &"
su tester -c "export DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus; nohup thunar /usr/share/opereta >/tmp/thunar.log 2>&1 &"
sleep 6
if [ -d /out ]; then
  su tester -c "DISPLAY=:99 scrot /tmp/desktop.png" && cp /tmp/desktop.png /out/xfce-desktop.png
  cp /tmp/session.log /out/session.log 2>/dev/null || true
fi
# 터미널 색: 첫 로그인 스크립트가 사용자 설정으로 복사했는지 본다.
su tester -c "test -r ~/.config/xfce4/terminal/terminalrc && grep -q ColorBackground=#0d0d0d ~/.config/xfce4/terminal/terminalrc" || { echo "FAIL: terminal colors were not seeded"; exit 1; }
su tester -c "test -e ~/.config/opereta/session-setup-done" || { echo "FAIL: first-login setup marker missing"; exit 1; }
echo "ok: first-login setup ran"

# 한글 입력기: 자동 시작으로 fcitx5 가 떠야 하고, 네 가지 전환 키가 실제 GTK 앱에서 한/영을 오가야 한다.
for i in $(seq 1 20); do su tester -c "pgrep -x fcitx5 >/dev/null" && break; sleep 1; done
su tester -c "pgrep -x fcitx5 >/dev/null" || { echo "FAIL: fcitx5 was not started by the session"; exit 1; }
echo "ok: fcitx5 is running"
su tester -c "grep -q '^1=Alt_R' ~/.config/fcitx5/config" || { echo "FAIL: trigger keys were not written"; su tester -c "cat ~/.config/fcitx5/config"; exit 1; }
su tester -c "export DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus GTK_IM_MODULE=fcitx XMODIFIERS=@im=fcitx; nohup mousepad >/tmp/mousepad.log 2>&1 &"
sleep 6
wid="$(su tester -c "DISPLAY=:99 xdotool search --class mousepad | head -n 1")"
[ -n "$wid" ] || { echo "FAIL: the test editor window did not open"; exit 1; }
su tester -c "DISPLAY=:99 xdotool windowactivate $wid" 2>/dev/null || true
su tester -c "DISPLAY=:99 xdotool windowfocus $wid" 2>/dev/null || true
sleep 1
im() { su tester -c "DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=$bus fcitx5-remote -n"; }
# 창이 포커스를 받고 fcitx5 가 입력 컨텍스트를 만들 때까지 첫 상태를 기다린다.
for i in $(seq 1 15); do [ "$(im 2>/dev/null)" = keyboard-us ] && break; sleep 1; done
[ "$(im)" = keyboard-us ] || { echo "FAIL: the input method does not start as keyboard-us: $(im)"; exit 1; }
# 키를 보낸 뒤 상태가 바뀔 때까지 기다린다(느린 CI 에서 한 번에 안 바뀌는 경우를 걸러 낸다).
toggle() { # 키, 기대 상태
  key="$1"; want="$2"
  su tester -c "DISPLAY=:99 xdotool key --clearmodifiers $key"
  for i in 1 2 3 4 5 6; do [ "$(im)" = "$want" ] && return 0; sleep 1; done
  return 1
}
for key in Hangul shift+space ctrl+space; do
  toggle "$key" hangul || { echo "FAIL: [$key] did not switch to Hangul (now: $(im))"; exit 1; }
  toggle "$key" keyboard-us || { echo "FAIL: [$key] did not switch back (now: $(im))"; exit 1; }
done
echo "ok: Hangul, Shift+Space and Ctrl+Space toggle the input method"
# 오른쪽 Alt 를 그대로 두드리는 전환은 수정 키 단독 입력이라 누르는 시간이 짧아야 한다. 참고용으로만 본다.
before="$(im)"; su tester -c "DISPLAY=:99 xdotool key --clearmodifiers Alt_R"; sleep 1
if [ "$before" != "$(im)" ]; then echo "info: a quick right Alt tap toggles the input method"; su tester -c "DISPLAY=:99 xdotool key --clearmodifiers Alt_R"; sleep 1; else echo "info: a right Alt tap did not toggle (the Xorg option korean:ralt_hangul makes it a real Hangul key)"; fi
test -s /etc/X11/xorg.conf.d/90-opereta-keyboard.conf || { echo "FAIL: Xorg keyboard options file is missing"; exit 1; }
grep -q "korean:ralt_hangul" /etc/X11/xorg.conf.d/90-opereta-keyboard.conf || { echo "FAIL: ralt_hangul option missing"; exit 1; }
su tester -c "pkill -x mousepad" || true

echo "== 4. login screen (lightdm --test-mode in Xephyr)"
pkill -u tester -x xfce4-session || true
sleep 3
pkill -u tester -x xfwm4 || true
apt-get install -y -qq --no-install-recommends xserver-xephyr >/dev/null
# lightdm-gtk-greeter 는 시스템 버스가 없으면 바로 끝난다.
mkdir -p /run/dbus && rm -f /run/dbus/pid && dbus-daemon --system --fork
su tester -c "export DISPLAY=:99; nohup lightdm --test-mode --debug >/tmp/lightdm.log 2>&1 &"
greeter=0
for i in $(seq 1 40); do
  if pgrep -u tester -f lightdm-gtk-greeter >/dev/null; then greeter=1; break; fi
  sleep 1
done
sleep 4
if [ -d /out ]; then
  su tester -c "DISPLAY=:99 scrot /tmp/greeter.png" && cp /tmp/greeter.png /out/lightdm-greeter.png
  cp /tmp/lightdm.log /out/lightdm.log 2>/dev/null || true
  cp /home/tester/.cache/lightdm/log/seat0-greeter.log /out/greeter.log 2>/dev/null || true
fi
if [ "$greeter" != 1 ]; then echo "FAIL: lightdm-gtk-greeter did not start"; tail -30 /tmp/lightdm.log; exit 1; fi
echo "ok: lightdm-gtk-greeter is running"
echo "PASS: Xfce desktop configuration"
'
