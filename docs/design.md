# 디자인 시스템

- 확인 날짜: 2026-10-13
- 기준: 프로젝트 소유자가 준 로고(`assets/brand/source/lightlix-logo-1024.png`, 1024×1024). 밝은 회색(#EBEBEB) 둥근 사각형 위에 검은 굵은 글자 `LT / IX` 두 줄.

## 원칙

- **흑백 두 색과 회색 단계만 쓴다.** 강조색을 따로 두지 않는다. 오류, 경고, 성공 표시처럼 의미가 있는 색은 libadwaita 의 기본 의미 색을 그대로 쓴다.
- 글자는 굵은 산세리프(`NanumGothic`, `DejaVu Sans`)로 간결하게 쓴다. 장식을 넣지 않는다.
- 램을 쓰지 않는다: 배경 이미지는 작은 마크 하나이고, GRUB 은 그림 없이 색만 쓴다.

## 팔레트

| 이름 | 값 | 쓰는 곳 |
|---|---|---|
| 검정 | `#000000` | 로고 글자, 밝은 바탕의 강조 글자 |
| 바탕(어둡게) | `#0D0D0D` | 패널, 창 제목 줄(활성), 메뉴, 배경, Calamares 사이드바 |
| 그래파이트 | `#1A1A1A` / `#2B2B2B` / `#3A3A3A` | 비활성 제목 줄, 호버, 테두리 |
| 회색 | `#8E8E8E` | 비활성 글자 |
| 밝은 회색 | `#EBEBEB` | 로고 바탕, 활성 테두리, 본문 글자(어두운 바탕) |
| 흰색 | `#FFFFFF` | 어두운 바탕 위의 마크 |

## 적용 범위

| 대상 | 내용 | 파일 |
|---|---|---|
| 앱 아이콘 | hicolor 16~512 `lightlix.png`, 데스크톱 파일 `Icon=lightlix` | `packages/lightlix-branding` |
| 배경 화면 | `#0D0D0D` 단색 가운데에 희미한 마크(`swaybg`) | `lightlix-branding`, `labwc-autostart.sh` |
| 창 관리자 | labwc 테마 `Lightlix`(제목 줄, 테두리, 메뉴, OSD), 모서리 6px | `lightlix-desktop` (`labwc-themerc`, `labwc-rc.xml`) |
| 패널 | 검정 바탕, `LIGHTLIX` 글자, 네트워크·메모리·시계 | `yambar.yml`, `lightlix-panel-net` |
| 로그인 화면 | tuigreet 흑백 테마, `LIGHTLIX` 인사말 | `greetd-lightlix.toml` |
| Control Center | 강조색을 회색조로(밝은 테마 `#1A1A1A`, 어두운 테마 `#EBEBEB`), 아이콘 | `gui/lightlix_cc/theme.py` |
| 설치기 | Calamares 로고, 환영 이미지, 사이드바 색, 슬라이드 | `packages/lightlix-installer/src/branding` |
| 부트 메뉴 | GRUB 메뉴 색(라이브와 설치된 시스템) | `9500-...grub-timeout.hook.binary`, `10-lightlix-colors.cfg` |
| 시스템 정보 | `os-release` 의 `LOGO=lightlix`, `ANSI_COLOR="1;37"` | `lightlix-base` |

## 이미지 다시 만들기

원본이 바뀌면 컨테이너에서 파생 이미지를 다시 만든다.

```sh
bash scripts/make-brand-assets.sh      # assets/brand/generated/ 를 다시 쓴다
```

원본 포토샵 파일(`Lightlix.psd`)은 저장소에 두지 않았다(8 MB, 도구가 읽는 데 필요하지 않다).
