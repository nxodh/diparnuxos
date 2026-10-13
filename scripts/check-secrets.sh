#!/usr/bin/env bash
# 공개 저장소에 비밀 정보(API 키, 토큰, 개인 키, 비밀번호)가 들어가지 않았는지 추적 파일에서 검사한다.
# 시험용 가짜 값은 줄에 FAKE, EXAMPLE, SECRETTOKEN 같은 표시가 있거나 시험 파일에서 조립해 만든 것만 허용한다.
# 이 스크립트 자신이 걸리지 않도록 일부 패턴은 조각으로 나누어 만든다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

fail=0

# 허용 표시: 이 단어가 들어 있는 줄은 가짜 값으로 본다.
allow='PLAINKEY|UNLISTEDKEY|SECRETTOKEN|HFTOKEN|FAKE|fake|EXAMPLE|example|placeholder|changeme|Repeat\(|<redacted>|test-'

patterns=(
    'hf_[A-Za-z0-9]{30,}'                       # Hugging Face 토큰
    'dpx_[A-Za-z0-9_-]{40,}'                    # Diparnux API 키(접두사 + 43자)
    'sk-[A-Za-z0-9_-]{30,}'                     # OpenAI 형식 키
    'ghp_[A-Za-z0-9]{30,}'                      # GitHub 토큰
    'github_pat_[A-Za-z0-9_]{30,}'
    'AKIA[0-9A-Z]{16}'                          # AWS 액세스 키
    'xox[baprs]-[A-Za-z0-9-]{10,}'              # Slack 토큰
    'AIza[0-9A-Za-z_-]{30,}'                    # Google API 키
    '-----BEGIN [A-Z ]*PRIVATE KEY-----'        # PEM 개인 키
    'PrivateKey *= *[A-Za-z0-9+/]{42}[A-Za-z0-9+/=]' # WireGuard 개인 키
    'Authorization: *Bearer +[A-Za-z0-9._-]{20,}'
)

for p in "${patterns[@]}"; do
    hits="$(git grep -n -I -E -e "${p}" -- . ':!daemon/vendor' ':!scripts/check-secrets.sh' | grep -v -E "${allow}" || true)"
    if [ -n "${hits}" ]; then
        echo "check-secrets: possible secret (pattern ${p%% *}...):" >&2
        echo "${hits}" | sed -E 's/(.{120}).*/\1.../' | head -n 10 >&2
        fail=1
    fi
done

# 비밀 정보가 들어가기 쉬운 파일 이름은 추적하지 않는다. 공개 키(keys/*.asc, *.gpg)는 허용한다.
bad_files="$(git ls-files | grep -E '\.(pem|p12|pfx|psk|gguf|ovpn)$|(^|/)id_(rsa|ed25519|ecdsa)|(^|/)\.env$|(^|/)apikeys[^/]*\.json$|(^|/)wg[0-9]*\.conf$' || true)"
if [ -n "${bad_files}" ]; then
    echo "check-secrets: tracked files that must not be committed:" >&2
    echo "${bad_files}" | head -n 10 >&2
    fail=1
fi

if [ "${fail}" -ne 0 ]; then
    echo "check-secrets: FAILED. Remove the value, rotate it if it was real, and keep secrets out of this public repository." >&2
    exit 1
fi
echo "check-secrets: no secret-looking value in $(git ls-files | wc -l | tr -d ' ') tracked files"
