"""모델 페이지: 추천, 설치된 모델, 내려받기, Hugging Face 검색."""

import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gio, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import (  # noqa: E402
    action_row,
    clear_group,
    confirm,
    error_text,
    icon_button,
    run_async,
    text_button,
)


class ModelsPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Models"), icon_name="folder-download-symbolic")
        self.ctx = ctx
        self._installed = []
        self._jobs = {}  # job id -> (row, bar)
        self._job_rows = []
        self._rec_rows = []
        self._installed_rows = []
        self._search_rows = []

        self.rec_group = Adw.PreferencesGroup(title=_("Recommended for this machine"))
        self.rec_group.set_description(_("An estimate from the file size; the real plan is computed after the download."))
        self.installed_group = Adw.PreferencesGroup(title=_("Installed models"))
        self.installed_group.set_header_suffix(text_button(_("Import a file"), self.import_file))
        self.jobs_group = Adw.PreferencesGroup(title=_("Downloads"))
        self.search_group = Adw.PreferencesGroup(title=_("Search Hugging Face"))
        self.token_group = Adw.PreferencesGroup(title=_("Hugging Face access"))
        for g in (self.rec_group, self.installed_group, self.jobs_group, self.search_group, self.token_group):
            self.add(g)

        self.search_entry = Adw.EntryRow(title=_("Search GGUF models"))
        self.search_entry.set_show_apply_button(True)
        self.search_entry.connect("apply", lambda *_a: self.search(self.search_entry.get_text()))
        self.search_group.add(self.search_entry)

        self.token_entry = Adw.PasswordEntryRow(title=_("Access token (for gated models)"))
        self.token_entry.set_show_apply_button(True)
        self.token_entry.connect("apply", lambda *_a: self.save_token())
        self.token_group.add(self.token_entry)
        self.token_group.set_description(_("The token is stored by the daemon. Restart diparnuxd for it to take effect."))

        self.reload()

    # --- 목록 ---
    def reload(self):
        run_async(self.ctx.client.models, self._on_models, self.ctx.toast_error)
        run_async(self.ctx.client.recommendations, self._on_recommendations, lambda e: None)

    def _on_models(self, result):
        self._installed = result.get("models") or []
        clear_group(self.installed_group, self._installed_rows)
        if not self._installed:
            row = action_row(_("No models installed"), _("Download one from the recommendations or search below."))
            self.installed_group.add(row)
            self._installed_rows.append(row)
        for m in self._installed:
            sub = [logic.fmt_bytes(m.get("size_bytes")), m.get("architecture", ""), m.get("license", "")]
            if m.get("incomplete"):
                sub.append(_("incomplete"))
            row = action_row(m.get("id", "?"), " · ".join(s for s in sub if s))
            if m.get("default"):
                row.add_suffix(Gtk.Label(label=_("Default"), css_classes=["accent"]))
            else:
                row.add_suffix(text_button(_("Set default"), lambda mid=m["id"]: self.set_default(mid)))
            row.add_suffix(icon_button("user-trash-symbolic", _("Delete"), lambda mid=m["id"]: self.delete(mid), "destructive-action"))
            self.installed_group.add(row)
            self._installed_rows.append(row)

    def _on_recommendations(self, result):
        clear_group(self.rec_group, self._rec_rows)
        hw = result.get("hardware") or {}
        basis = result.get("basis")
        if basis == "gpu":
            head = _("Based on %s of GPU memory") % logic.fmt_mib(hw.get("vram_mib", -1))
        elif basis == "cpu":
            head = _("No usable GPU: based on %s of system memory") % logic.fmt_mib(hw.get("ram_mib", -1))
        else:
            head = _("Hardware could not be read")
        row = action_row(head, "")
        self.rec_group.add(row)
        self._rec_rows.append(row)
        best = result.get("recommended", -1)
        for i, m in enumerate(result.get("models") or []):
            fits = m.get("fits")
            tags = [m.get("params", ""), logic.fmt_mib(m.get("size_mib", -1)), m.get("license", "")]
            tags.append(_("fits") if fits else _("too large"))
            title = m.get("name", m.get("repo", "?"))
            if i == best:
                title += "  ★"
            r = action_row(title, " · ".join(t for t in tags if t))
            r.add_suffix(text_button(_("Download"), lambda m=m: self.pull(m["repo"], m["file"])))
            self.rec_group.add(r)
            self._rec_rows.append(r)

    # --- 동작 ---
    def set_default(self, model_id):
        run_async(
            lambda: self.ctx.client.set_default_model(model_id),
            lambda _r: (self.ctx.toast(_("Default model set")), self.reload()),
            self.ctx.toast_error,
        )

    def delete(self, model_id):
        def go():
            run_async(
                lambda: self.ctx.client.delete_model(model_id),
                lambda _r: (self.ctx.toast(_("Model deleted")), self.reload()),
                self.ctx.toast_error,
            )

        confirm(self.ctx.window, _("Delete this model?"), model_id, _("Delete"), go, destructive=True)

    def pull(self, repo, file, revision=""):
        def started(job):
            self.ctx.toast(_("Download started"))
            self._track_job(job)

        run_async(lambda: self.ctx.client.pull(repo, file, revision), started, self.ctx.toast_error)

    # --- 파일 가져오기 ---
    def import_file(self):
        """내려받아 둔 .gguf 파일을 골라 모델로 가져온다."""
        dialog = Gtk.FileDialog(title=_("Choose a GGUF model file"))
        flt = Gtk.FileFilter()
        flt.set_name(_("GGUF model files"))
        flt.add_pattern("*.gguf")
        filters = Gio.ListStore.new(Gtk.FileFilter)
        filters.append(flt)
        dialog.set_filters(filters)

        def chosen(dlg, result):
            try:
                gfile = dlg.open_finish(result)
            except GLib.Error:
                return  # 사용자가 닫았다.
            path = gfile.get_path() if gfile is not None else None
            if path:
                self.start_import(path)

        dialog.open(self.ctx.window, None, chosen)

    def start_import(self, path):
        name = os.path.basename(path)
        bar = Gtk.ProgressBar(valign=Gtk.Align.CENTER, hexpand=False)
        bar.set_size_request(160, -1)
        row = action_row(name, _("Importing"))
        row.add_suffix(bar)
        self.jobs_group.add(row)
        self._job_rows.append(row)

        def progress(done, total):
            GLib.idle_add(self._import_progress, row, bar, done, total)

        def finished(_result):
            self._drop_row(row)
            self.ctx.toast(_("Imported %s") % name)
            self.reload()

        def failed(exc):
            self._drop_row(row)
            self.ctx.toast_error(exc)

        run_async(lambda: self.ctx.client.import_model(path, name, progress), finished, failed)

    def _import_progress(self, row, bar, done, total):
        bar.set_fraction(done / total if total else 0.0)
        row.set_subtitle(_("Importing") + " · %s / %s" % (logic.fmt_bytes(done), logic.fmt_bytes(total)))
        return GLib.SOURCE_REMOVE

    def _drop_row(self, row):
        if row in self._job_rows:
            self._job_rows.remove(row)
            self.jobs_group.remove(row)

    def save_token(self):
        token = self.token_entry.get_text().strip()
        if not token:
            return
        run_async(
            lambda: self.ctx.client.set_config(hf_token=token),
            lambda r: (
                self.token_entry.set_text(""),
                self.ctx.toast(_("Token saved. Restart diparnuxd to use it.") if r.get("restart_required") else _("Token saved")),
            ),
            self.ctx.toast_error,
        )

    # --- 내려받기 진행률 ---
    def _track_job(self, job):
        jid = job.get("id")
        if not jid or jid in self._jobs:
            return
        bar = Gtk.ProgressBar(valign=Gtk.Align.CENTER, hexpand=False)
        bar.set_size_request(160, -1)
        row = action_row(job.get("file", jid), job.get("repo", ""))
        row.add_suffix(bar)
        row.add_suffix(icon_button("process-stop-symbolic", _("Cancel"), lambda: self._cancel(jid)))
        self.jobs_group.add(row)
        self._job_rows.append(row)
        self._jobs[jid] = (row, bar)
        GLib.timeout_add_seconds(1, self._poll_job, jid)

    def _cancel(self, jid):
        run_async(lambda: self.ctx.client.cancel_job(jid), None, self.ctx.toast_error)

    def _poll_job(self, jid):
        entry = self._jobs.get(jid)
        if entry is None:
            return GLib.SOURCE_REMOVE
        run_async(lambda: self.ctx.client.job(jid), lambda j: self._apply_job(jid, j), lambda e: self._job_failed(jid, e))
        return GLib.SOURCE_CONTINUE

    def _apply_job(self, jid, job):
        entry = self._jobs.get(jid)
        if entry is None:
            return
        row, bar = entry
        bar.set_fraction(logic.progress_fraction(job))
        total = job.get("bytes_total") or 0
        row.set_subtitle("%s · %s / %s" % (job.get("repo", ""), logic.fmt_bytes(job.get("bytes_done")), logic.fmt_bytes(total)))
        if not logic.job_is_final(job):
            return
        self._finish_job(jid)
        if job.get("state") == "done":
            self.ctx.toast(_("Downloaded %s") % job.get("model_id", job.get("file", "")))
            self.reload()
        elif job.get("state") == "failed":
            self.ctx.toast(_("Download failed: %s") % (job.get("error") or job.get("error_code", "")), timeout=8)
        else:
            self.ctx.toast(_("Download canceled"))

    def _job_failed(self, jid, exc):
        self._finish_job(jid)
        self.ctx.toast_error(exc)

    def _finish_job(self, jid):
        entry = self._jobs.pop(jid, None)
        if entry is None:
            return
        row = entry[0]
        if row in self._job_rows:
            self._job_rows.remove(row)
            self.jobs_group.remove(row)

    # --- 검색 ---
    def search(self, query):
        query = query.strip()
        if not query:
            return
        run_async(lambda: self.ctx.client.search_models(query), self._show_hits, self.ctx.toast_error)

    def _show_hits(self, result):
        clear_group(self.search_group, self._search_rows)
        hits = result.get("results") or []
        if not hits:
            row = action_row(_("No results"))
            self.search_group.add(row)
            self._search_rows.append(row)
        for h in hits:
            tags = [_("%d downloads") % h.get("downloads", 0)]
            if h.get("license"):
                tags.append(h["license"])
            if h.get("gated"):
                tags.append(_("gated"))
            row = action_row(h.get("id", "?"), " · ".join(tags), activatable=True)
            row.add_suffix(Gtk.Image.new_from_icon_name("go-next-symbolic"))
            row.connect("activated", lambda _r, repo=h.get("id"): self._open_repo(repo))
            self.search_group.add(row)
            self._search_rows.append(row)

    def _open_repo(self, repo):
        run_async(lambda: self.ctx.client.remote_files(repo), lambda r: self._show_files(r), self.ctx.toast_error)

    def _show_files(self, remote):
        clear_group(self.search_group, self._search_rows)
        back = action_row(_("Back to results"), remote.get("repo", ""), activatable=True)
        back.add_prefix(Gtk.Image.new_from_icon_name("go-previous-symbolic"))
        back.connect("activated", lambda *_a: self.search(self.search_entry.get_text()))
        self.search_group.add(back)
        self._search_rows.append(back)
        if remote.get("gated"):
            note = action_row(
                _("This repository is gated"),
                _("Accept its terms on Hugging Face and save an access token above before downloading."),
            )
            self.search_group.add(note)
            self._search_rows.append(note)
        files = remote.get("files") or []
        if not files:
            row = action_row(_("No GGUF files in this repository"))
            self.search_group.add(row)
            self._search_rows.append(row)
        for f in files:
            sub = [logic.fmt_bytes(f.get("size")), remote.get("license", "")]
            row = action_row(f.get("path", "?"), " · ".join(s for s in sub if s))
            row.add_suffix(
                text_button(
                    _("Download"),
                    lambda f=f, repo=remote.get("repo"), rev=remote.get("revision", ""): self.pull(repo, f["path"], rev),
                )
            )
            self.search_group.add(row)
            self._search_rows.append(row)
