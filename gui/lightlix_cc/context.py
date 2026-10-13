"""페이지들이 함께 쓰는 상태: 데몬 클라이언트, 최근 상태, 토스트, 주기적 갱신."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib  # noqa: E402

from . import api  # noqa: E402
from .widgets import error_text, run_async  # noqa: E402

# 창이 활성일 때와 아닐 때의 상태 확인 간격(초). 보이지 않는 창이 3초마다 위젯을 다시 만들면 램이 계속 늘어난다.
POLL_SECONDS = 3
POLL_SECONDS_IDLE = 15


class Context:
    def __init__(self, client):
        self.client = client
        self.status = None
        self.status_error = None
        self.window = None
        self.toast_overlay = None
        self._listeners = []
        self._poll_id = 0
        self._interval = POLL_SECONDS
        self._busy = False

    # --- 알림 ---
    def toast(self, message, timeout=4):
        if self.toast_overlay is None:
            return
        toast = Adw.Toast(title=message)
        toast.set_timeout(timeout)
        self.toast_overlay.add_toast(toast)

    def toast_error(self, exc):
        self.toast(error_text(exc), timeout=6)

    # --- 상태 구독 ---
    def subscribe(self, callback):
        """상태가 갱신될 때마다 callback(status, error) 를 부른다."""
        self._listeners.append(callback)
        if self.status is not None or self.status_error is not None:
            callback(self.status, self.status_error)

    def refresh(self):
        if self._busy:
            return
        self._busy = True

        def done(status):
            self._busy = False
            self.status, self.status_error = status, None
            self._notify()

        def failed(exc):
            self._busy = False
            self.status_error = exc if isinstance(exc, api.DaemonError) else api.DaemonError("error", str(exc))
            self._notify()

        run_async(self.client.status, done, failed)

    def _notify(self):
        for cb in list(self._listeners):
            try:
                cb(self.status, self.status_error)
            except Exception:
                import traceback

                traceback.print_exc()

    def start_polling(self):
        if self._poll_id:
            return
        self.refresh()
        self._poll_id = GLib.timeout_add_seconds(self._interval, self._tick)

    def _tick(self):
        self.refresh()
        return GLib.SOURCE_CONTINUE

    def set_active(self, active):
        """창이 활성이면 빠르게, 아니면 느리게 확인한다. 폴링 중이 아니면 간격만 기억한다."""
        interval = POLL_SECONDS if active else POLL_SECONDS_IDLE
        if interval == self._interval:
            return
        self._interval = interval
        if self._poll_id:
            GLib.source_remove(self._poll_id)
            self._poll_id = GLib.timeout_add_seconds(self._interval, self._tick)
            if active:
                self.refresh()

    def stop_polling(self):
        if self._poll_id:
            GLib.source_remove(self._poll_id)
            self._poll_id = 0
