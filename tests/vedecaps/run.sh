#!/bin/sh
# 컨테이너 안에서 실행한다(scripts/test-vedecaps-in-docker.sh 가 부른다).
# 가짜 llama-server(127.0.0.1:18080), 게이트웨이 시험 프로그램(127.0.0.1:8080), 실제 Vedecaps 실행 파일을 함께 돌려
# Vedecaps 가 Diparnux 게이트웨이를 통해 응답을 받는지, 키 규칙이 설명대로인지 확인한다.
# 필요한 파일: /t/fake_llama.py, /bin/gwharness, /v/vedecaps(고정 버전)
set -u

HOME=/h
export HOME
mkdir -p /h/.config/vedecaps /tmp/work
python3 /t/fake_llama.py &
sleep 1

KEY=dpx_harnesskey0123456789abcdef0123456789
WRONG=dpx_wrongwrongwrongwrongwrongwrongwrong
fail=0

# scenario <이름> <Vedecaps 가 보내는 키(환경 변수)> <게이트웨이 환경 변수...>; 결과 출력은 /tmp/out 에 남는다.
scenario() {
    name="$1"
    client_key="$2"
    shift 2
    : > /tmp/fake-llama.log
    env "$@" /bin/gwharness &
    gw=$!
    sleep 1
    (cd /tmp/work && env DIPARNUX_API_KEY="$client_key" timeout 120 /v/vedecaps run -m diparnux/fake.gguf "say hi" >/tmp/out 2>&1)
    kill "$gw" 2>/dev/null
    wait "$gw" 2>/dev/null
    echo "== $name"
    sed 's/^/   /' /tmp/out | tail -5
}

expect() {
    if grep -q -- "$1" /tmp/out; then
        echo "   ok: $2"
    else
        echo "   FAIL: $2 (wanted '$1')"
        fail=1
    fi
}

expect_log() {
    if grep -q -- "$1" /tmp/fake-llama.log; then
        echo "   ok: $2"
    else
        echo "   FAIL: $2 (wanted '$1' in the model server log)"
        fail=1
    fi
}

# connect vedecaps 가 만드는 로컬 설정과 같은 모양이다(키 없음).
cat > /h/.config/vedecaps/vedecaps.json <<'JSON'
{
  "autoupdate": false,
  "model": "diparnux/fake.gguf",
  "provider": {"diparnux": {"api": "http://127.0.0.1:8080/v1", "name": "Diparnux", "protocol": "openai",
    "models": {"fake.gguf": {"limit": {"context": 16384, "output": 4096}, "name": "fake.gguf"}}}}
}
JSON

scenario "A: no key exists" "" HARNESS_LOCAL_NO_KEY=1
expect "Hello from the fake model." "the answer is shown"
expect_log "GET /props" "Vedecaps detected the server through /props"
expect_log "stream=True" "the reply was streamed"
expect_log "tools=[1-9]" "the tools were offered"

scenario "B: a key exists, local_no_key on" "" HARNESS_LOCAL_NO_KEY=1 HARNESS_KEY="$KEY"
expect "Hello from the fake model." "a keyless local request passes"

scenario "C: a key exists, local_no_key off, no key sent" "" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "a valid API key is required" "Vedecaps shows the gateway's error.message"

# 키가 필요한 설정(connect vedecaps --remote 와 같은 모양): 키는 환경 변수에서 읽는다.
python3 - <<'PY'
import json
p = "/h/.config/vedecaps/vedecaps.json"
d = json.load(open(p))
d["provider"]["diparnux"]["options"] = {"apiKey": "{env:DIPARNUX_API_KEY}"}
json.dump(d, open(p, "w"), indent=2)
PY

scenario "D: key required, right key in the environment" "$KEY" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "Hello from the fake model." "the key from the environment is accepted"
expect_log "auth=None" "the gateway did not forward the key to the model server"
if grep -q "dpx_" /tmp/fake-llama.log; then
    echo "   FAIL: a key reached the model server"
    fail=1
fi

scenario "E: key required, wrong key in the environment" "$WRONG" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "a valid API key is required" "a wrong key is refused"

if [ "$fail" -ne 0 ]; then
    echo "test-vedecaps: FAILED"
    exit 1
fi
echo "test-vedecaps: all scenarios passed"
