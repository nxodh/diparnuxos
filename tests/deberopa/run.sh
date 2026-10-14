#!/bin/sh
# 컨테이너 안에서 실행한다(scripts/test-deberopa-in-docker.sh 가 부른다).
# 가짜 llama-server(127.0.0.1:18080), 게이트웨이 시험 프로그램(127.0.0.1:8080), 실제 Deberopa 실행 파일을 함께 돌려
# Deberopa 가 Opereta 게이트웨이를 통해 응답을 받는지, 키 규칙이 설명대로인지 확인한다.
# 필요한 파일: /t/fake_llama.py, /bin/gwharness, /v/deberopa(고정 버전)
set -u

HOME=/h
export HOME
mkdir -p /h/.config/deberopa /tmp/work
python3 /t/fake_llama.py &
sleep 1

KEY=opr_harnesskey0123456789abcdef0123456789
WRONG=opr_wrongwrongwrongwrongwrongwrongwrong
fail=0

# scenario <이름> <Deberopa 가 보내는 키(환경 변수)> <게이트웨이 환경 변수...>; 결과 출력은 /tmp/out 에 남는다.
scenario() {
    name="$1"
    client_key="$2"
    shift 2
    : > /tmp/fake-llama.log
    env "$@" /bin/gwharness &
    gw=$!
    sleep 1
    (cd /tmp/work && env OPERETA_API_KEY="$client_key" timeout 120 /v/deberopa run -m opereta/fake.gguf "say hi" >/tmp/out 2>&1)
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

# connect deberopa 가 만드는 로컬 설정과 같은 모양이다(키 없음).
cat > /h/.config/deberopa/deberopa.json <<'JSON'
{
  "autoupdate": false,
  "model": "opereta/fake.gguf",
  "provider": {"opereta": {"api": "http://127.0.0.1:8080/v1", "name": "Opereta", "protocol": "openai",
    "models": {"fake.gguf": {"limit": {"context": 16384, "output": 4096}, "name": "fake.gguf"}}}}
}
JSON

scenario "A: no key exists" "" HARNESS_LOCAL_NO_KEY=1
expect "Hello from the fake model." "the answer is shown"
expect_log "GET /props" "Deberopa detected the server through /props"
expect_log "stream=True" "the reply was streamed"
expect_log "tools=[1-9]" "the tools were offered"

scenario "B: a key exists, local_no_key on" "" HARNESS_LOCAL_NO_KEY=1 HARNESS_KEY="$KEY"
expect "Hello from the fake model." "a keyless local request passes"

scenario "C: a key exists, local_no_key off, no key sent" "" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "a valid API key is required" "Deberopa shows the gateway's error.message"

# 키가 필요한 설정(connect deberopa --remote 와 같은 모양): 키는 환경 변수에서 읽는다.
python3 - <<'PY'
import json
p = "/h/.config/deberopa/deberopa.json"
d = json.load(open(p))
d["provider"]["opereta"]["options"] = {"apiKey": "{env:OPERETA_API_KEY}"}
json.dump(d, open(p, "w"), indent=2)
PY

scenario "D: key required, right key in the environment" "$KEY" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "Hello from the fake model." "the key from the environment is accepted"
expect_log "auth=None" "the gateway did not forward the key to the model server"
if grep -q "opr_" /tmp/fake-llama.log; then
    echo "   FAIL: a key reached the model server"
    fail=1
fi

scenario "E: key required, wrong key in the environment" "$WRONG" HARNESS_LOCAL_NO_KEY=0 HARNESS_KEY="$KEY"
expect "a valid API key is required" "a wrong key is refused"

# ---- 0.5.4: HTTPS(TLS)와 options.ca. Opereta 게이트웨이가 쓰는 certstore 의 실제 CA 와 서버 인증서를 쓴다. ----
# 서버 인증서 SAN 에는 box.local 과 127.0.0.1(IP)이 들어 있고 CA 에는 DNS 이름 제약만 있다. IP SAN 인증서가 CA 의 DNS 제약과 함께
# Deberopa(Bun)에서 통과하는지가 핵심이다(IP 제약이 있으면 UNSUPPORTED_CONSTRAINT_TYPE).
grep -q "box.local" /etc/hosts || echo "127.0.0.1 box.local" >> /etc/hosts
rm -rf /tmp/tls-main /tmp/tls-other
env HARNESS_TLS_DIR=/tmp/tls-other HARNESS_CA_OUT=/tmp/other-ca.pem HARNESS_ONLY_CA=1 /bin/gwharness

set_https() {
    # set_https <api 주소> [CA 파일; 빈 값이면 ca 를 적지 않는다]
    python3 - "$1" "${2:-}" <<'PY'
import json, sys
p = "/h/.config/deberopa/deberopa.json"
d = json.load(open(p))
prov = d["provider"]["opereta"]
prov["api"] = sys.argv[1]
opts = {}
if sys.argv[2]:
    opts["ca"] = sys.argv[2]
prov["options"] = opts
json.dump(d, open(p, "w"), indent=2)
PY
}

tls_scenario() {
    name="$1"
    shift
    scenario "$name" "" HARNESS_LOCAL_NO_KEY=1 HARNESS_TLS_DIR=/tmp/tls-main HARNESS_CA_OUT=/tmp/opr-ca.pem
}

# 이 시험은 CA 를 시나리오 시작 때 한 번 만들고 이후 같은 디렉터리를 재사용한다(같은 CA).
set_https "https://box.local:8444/v1" /tmp/opr-ca.pem
tls_scenario "F: https by name with the CA"
expect "Hello from the fake model." "https by DNS name with options.ca works"

set_https "https://127.0.0.1:8444/v1" /tmp/opr-ca.pem
tls_scenario "F2: https by IP address with the CA (IP SAN, DNS-only name constraint)"
expect "Hello from the fake model." "https by IP address works against a CA that has only DNS name constraints"

set_https "https://box.local:8444/v1" ""
tls_scenario "G: https without options.ca"
expect "tls ca" "the error tells where the CA comes from"
expect "opereta tls ca" "the hint names the current command"

set_https "https://box.local:8444/v1" /tmp/other-ca.pem
tls_scenario "H: https with another CA"
expect "tls ca" "a different CA is refused with the same hint"
expect "opereta tls ca" "the hint for another CA names the current command"

# 0.1.4 의 VE1/VE4: 키가 없는 이 컴퓨터의 요청으로 /opereta/info 를 읽어 Opereta 서버와 슬롯 수를 보여 준다.
echo "== J: deberopa debug local reads /opereta/info"
env HARNESS_LOCAL_NO_KEY=1 /bin/gwharness &
gw=$!
sleep 1
(cd /tmp/work && timeout 60 /v/deberopa debug local >/tmp/out 2>&1)
kill "$gw" 2>/dev/null
wait "$gw" 2>/dev/null
sed 's/^/   /' /tmp/out | tail -12
expect "Opereta 0.6.3" "the server is recognised as Opereta with its version"
expect "2 slots" "the slot count comes from /opereta/info"

if [ "$fail" -ne 0 ]; then
    echo "test-deberopa: FAILED"
    exit 1
fi
echo "test-deberopa: all scenarios passed"
