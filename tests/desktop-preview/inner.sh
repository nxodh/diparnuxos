#!/bin/bash
# 컨테이너 안에서 실행한다. THEME=Opereta|Opereta-Dark, OUT=/out, 저장소는 /src(읽기 전용).
# 패키지가 설치되는 위치와 같은 곳에 파일을 놓고 세션을 띄운 뒤 창 몇 개를 열어 스크린샷을 만든다.
set -eu
export DISPLAY=:99 HOME=/root LANG=C.UTF-8
THEME="${THEME:-Opereta}"
D=/src/packages/opereta-desktop/src
mkdir -p /usr/share/themes /usr/share/icons /etc/xdg/opereta/picom /usr/share/plank/themes /usr/share/opereta /root/.config/plank/dock1/launchers
cp -a "$D"/themes/Opereta "$D"/themes/Opereta-Dark /usr/share/themes/
for d in light dark; do
  t=Opereta; [ $d = dark ] && t=Opereta-Dark
  cp -a /src/assets/brand/generated/xfwm4/$d/*.png /usr/share/themes/$t/xfwm4/
done
cp -a "$D"/icons/Opereta "$D"/icons/Opereta-Dark /usr/share/icons/
cp -a "$D"/xdg/xfce4 /etc/xdg/opereta/
cp "$D"/picom/picom.conf /etc/xdg/opereta/picom/
install -m 755 "$D"/opereta-compositor /usr/bin/opereta-compositor
install -m 755 "$D"/opereta-dock /usr/bin/opereta-dock
cp -a "$D"/plank/themes/* /usr/share/plank/themes/
cp "$D"/plank/90_opereta-plank.gschema.override /usr/share/glib-2.0/schemas/
glib-compile-schemas /usr/share/glib-2.0/schemas
cp "$D"/plank/launchers/*.dockitem /root/.config/plank/dock1/launchers/
cp /src/assets/brand/generated/wallpaper*.png /usr/share/opereta/ 2>/dev/null || true
export XDG_CONFIG_DIRS=/etc/xdg/opereta:/etc/xdg
Xvfb :99 -screen 0 1440x900x24 -nolisten tcp +extension GLX +extension RENDER >/tmp/xvfb.log 2>&1 &
sleep 2

cat > /tmp/session.sh <<'SESSION'
#!/bin/bash
export DISPLAY=:99
xfconfd >/dev/null 2>&1 &
sleep 1
q() { xfconf-query "$@"; }
q -c xsettings -p /Net/ThemeName -n -t string -s "$THEME"
q -c xsettings -p /Net/IconThemeName -n -t string -s "$THEME"
q -c xsettings -p /Gtk/FontName -n -t string -s "Pretendard 10"
q -c xsettings -p /Gtk/DecorationLayout -n -t string -s "close,maximize,minimize:"
q -c xfwm4 -p /general/theme -n -t string -s "$THEME"
q -c xfwm4 -p /general/button_layout -n -t string -s "CMH|"
q -c xfwm4 -p /general/use_compositing -n -t bool -s false
xfsettingsd >/dev/null 2>&1 &
sleep 1
xfwm4 >/tmp/xfwm4.log 2>&1 &
sleep 2
opereta-compositor start >/tmp/comp.log 2>&1
echo "compositor: $(opereta-compositor status)" > /out/compositor-$THEME.txt
# 스크린샷은 xrender 로 찍는다(소프트웨어 GLX 의 화면은 scrot 에 잡히지 않는다). 시작 순서 시험은 위에서 이미 했다.
pkill -x picom; sleep 1
picom --config /etc/xdg/opereta/picom/picom.conf --backend xrender -b; sleep 2
xsetroot -solid '#e8e8ea' 2>/dev/null || true
xfce4-panel >/tmp/panel.log 2>&1 &
sleep 2
opereta-dock >/tmp/dock.log 2>&1 &
xfce4-terminal --geometry=70x10+120+120 >/tmp/term.log 2>&1 &
thunar /usr/share >/tmp/thunar.log 2>&1 &
gtk3-widget-factory >/tmp/wf.log 2>&1 &
sleep 14
scrot "/out/desktop-$THEME.png"
SESSION
chmod +x /tmp/session.sh
export THEME
dbus-run-session -- /tmp/session.sh || true
cp /tmp/dock.log "/out/dock-$THEME.log" 2>/dev/null || true
echo "done: $THEME"
