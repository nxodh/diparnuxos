# opereta CLI

구현은 `daemon/cmd/opereta/`, 유닉스 소켓 클라이언트는 `daemon/internal/client/`에 있다.
`opereta-daemon` 패키지가 `/usr/bin/opereta`와 `/usr/sbin/operetad`를 설치한다.
CLI는 데몬의 REST API만 호출하며, 권한이 필요한 시스템 변경은 데몬이 한다.

## 시작하기

라이브 사용자는 부팅 훅에서 `opereta` 그룹에 들어간다. 다른 사용자는 관리자가 이 그룹에 추가한 뒤 다시 로그인해야 한다.
기본 소켓은 `/run/opereta/operetad.sock`이며 권한은 `root:opereta`, `0660`이다.

```sh
opereta status
opereta gpu check
opereta model files ggml-org/tiny-llamas
opereta model pull ggml-org/tiny-llamas --file stories260K.gguf
opereta model list
```

`model list`에서 확인한 실제 ID를 아래 `<model-id>`에 넣는다. 이 작은 모델은 추론 경로 검사에 쓰며 응답 품질을 평가하는 모델이 아니다.

```sh
opereta model default <model-id>
opereta serve plan
opereta serve start
opereta chat --max-tokens 16 Once upon a time
opereta serve logs -n 50
opereta serve stop
```

`serve plan`과 `serve start`는 `--ngl`, `--ctx`, `--cache-type`, `--batch`, `--load-mode`를 받는다.
CPU 계획은 경고와 `running_cpu_fallback` 상태로 표시한다. GPU 계획인데 실제 오프로드가 없으면 서버를 중지하고 오류로 처리한다.

## 설정과 키

llama-server API는 기본적으로 `127.0.0.1:8080`에서 열린다. LAN 공개에는 API 키가 필요하다.
생성한 키의 평문은 생성 응답에서 한 번만 보인다.

```sh
opereta apikey create workstation
opereta config set lan on
opereta config set port 8080
opereta config set autostart on
opereta config show
```

설정 변경 뒤 재시작이 필요한 경우 CLI가 안내한다. Hugging Face 토큰은 명령 인자가 아니라 표준 입력으로 전달한다.
토큰을 입력할 때 주변 사람이 터미널을 보지 않게 한다.

```sh
opereta config set hf-token
sudo systemctl restart operetad
```

다운로드 클라이언트는 데몬이 시작할 때 토큰을 읽으므로 토큰을 바꾸면 데몬을 재시작한다.
이 재시작은 진행 중인 다운로드를 취소할 수 있다. `serve stop/start`만 실행하면 토큰은 바뀌지 않는다.

## 출력과 오류

```sh
opereta help
opereta help model
opereta --json status
opereta --socket /run/opereta/operetad.sock status
opereta report -o report.txt
```

전역 플래그 `--json`, `--socket`은 명령 앞에 둔다. 소켓 경로는 `OPERETA_SOCKET` 환경 변수로도 정한다.
사람용 출력은 영어 ASCII다. JSON은 비 ASCII 문자를 `\uXXXX`로 이스케이프하며 원래 값을 보존한다.
`report -o`가 새로 만드는 파일은 권한 `0600`이다. 다운로드 중 Ctrl-C는 CLI의 기다림을 끊으며 데몬의 작업은 계속된다.

| 종료 코드 | 의미 |
|---|---|
| 0 | 성공 또는 기다림 취소 |
| 1 | API 오류, 다운로드나 서버 실패 |
| 2 | 사용법 오류 또는 미구현 기능 |
| 3 | 소켓 연결 실패 또는 접근 권한 부족 |

`update`와 데스크톱 모드는 후속 Phase에 구현한다. 가짜 API 테스트는 각 명령의 요청, 응답, 종료 코드를 검사한다.
실제 ISO의 다운로드·CPU 추론은 QEMU E2E로 검사하며, NVIDIA 오프로드는 별도 실기 검증이 필요하다.
