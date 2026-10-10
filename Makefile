# Lightlix 빌드 진입점. 버전과 저장소 값은 lightlix.conf 에서만 읽는다.

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
LIGHTLIX_CONF := $(ROOT)/lightlix.conf
export LIGHTLIX_CONF

LIGHTLIX_VERSION := $(shell . "$(LIGHTLIX_CONF)" && printf '%s' "$$LIGHTLIX_VERSION")
ISO ?= $(ROOT)/out/lightlix-$(LIGHTLIX_VERSION)-amd64.iso

SHELL_SCRIPTS := $(shell find $(ROOT)/scripts $(ROOT)/build/auto $(ROOT)/build/config/hooks $(ROOT)/packages $(ROOT)/tests -type f \( -name '*.sh' -o -path '*/build/auto/*' -o -name '*.hook.*' -o -name '*.preinst' -o -name '*.postinst' -o -name '*.prerm' -o -name '*.postrm' \) 2>/dev/null | sort)

.PHONY: iso iso-native deb lint test-gpu-check test-nvidia-image-docker test-qemu test-qemu-docker clean

# Docker 안에서 ISO 를 빌드한다.
iso:
	bash "$(ROOT)/scripts/build-in-docker.sh"

# Debian 13 호스트에서 root 로 ISO 를 직접 빌드한다.
iso-native:
	bash "$(ROOT)/scripts/build-iso.sh"

# packages/ 의 패키지를 .deb 로 빌드해 build/config/packages.chroot/ 에 넣는다.
deb:
	bash "$(ROOT)/scripts/build-debs.sh"

# 셸 스크립트를 검사한다. shellcheck 가 없으면 bash -n 으로 문법만 본다.
lint:
	@if [ -z "$(SHELL_SCRIPTS)" ]; then \
		echo "lint: 검사할 셸 스크립트가 없다"; \
	elif command -v shellcheck >/dev/null 2>&1; then \
		echo "lint: shellcheck 으로 검사한다"; \
		shellcheck $(SHELL_SCRIPTS); \
	else \
		echo "lint: shellcheck 가 없어 bash -n 으로 문법만 검사한다"; \
		for f in $(SHELL_SCRIPTS); do bash -n "$$f" || exit 1; done; \
	fi

# GPU 진단 분기를 명령 스텁으로 확인한다.
test-gpu-check:
	bash "$(ROOT)/tests/nvidia/gpu-check.sh"

# 완성된 ISO 안의 패키지, 모듈, 라이선스, 서명 키 제거를 확인한다.
test-nvidia-image-docker:
	bash "$(ROOT)/scripts/test-nvidia-image-in-docker.sh" "$(ISO)"

# ISO 의 로그인 프롬프트와 GPU 없는 CPU 폴백 안내를 QEMU 로 확인한다.
test-qemu:
	bash "$(ROOT)/tests/qemu/boot-smoke.sh" "$(ISO)"

# 같은 확인을 Debian 컨테이너 안에서 한다. 호스트에 QEMU 가 없어도 된다.
test-qemu-docker:
	bash "$(ROOT)/scripts/test-qemu-in-docker.sh" "$(ISO)"

# 산출물(out/)과 빌드된 .deb 를 지운다. 작업 디렉터리(/var/tmp/lightlix-build)는 지우지 않는다.
clean:
	rm -rf "$(ROOT)/out"
	find "$(ROOT)/build/config/packages.chroot" -name '*.deb' -delete
