# Lightlix 빌드 진입점. 버전과 저장소 값은 lightlix.conf 에서만 읽는다.

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
LIGHTLIX_CONF := $(ROOT)/lightlix.conf
export LIGHTLIX_CONF

LIGHTLIX_VERSION := $(shell . "$(LIGHTLIX_CONF)" && printf '%s' "$$LIGHTLIX_VERSION")
ISO ?= $(ROOT)/out/lightlix-$(LIGHTLIX_VERSION)-amd64.iso

SHELL_SCRIPTS := $(shell find $(ROOT)/scripts $(ROOT)/build/auto $(ROOT)/build/config/hooks $(ROOT)/build/config/includes.chroot/lib/live/config $(ROOT)/packages $(ROOT)/tests $(ROOT)/llama $(ROOT)/daemon/dist -type f \( -name '*.sh' -o -path '*/build/auto/*' -o -path '*/lib/live/config/*' -o -name '*.hook.*' -o -name '*.preinst' -o -name '*.postinst' -o -name '*.prerm' -o -name '*.postrm' \) 2>/dev/null | sort)

.PHONY: iso iso-native deb llama test-llama-docker clean clean-llama lint test-daemon test-gui test-gpu-check test-nvidia-image-docker test-qemu test-qemu-docker test-qemu-usb-docker test-qemu-e2e test-qemu-e2e-docker test-qemu-e2e-driver

# Docker 안에서 ISO 를 빌드한다. lightlix-llm .deb 가 필요하므로 먼저 llama 를 실행한다.
iso: llama
	bash "$(ROOT)/scripts/build-in-docker.sh"

# Debian 13 호스트에서 root 로 ISO 를 직접 빌드한다.
iso-native:
	bash "$(ROOT)/scripts/build-iso.sh"

# packages/ 의 패키지를 .deb 로 빌드해 build/config/packages.chroot/ 에 넣는다.
deb:
	bash "$(ROOT)/scripts/build-debs.sh"

# llama.cpp 를 Docker 안에서 빌드해 out/llama/ 에 lightlix-llm .deb 를 만든다. 입력이 같으면 건너뛴다.
# 로컬 CPU 스모크 빌드: LLAMA_CUDA=OFF LLAMA_PLATFORM=linux/arm64 make llama (산출물은 검사 뒤 지운다. llama/README.md 참고)
llama:
	bash "$(ROOT)/scripts/build-llama-in-docker.sh"

# out/llama/ 의 .deb 를 CUDA 가 없는 Debian 컨테이너에서 검사한다(CPU 추론 포함).
test-llama-docker:
	bash "$(ROOT)/scripts/test-llama-in-docker.sh"

# 셸 스크립트를 검사한다. shellcheck 가 없으면 bash -n 으로 문법만 본다.
lint:
	@if [ -z "$(SHELL_SCRIPTS)" ]; then \
		echo "lint: no shell scripts found"; \
	elif command -v shellcheck >/dev/null 2>&1; then \
		echo "lint: checking with shellcheck"; \
		shellcheck $(SHELL_SCRIPTS); \
	else \
		echo "lint: shellcheck not found; checking syntax with bash -n"; \
		for f in $(SHELL_SCRIPTS); do bash -n "$$f" || exit 1; done; \
	fi

# Control Center 단위 테스트와 GTK 스모크 테스트를 Docker 의 xvfb 에서 실행한다.
test-gui:
	bash "$(ROOT)/scripts/test-gui-in-docker.sh"

# GPU 진단 분기를 명령 스텁으로 확인한다.
test-gpu-check:
	bash "$(ROOT)/tests/nvidia/gpu-check.sh"

# Go 모듈은 vendoring한 의존성만 쓰며 NVML에 필요한 cgo를 켠다.
test-daemon:
	cd "$(ROOT)/daemon" && CGO_ENABLED=1 go build -mod=vendor -trimpath -buildvcs=false ./...
	cd "$(ROOT)/daemon" && CGO_ENABLED=1 go vet -mod=vendor -buildvcs=false ./...
	cd "$(ROOT)/daemon" && CGO_ENABLED=1 go test -mod=vendor -buildvcs=false -count=1 ./...

# 완성된 ISO 안의 패키지, 모듈, 라이선스, 서명 키 제거를 확인한다.
test-nvidia-image-docker:
	bash "$(ROOT)/scripts/test-nvidia-image-in-docker.sh" "$(ISO)"

# ISO 의 로그인 프롬프트와 GPU 진단 줄(Lightlix GPU [no_gpu]:)을 QEMU 직렬 콘솔에서 확인한다.
test-qemu:
	bash "$(ROOT)/tests/qemu/boot-smoke.sh" "$(ISO)"

# 같은 확인을 Debian 컨테이너 안에서 한다. 호스트에 QEMU 가 없어도 된다.
test-qemu-docker:
	bash "$(ROOT)/scripts/test-qemu-in-docker.sh" "$(ISO)"

# ISO 를 USB 저장 장치로 붙여 부팅한다. 광학 드라이브가 없는 실기의 USB 기록 매체와 가깝다.
test-qemu-usb-docker:
	BOOT_MEDIA=usb bash "$(ROOT)/scripts/test-qemu-in-docker.sh" "$(ISO)"

# 완성된 ISO의 실제 CPU 추론을 직렬 콘솔로 확인한다.
test-qemu-e2e:
	bash "$(ROOT)/tests/qemu/cpu-inference.sh" "$(ISO)"

# Debian 컨테이너에서 같은 E2E를 실행한다.
test-qemu-e2e-docker:
	bash "$(ROOT)/scripts/test-qemu-e2e-in-docker.sh" "$(ISO)"

# ISO 없이 드라이버의 실패 판정과 정리를 확인한다.
test-qemu-e2e-driver:
	python3 -B "$(ROOT)/tests/qemu/test_cpu_inference.py"

# 산출물(out/)과 빌드된 .deb 를 지운다. out/llama 는 빌드가 오래 걸리므로 남긴다(clean-llama 로 지운다).
# 작업 디렉터리(/var/tmp/lightlix-build)는 지우지 않는다.
clean:
	@if [ -d "$(ROOT)/out" ]; then find "$(ROOT)/out" -mindepth 1 -maxdepth 1 ! -name llama -exec rm -rf {} +; fi
	find "$(ROOT)/build/config/packages.chroot" -name '*.deb' -delete

# llama 빌드 산출물(out/llama/)을 지운다. 다음 make llama 는 처음부터 다시 빌드한다.
clean-llama:
	rm -rf "$(ROOT)/out/llama"
