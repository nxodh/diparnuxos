// Opereta 의 Firefox 기본값. 설치 경로는 /usr/share/firefox-esr/browser/defaults/preferences/opereta.js 다.
// 제목 줄은 창 관리자(xfwm4)가 그리게 한다. 그러면 닫기, 최대화(+), 최소화(-) 단추가 다른 창과 같은 모양이고 항상 맞다.
// Firefox 가 직접 그리는 제목 줄(탭이 제목 줄에 합쳐진 모양)은 GTK 가 내장한 단추 그림을 써서 최대화가 사각형으로 보인다.
// 사용자가 Firefox 설정(사용자 지정 도구 모음의 "제목 표시줄")에서 바꾸면 그 값이 우선한다.
pref("browser.tabs.inTitlebar", 0);
