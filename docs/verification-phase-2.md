# Phase 2 검증 기록

확인 날짜: 2026-10-11. 작업 브랜치는 `phase-2`, PR의 기준 브랜치는 `phase-1-fixes`다.
NVIDIA 실 하드웨어에서는 확인하지 않았다. 아래 로컬 결과, 패키지 컨테이너 검사, QEMU fixture, 실제 ISO CI를 구분한다. 실행별 결과는 아래 실행표를 기준으로 한다.

## 현재 요약

`035dfb4a12ae09ebc04b72967b708d6ed67c22d2`의 [CI 38142350756](https://github.com/nxodh/operetaos/actions/runs/38142350756)이 전체 성공했다(lint와 ISO 빌드·부팅 job 모두). 이 실행에서 QEMU/KVM 위의 실제 ISO가 live 사용자 권한으로 모델 다운로드와 SHA-256 확인, CPU 서버 시작, 채팅, 데몬 재시작 뒤 기존 서버 채택, 정상 중지까지 마쳤다. 이 확인은 CPU 경로에 한정된다. GPU가 없는 VM이므로 `running_cpu_fallback`이며 NVIDIA 오프로드는 검증하지 않았다.

앞선 세 실행은 각각 다른 단계에서 실패했고 아래 표에 보존했다. 마지막 실패(38140520091)의 정확한 `/proc` 원인은 로그로 확정하지 못했다. 그 뒤 llama 유닛을 `Type=exec`로 바꾼 `035dfb4`에서 같은 단계가 통과했지만, 이것이 그 실패의 원인이었다고 단정하지 않는다.

## 인수 시점과 구현

`1807605`까지 데몬 라이브러리, 공유 인터페이스, REST API, systemd 계층이 커밋되어 있었다.
Claude 워커가 남긴 CLI와 llama.cpp 파이프라인을 검토하고 이어서 패키징과 QEMU CPU 추론 검사를 구현했다.
CLI 구현은 `daemon/cmd/opereta`, 유닉스 소켓 클라이언트는 `daemon/internal/client`에 있다.

인수 과정에서 모델 요청의 `PullError`와 Hugging Face 인증 실패가 모두 `internal`로 바뀌는 문제를 수정했다.
인증 실패, gated 저장소, 파일 선택 필요, 이름 충돌 등을 HTTP 상태와 오류 코드로 보존한다.
알 수 없는 내부 오류의 상세 내용은 API 응답에 노출하지 않는다. 회귀 검사는 `daemon/internal/api/errors_test.go`에 있다.

## 실제로 확인한 로컬 결과

### CLI와 Go

- Debian 13 arm64 컨테이너에서 CLI·클라이언트의 gofmt 확인과 `go vet -mod=vendor`가 통과했다.
- `go test -race -mod=vendor -count=1 ./cmd/opereta/... ./internal/client/...`가 통과했다. 각각 2.325초, 3.647초였다.
- 실제 패키지 빌드가 실행한 전체 Go 테스트에서 CLI, API, 다운로드, 모델 관리, supervisor 등 모든 패키지가 통과했다. 새 API 오류 회귀 검사도 포함됐다.
- Claude CLI 워커의 실제 데몬 연동 결과도 검토했다. systemd·nftables가 없는 컨테이너에서 상태 조회와 오류 응답을 확인한 범위이며, 실제 추론 서비스 실행 성공을 뜻하지 않는다.

첫 전체 빌드 시도는 검사 도중 root가 API 파일을 복사해 의존성 입력이 바뀌면서 실패했다.
그 결과는 성공으로 계산하지 않는다. 이후 패키징 워커의 고정 사본에서 전체 Go 테스트가 통과했다.

### llama.cpp

이 결과는 완료된 llama 워커의 원본 빌드·검사 보고서를 회수하고 코드와 대조한 결과다.

- 고정 커밋의 네이티브 arm64 CPU 소스 빌드가 약 43분에 통과했다. 패키지 크기는 4,133,040바이트였고 CPU 백엔드 변형 여덟 개가 들어 있었다.
- 모든 ELF의 RUNPATH `$ORIGIN`, 빌드 트리를 제거한 뒤 복사본의 `--version`과 `--list-devices`, 헤더·컴파일러·정적 라이브러리 부재를 확인했다.
- 같은 입력의 캐시 재실행은 빌드를 건너뛰었고 약 1.8초였다.
- CUDA 없는 깨끗한 Debian 컨테이너에서 작은 GGUF를 실제로 내려받고 SHA-256을 확인한 뒤 `/health` 200과 비어 있지 않은 채팅 응답을 얻었다. 실제 추론을 포함한 패키지 검사는 약 17초에 통과했다.
- amd64 CUDA 빌더 이미지 설치와 CMake 구성은 통과했다. 이 검사는 CUDA 커널 전체 컴파일을 포함하지 않았다.
- 추가한 제3자 고지 추출을 고정 소스의 파일로 Debian 컨테이너에서 실행해 통과했다. 최종 `.deb`에 포함되는지는 CI가 검사한다.

CPU 스모크 패키지와 임시 llama 빌더 이미지는 원래 워커가 정리했다. 기존 `out/drive-upload`는 보존했다.

### 정적 검사

인수 초기 파일에서 `make lint`, GPU 진단 스텁 23개, Docker shellcheck 0.9.0과 actionlint가 통과했다.
라이선스 스크립트 추가 뒤 shellcheck 0.9.0도 통과했다. 패키징·E2E 변경 뒤 전체 shellcheck 0.9.0과 actionlint도 통과했다.

### 패키징과 시스템 통합

- 실제 arm64 `opereta-daemon` 패키지는 2,790,088바이트였다. 버전 주입, glibc와 같은 버전의 Opereta 런타임 의존성, 설치 파일과 생성된 maintscript를 확인했다.
- sysusers를 tmpfiles보다 먼저 실행하는 순서, 비특권 추론 사용자의 모델·키 읽기, 다른 사용자의 키 접근 거부, 제어 소켓 그룹과 서비스 활성화 링크를 검사해 통과했다.
- 실제 패키지의 유닛은 `systemd-analyze verify` 종료 0이었다. 별도 llama-server와 GPU 진단 실행 파일은 이 컨테이너 검사에서 스텁으로 대체했다.
- 실제 nft 기본 규칙을 적용한 뒤 잘못된 저장 파일 적용을 시도했다. 실패 전후 테이블이 같았고 drop 정책이 남았다.
- GPU 권한 검사와 사용자 이름을 바꾼 live-config 훅은 fixture로 통과했다. 실제 NVIDIA 장치 접근을 증명하지 않는다.
- 최종 `make test-daemon`의 전체 Go build, vet, test가 종료 0이었다. 임시 컨테이너와 해당 워커의 이미지 태그는 제거했다.

패키지 검증 중 Go의 압축 DWARF를 처리하지 못하는 `dh_dwz`, compat 13의 sysusers 순서, 라이선스 파일 자동 압축 문제를 실제로 발견하고 수정했다.
이 로컬 패키지 검사는 버전 변경 전의 결과이며 최종 버전의 amd64 패키지는 ISO CI가 다시 빌드·검사한다.

### QEMU 드라이버

root가 데몬 RSS 기록을 추가하고 fixture의 준비 표시 경쟁을 수정한 뒤 Debian 컨테이너에서 고정 사본의 테스트 10개가 32.684초에 모두 통과했다.
정상 흐름, 권한·서비스·해시·생성 응답 오류, QEMU 종료, 명령·직렬·전체 시간 초과, 신호 정리와 KVM 초기화 실패의 TCG 재시도를 검사했다.
실제 ISO와 모델 추론은 이 fixture에서 실행하지 않았다.

## GitHub Actions 실행 결과

[CI 38131367776](https://github.com/nxodh/operetaos/actions/runs/38131367776)는 PR의 `b525ed9662e85cd9c83e89f6e38e78ac9d7842d7` head에서 실행되어 전체 실패로 끝났다(2026-10-11 10:16:37–11:15:18 UTC). 결과와 아래 단계 시간은 GitHub Actions job/step API와 실제 로그에서 확인했다.

통과한 단계:

- lint job은 1분 23초에 통과했다. 전체 Go build/vet/test, 셸 lint, GPU 진단 스텁 23개, QEMU E2E fixture 검사를 포함한다.
- `Build llama.cpp package (opereta-llm)`는 44분 7초에 통과했다. 실제 amd64 CUDA 모듈을 빌드했고, CUDA 모듈의 `NEEDED` 항목과 `.deb` 의존성 검사를 실행했다.
- `Check opereta-llm package without CUDA`는 8초에 통과했다. `opereta-llm_0.2.0_amd64.deb`에 CUDA 모듈이 포함됐고 CPU 백엔드 모듈 14개, 제3자 고지 7개와 `opereta-nvidia` 의존성이 확인됐다. CUDA 없는 Debian에서 `libcudart.so.13`과 `libcublas.so.13`은 `not found`였고, 고정 모델 `stories260K.gguf`의 SHA-256 `047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b` 검증, `/health` 200, 채팅 응답이 통과했다. 응답은 16 completion tokens, 로그의 `predicted_per_second`는 4005.3404539385847이었다. 이는 패키지 검사용 Debian 컨테이너 결과이며 ISO의 live-user E2E 결과가 아니다.
- `Save verified llama.cpp package` 캐시 저장이 통과했다.
- ISO 생성은 12분 34초, NVIDIA 모듈·이미지 정책 검사는 6초에 통과했다. ISO 검사 로그에서 `opereta-llm=0.2.0`, `opereta-daemon=0.2.0`, NVIDIA 드라이버 595.91.07, CUDA 런타임 패키지를 확인했고 CUDA 개발 도구와 `-dev` 패키지 부재, 라이선스, 오픈 모듈, 서명 키 제거, GPT/EFI 파티션 검사가 통과했다. 이 이미지는 물리 GPU에서 부팅·추론한 결과가 아니다.
- UEFI 직렬 로그인 프롬프트 부팅은 32초, ISO를 USB 저장 장치로 붙인 UEFI 부팅은 34초에 통과했다.

실패한 단계와 확보한 근거:

- `CPU inference E2E (UEFI, live user, Hugging Face NAT)`는 34초 후 실패했다. 실제 로그에서 `model-pull` 명령이 `ggml-org/tiny-llamas/stories260K.gguf` (revision `99dd1a73db5a37100bd4ae633f4cfce6560e1567`) 다운로드 전 중단됐고, `bytes_done`은 0 / `bytes_total`은 1,185,376, `error_code`는 `insufficient_space`, 오류는 `not enough free disk space for the model`이었다. CLI는 모델에 약 1.1 MiB가 필요하다고 출력했다.
- 따라서 이 run은 ISO에서의 실제 모델 SHA 검증, 채팅 생성, llama 서비스 시작·정상 중지, PID와 RSS를 입증하지 못했다. 앞선 패키지 검사 결과를 live-user 추론 성공으로 간주하지 않는다.
- `Upload QEMU E2E logs`도 실패했다. GitHub 로그는 `EACCES: permission denied, scandir '/home/runner/work/operetaos/operetaos/out/qemu-e2e/run-3koliggf'`를 기록했다. GitHub Artifacts API 결과는 빈 목록이어서 요청된 `result.json`과 직렬 로그를 받을 수 없었다. 이후 ISO와 checksum 아티팩트 업로드는 건너뛰었다.

이 CI 실행에서 live-user CPU 추론은 미검증으로 남았다. 나머지 E2E 단계는 모델을 받지 못해 실행되지 않았다.

## 후속 통합 CI 실행

아래 실행은 첫 CI 이후 수정 사항을 확인하기 위해 순서대로 진행했다. 이전 실패 설명은 당시 로그에서 확인한 사실로 유지하며, 이후 수정이 과거 실행을 성공으로 바꾸지는 않는다.

| 실행 / head | 실제 결과 | 완료된 범위 | 멈춘 범위와 근거 |
|---|---|---|---|
| [38138886386](https://github.com/nxodh/operetaos/actions/runs/38138886386) / `1e689e1` | 실패 | 빌드·ISO·UEFI 부팅·모델 pull/SHA·CPU 서버 시작. 실제 계획과 서버 context는 128, batch는 32였다. | E2E 드라이버가 context 256을 기대해 잘못 실패했다. 채팅, daemon 재시작, 정상 중지는 실행하지 않았다. 로그 아티팩트는 업로드됐고 ISO/checksum 아티팩트는 없었다. |
| [38140520091](https://github.com/nxodh/operetaos/actions/runs/38140520091) / `be4a344` | 실패 | 캐시된 패키지 검사·ISO·이미지 정책·UEFI CD/USB 부팅·모델 pull/SHA. QEMU 실패 로그 업로드는 성공했다. | CPU MainPID의 실행 파일/환경 정체성 확인이 실패해 시작을 중단했다. 로그에는 당시 `/proc` 실행 파일이나 환경 차이가 없어 원인을 확정할 수 없다. chat, daemon 재시작, 정상 중지는 실행하지 않았고 ISO/checksum은 업로드되지 않았다. |
| [38142350756](https://github.com/nxodh/operetaos/actions/runs/38142350756) / `035dfb4a12ae09ebc04b72967b708d6ed67c22d2` | **성공** (사용자 요청 취소 뒤 같은 SHA로 재실행) | lint, 캐시된 llama 패키지 검사, ISO 빌드, 이미지 정책, UEFI CD/USB 부팅, live CPU E2E 전체, QEMU 로그와 ISO/checksum 업로드. | 없음. 상세는 아래 "CI 38142350756의 live CPU E2E"에 있다. |

R13의 `Type=simple` fork-before-exec 경로는 독립 Sol 리뷰와 disposable supervisor fake에서 재현되었고, shipped llama 유닛은 `Type=exec`로 바뀌었다. 독립 리뷰는 `035dfb4`의 두 파일 변경(`daemon/dist/opereta-llama.service`, `daemon/dist/dist_test.go`)을 소스 수준에서 승인했고 새 발견은 없었다. 이 승인은 systemd 257.13 파서 검증과 Docker Go 검사에 근거하며 실제 PID 1 실행을 대신하지 않는다. 같은 변경이 포함된 CI 38142350756에서 실제 서비스 시작이 통과했지만, 38140520091의 실패가 바로 이 타이밍 때문이었다고 단정하지 않는다.

### CI 38142350756의 live CPU E2E

실행 시각은 2026-10-11 13:34 UTC 이후이며, 아래 값은 업로드된 `opereta-qemu-e2e-logs` 아티팩트(`result.json`과 단계별 출력)에서 읽었다. `result.json`은 `outcome: succeeded`, `kind: pass`, `accel: kvm`, 총 26.648초였다.

| 확인 | 실제 결과 |
|---|---|
| 사용자와 권한 | 비특권 `user`(uid 1000)가 `opereta` 그룹에 속했다. 제어 소켓은 `660 root:opereta`였다. 그룹 밖 사용자는 `permission denied`, 데몬이 없으면 `operetad is not running`을 받았다. |
| 모델 다운로드 | `ggml-org/tiny-llamas`의 `stories260K.gguf`(revision `99dd1a73db5a37100bd4ae633f4cfce6560e1567`) 1,185,376바이트가 약 0.5초에 내려왔다. SHA-256은 `047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b`로 고정값과 같았다. |
| 서버 계획 | GPU 없음(`no_gpu`)이라 `cpu_only`, ctx 128, batch 32, mmap이었다. 모델 학습 context가 128이라 ctx 256 override는 무시됐다. |
| 서버 시작 | 상태 `running_cpu_fallback`, 127.0.0.1:8080, `lan_exposed: false`. 유닛은 `ActiveState=active`, `Result=success`였다. |
| 채팅 | 16 토큰 응답을 받았다. 이 모델은 26만 파라미터급 테스트 모델이라 `tokens_per_second` 값(약 7,300)은 성능 지표가 아니다. |
| 데몬 재시작 | autostart를 끈 채로, 그리고 켠 채로 `operetad`를 각각 재시작했다. 두 경우 모두 같은 PID 1312, 같은 모델·ctx 128·batch 32의 서버를 채택했고 채팅이 계속 성공했다. 채택 사유는 CPU 전용 런타임 환경과 health 200 확인이었다. |
| 정상 중지 | `serve stop`이 `stopped`를 돌려줬고 유닛은 `inactive`(MainPID 0), 이후 채팅은 `server_not_running`이었다. |
| 데몬 메모리 | `operetad`의 VmRSS는 33,980 kB였다. 이는 데몬 프로세스 하나의 값이며 전체 OS 유휴 램이 아니다. |

이 실행의 `llama-server` 로그에는 `ggml_cuda_init: failed to initialize CUDA: no CUDA-capable device is detected`가 남았다. GPU가 없는 VM에서 CUDA 모듈이 로드에 실패하고 CPU로 동작한다는 설계된 경로이며 오류가 아니다.

알려진 사소한 결함이 하나 있다. context override가 무시될 때 경고 문구가 `allowed range is 256-128`처럼 최솟값과 최댓값 순서를 바꿔 출력한다. 동작에는 영향이 없다. 소스를 승인된 SHA 이후로 바꾸게 되므로 이번 검증 범위에서는 고치지 않았다.

HF 토큰 변경 뒤 CLI가 llama 서버 재시작을 안내하던 문제도 `5c4afbf`로 수정했다.
다운로드 클라이언트의 새 토큰 적용에는 데몬 재시작이 필요하므로 `sudo systemctl restart operetad`를 안내한다.
이 변경의 Debian 컨테이너 CLI/client gofmt, vet, race 검사가 통과했다.

## 검증 경계와 남은 항목

- ISO 안에서 live 사용자의 CPU 흐름(다운로드, SHA, 시작, 채팅, 데몬 재시작 채택, 정지)은 CI 38142350756에서 처음 끝까지 통과했다. 이 값은 KVM VM 한 번의 결과이며 반복 횟수나 안정성을 보장하지 않는다.
- 실 NVIDIA GPU에서의 CUDA 오프로드·추론은 검증하지 않았다. CUDA 모듈 컴파일, CUDA 없는 환경의 CPU 폴백, GPU 없는 VM에서의 `running_cpu_fallback`만 확인했다. 오프로드 검증(`offloaded N/M layers to GPU`, NVML 프로세스 목록)과 `opereta-llm`의 `/dev/nvidia*` 접근은 실기에서만 확인할 수 있다.
- Secure Boot, 실기 Wi-Fi, 실기 USB 부팅은 확인하지 않았다. CI의 USB 시험은 QEMU에서 ISO를 USB 저장 장치로 연결한 결과다.
- 유휴 전체 OS 메모리 사용량은 측정하지 않았다. 위 33,980 kB는 데몬 프로세스의 값이다.
- 제어된 fixture는 드라이버의 판정·시간 제한·정리만 검사하며 실제 ISO의 추론 성공으로 계산하지 않는다.
- CI 아티팩트(ISO 약 1.3 GB, E2E 로그)의 보존 기간은 7일이다. 이 ISO에는 NVIDIA 드라이버와 CUDA 런타임이 들어 있어 공개 저장소의 CI 아티팩트로 배포해도 되는지는 사용자 결정이 남아 있다. 재배포 조건의 해석 위험은 `docs/licenses.md`에 있다.

## 남은 실기 검증

RTX 3060 Laptop을 포함한 RTX 30/40/50의 NVIDIA 모듈 로드, CUDA 오프로드, 장치 권한, 토큰 속도, 온도와 전력은 실기가 필요하다.
Secure Boot, 실기 Wi-Fi·USB 부팅, 장시간 유휴 램 목표도 아직 확인하지 않았다.
GUI·설치기·업데이트는 후속 Phase이며 이 작업에서 시작하지 않는다.
Phase 3의 GUI(labwc/greetd/GTK), 기기별 Secure Boot MOK 절차는 남아 있다. 설치기, 커널·드라이버 업데이트/롤백, 자체 APT 저장소는 Phase 4 범위이며, 실기 지원 범위 확정과 릴리스 전 검증도 남아 있다.
