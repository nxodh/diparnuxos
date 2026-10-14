# 데스크톱 화면 확인용 컨테이너. Xvfb 위에 Opereta 의 구성 요소를 설치한 것처럼 꾸며 스크린샷을 만든다.
# 실기를 대체하지는 못하지만 창 틀, 단추, 테마, 독, 패널이 의도대로 나오는지 회귀를 잡는다.
FROM debian:trixie
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
    xvfb xauth scrot x11-utils xdotool dbus-x11 procps \
    xfwm4 xfce4-panel xfdesktop4 xfce4-terminal thunar mousepad xfce4-settings xfce4-whiskermenu-plugin \
    xfce4-notifyd libxfce4ui-utils xfce4-genmon-plugin xfce4-pulseaudio-plugin xfce4-power-manager \
    xfce4-power-manager-plugins xfce4-windowck-plugin picom plank libglib2.0-bin \
    python3 python3-gi gir1.2-gtk-4.0 gir1.2-adw-1 gir1.2-gtk-3.0 libadwaita-1-0 gtk-3-examples firefox-esr \
    gsettings-desktop-schemas adwaita-icon-theme papirus-icon-theme librsvg2-common \
    fonts-pretendard fonts-nanum fonts-dejavu-core \
 && rm -rf /var/lib/apt/lists/*
