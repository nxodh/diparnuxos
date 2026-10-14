# 0.6.2 검증 기록: Deberopa 이름 변경

- 확인 날짜: 2026-10-14
- 상태: 단위 시험, 컨테이너 시험, **실제 Deberopa 0.2.0 으로 한 호환 시험**을 통과했다. CI 전체(ISO, 이미지 검사, QEMU E2E)는 아직 돌지 않았다.

| 대상 | 방법 | 결과 |
|---|---|---|
| 릴리스 계약 | `deberopa-linux-x64.tar.gz` 를 받아 `checksums.txt` 대조, 묶음 내용, `--version` | 일치(`0.2.0`, 묶음에 `deberopa` 하나) |
| 인식 문자열 | 바이너리의 문자열 검사(컨테이너) | 새 이름 인식 문자열 있음, 이전 OS 이름 0건 |
| 호환 시험 | `make test-deberopa-docker`(실제 Deberopa 0.2.0, 게이트웨이, 가짜 모델 서버) | **모든 시나리오 통과, 알려진 한계 없음**: 키 규칙, https 이름·IP, 안내의 `opereta tls ca`, `debug local` 이 "Opereta <버전> · 2 slots · 16,384 ctx per slot" |
| 패키지 | `opereta-deberopa` 를 amd64 컨테이너에서 빌드 | 통과(SHA-256 확인, `Replaces/Conflicts`, `/usr/bin/deberopa`) |
| Go | `go vet`, `gofmt`, `go test ./...` | 통과 |
| 제어 센터 | `make test-gui` | 88개 통과 |
| 이름 흔적, 비밀, 개인 정보, 린트 | `make lint check-legacy-name check-secrets` | 통과 |
| 이전 폴더 | 옛 폴더만 있을 때 새 폴더를 만들지 않고 옛 파일에 합침 | 통과 |

## 확인하지 못한 것

- CI: 이미지 검사(`opereta-deberopa` 패키지, `/usr/bin/deberopa`, 예외 없는 이름 검사), QEMU 부팅·데스크톱·설치 E2E.
- 실제 GPU 에서 Deberopa 작업을 끝까지 돌리는 것.
