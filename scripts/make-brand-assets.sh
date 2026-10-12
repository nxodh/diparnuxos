#!/usr/bin/env bash
# 브랜드 이미지 자산을 컨테이너에서 다시 만든다(Pillow). 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v docker >/dev/null 2>&1 || { echo "make-brand-assets: docker is required" >&2; exit 2; }
docker run --rm -v "${ROOT}:/src" python:3.12-slim bash -euc '
pip install -q pillow >/dev/null 2>&1
cd /src
python3 scripts/make-brand-assets.py
'
