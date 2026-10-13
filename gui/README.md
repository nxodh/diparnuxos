# Diparnux Control Center

- 확인 날짜: 2026-10-12
- 상태: 구현됨. 가짜 데몬과 xvfb 의 GTK4 에서 화면 생성과 마법사 흐름을 확인했다. 실제 모니터에서의 표시와 입력, 한글 입력기는 미검증이다.

GTK4 + libadwaita(PyGObject) 앱이다. `diparnuxd` 의 유닉스 소켓 API 만 쓰며, root 권한이 필요한 일은 모두 데몬이 처리한다.

## 구성

| 파일 | 내용 |
|---|---|
| `diparnux_cc/api.py` | 표준 라이브러리만 쓰는 데몬 클라이언트. GTK 에 의존하지 않는다 |
| `diparnux_cc/logic.py` | GPU 상태 해석, 형식 변환, 첫 부팅 마법사 단계 판단. GTK 에 의존하지 않는다 |
| `diparnux_cc/window.py`, `dashboard.py`, `models.py`, `server.py`, `system.py`, `wizard.py` | 화면 |
| `po/` | gettext 카탈로그. 원문은 영어이고 `ko.po` 가 한국어 번역이다 |
| `tests/` | 단위 테스트, 가짜 데몬(`fakedaemon.py`), GTK 스모크 테스트 |

## 실행

```
diparnux-control-center              # 메인 창
diparnux-control-center --wizard     # 첫 부팅 마법사를 연다
diparnux-control-center --autostart  # 마법사가 끝나지 않았을 때만 창을 연다(Xfce 자동 시작 항목이 부른다)
diparnux-control-center --socket PATH --localedir DIR
```

## 테스트

저장소 루트에서 `make test-gui`. Docker 의 Debian 13 컨테이너에서 xvfb 와 함께 실행하며 Mac 에는 아무것도 설치하지 않는다.

## 번역 갱신

소스의 `_()` 문자열을 바꾸면 `.pot` 을 다시 만들고 `ko.po` 를 맞춘다.

```
xgettext --language=Python --keyword=_ --keyword=ngettext:1,2 --from-code=UTF-8 --no-location \
  --package-name=diparnux-control-center --output=po/diparnux-control-center.pot diparnux_cc/*.py
```

`tests/test_i18n.py` 는 `.pot` 과 소스, `.pot` 과 `ko.po` 가 어긋나면 실패한다.
