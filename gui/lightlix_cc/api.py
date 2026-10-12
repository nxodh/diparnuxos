"""lightlixd 의 유닉스 소켓 REST API 클라이언트. 표준 라이브러리만 쓴다.

GUI 는 데몬하고만 통신한다. root 권한이 필요한 일은 모두 데몬이 처리한다.
이 모듈은 GTK 에 의존하지 않으므로 단독으로 테스트할 수 있다.
"""

import http.client
import json
import socket
import urllib.parse

DEFAULT_SOCKET = "/run/lightlix/lightlixd.sock"
# 모델 내려받기 시작이나 서버 시작처럼 오래 걸릴 수 있는 호출의 제한 시간이다.
SLOW_TIMEOUT = 300.0
DEFAULT_TIMEOUT = 15.0


class DaemonError(Exception):
    """데몬이 돌려준 오류. code 는 짧은 식별자이고 message 는 영어 설명이다."""

    def __init__(self, code, message, status=0):
        super().__init__("%s: %s" % (code, message))
        self.code = code
        self.message = message
        self.status = status


class DaemonUnavailable(DaemonError):
    """데몬에 연결하지 못했다. code 는 'unavailable' 또는 'permission_denied' 이다."""


class _UnixConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout):
        super().__init__("localhost", timeout=timeout)
        self._path = path

    def connect(self):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        try:
            sock.connect(self._path)
        except BaseException:
            sock.close()
            raise
        self.sock = sock


class Client:
    def __init__(self, socket_path=DEFAULT_SOCKET, timeout=DEFAULT_TIMEOUT):
        self.socket_path = socket_path
        self.timeout = timeout

    # --- 저수준 ---
    def _request(self, method, path, body=None, timeout=None, raw=False):
        conn = _UnixConnection(self.socket_path, timeout or self.timeout)
        headers = {"Accept": "application/json"}
        payload = None
        if body is not None:
            payload = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        try:
            try:
                conn.request(method, path, body=payload, headers=headers)
                resp = conn.getresponse()
                data = resp.read()
            except PermissionError as exc:
                raise DaemonUnavailable(
                    "permission_denied",
                    "permission denied on %s; add your user to the 'lightlix' group" % self.socket_path,
                ) from exc
            except (FileNotFoundError, ConnectionRefusedError) as exc:
                raise DaemonUnavailable("unavailable", "lightlixd is not running") from exc
            except (OSError, http.client.HTTPException) as exc:
                raise DaemonUnavailable("unavailable", "cannot talk to lightlixd: %s" % exc) from exc
        finally:
            conn.close()

        text = data.decode("utf-8", errors="replace")
        if resp.status >= 400:
            code, message = "http_%d" % resp.status, text.strip()[:300]
            try:
                err = json.loads(text)["error"]
                code, message = str(err.get("code", code)), str(err.get("message", message))
            except (ValueError, KeyError, TypeError, AttributeError):
                pass
            raise DaemonError(code, message, resp.status)
        if raw:
            return text
        if not text.strip():
            return {}
        try:
            return json.loads(text)
        except ValueError as exc:
            raise DaemonError("bad_response", "the daemon returned a response that is not JSON", resp.status) from exc

    @staticmethod
    def _query(path, **params):
        items = {k: v for k, v in params.items() if v is not None}
        if not items:
            return path
        return path + "?" + urllib.parse.urlencode(items)

    # --- 상태 ---
    def status(self):
        return self._request("GET", "/v1/status")

    def gpu_check(self):
        return self._request("POST", "/v1/gpu/check", timeout=150.0)

    def report(self):
        return self._request("GET", "/v1/report", raw=True, timeout=60.0)

    # --- 모델 ---
    def models(self):
        return self._request("GET", "/v1/models")

    def delete_model(self, model_id):
        return self._request("DELETE", "/v1/models/" + urllib.parse.quote(model_id, safe=""))

    def set_default_model(self, model_id):
        return self._request("PUT", "/v1/models/default", {"id": model_id})

    def search_models(self, query, limit=20):
        return self._request("GET", self._query("/v1/models/search", q=query, limit=limit), timeout=40.0)

    def remote_files(self, repo, revision=None):
        return self._request("GET", self._query("/v1/models/remote", repo=repo, revision=revision), timeout=40.0)

    def pull(self, repo, file, revision=""):
        return self._request("POST", "/v1/models/pull", {"repo": repo, "file": file, "revision": revision})

    def job(self, job_id):
        return self._request("GET", "/v1/jobs/" + urllib.parse.quote(job_id, safe=""))

    def cancel_job(self, job_id):
        return self._request("DELETE", "/v1/jobs/" + urllib.parse.quote(job_id, safe=""))

    def recommendations(self):
        return self._request("GET", "/v1/recommendations")

    # --- 서버 ---
    def plan(self, model="", **overrides):
        return self._request("GET", self._query("/v1/server/plan", model=model or None, **overrides))

    def start(self, model, overrides=None, wait=False):
        body = {"model": model, "overrides": overrides or {}, "wait": wait}
        return self._request("POST", "/v1/server/start", body, timeout=SLOW_TIMEOUT if wait else None)

    def stop(self):
        return self._request("POST", "/v1/server/stop", timeout=60.0)

    def logs(self, lines=200):
        return self._request("GET", self._query("/v1/server/logs", lines=lines))

    def chat(self, messages, max_tokens=256):
        return self._request(
            "POST", "/v1/server/chat", {"messages": messages, "max_tokens": max_tokens}, timeout=180.0
        )

    # --- 설정 ---
    def config(self):
        return self._request("GET", "/v1/config")

    def set_config(self, **patch):
        return self._request("PUT", "/v1/config", patch)

    def api_keys(self):
        return self._request("GET", "/v1/apikeys")

    def create_key(self, label):
        return self._request("POST", "/v1/apikeys", {"label": label})

    def delete_key(self, key_id):
        return self._request("DELETE", "/v1/apikeys/" + urllib.parse.quote(key_id, safe=""))

    # --- 시스템 ---
    def mode(self):
        return self._request("GET", "/v1/mode")

    def set_mode(self, mode):
        return self._request("POST", "/v1/mode", {"mode": mode}, timeout=SLOW_TIMEOUT)

    def ssh(self):
        return self._request("GET", "/v1/system/ssh")

    def set_ssh(self, enabled):
        return self._request("PUT", "/v1/system/ssh", {"enabled": bool(enabled)}, timeout=60.0)

    def update(self):
        return self._request("POST", "/v1/update")

    # --- 첫 부팅 ---
    def firstboot(self):
        return self._request("GET", "/v1/firstboot")

    def complete_firstboot(self, skipped=()):
        return self._request("PUT", "/v1/firstboot", {"done": True, "skipped": list(skipped)})

    def reset_firstboot(self):
        return self._request("DELETE", "/v1/firstboot")
