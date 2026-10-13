#!/usr/bin/env python3
"""Calamares 설정의 정합성을 확인한다. Calamares 패키지가 설치된 Debian 13 컨테이너에서 실행한다.

확인하는 것: YAML 구문, sequence 에 쓴 모듈과 인스턴스가 실제로 있는지, 인스턴스의 설정 파일이 있는지,
스키마가 있는 모듈 설정이 스키마를 통과하는지, 브랜딩의 이미지 파일, 전역 저장소 키의 연결.
"""

import glob
import os
import re
import sys

import yaml

try:
    import jsonschema
except ImportError:  # pragma: no cover
    jsonschema = None

ROOT = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src")
MODULE_DIRS = glob.glob("/usr/lib/*/calamares/modules") + glob.glob("/usr/lib/calamares/modules")
# Calamares 3.3.14 의 fstab 스키마는 모듈이 실제로 읽는 키(mountOptions 등)와 맞지 않는다. 업스트림 예제도 통과하지 못해 건너뛴다.
STALE_SCHEMAS = {"fstab"}
failures = []


def fail(message):
    failures.append(message)
    print("FAIL:", message)


def load(path):
    with open(path, encoding="utf-8") as f:
        try:
            return yaml.safe_load(f)
        except yaml.YAMLError as exc:
            fail(f"{path}: YAML error: {exc}")
            return None


def module_dir(name):
    for base in MODULE_DIRS:
        if os.path.isfile(os.path.join(base, name, "module.desc")):
            return os.path.join(base, name)
    return None


def check_password_quality(options):
    """users.conf 의 libpwquality 옵션으로 실제 비밀번호 검사를 돌린다(컨테이너에는 cracklib 사전이 없다).

    설정 없이는 "error loading dictionary" 로 거절되어야 하고(이 시험이 실제 문제를 재현하는지 확인한다),
    users.conf 의 옵션을 적용하면 규칙에 맞는 비밀번호가 통과해야 한다.
    """
    import shutil
    import subprocess

    if shutil.which("pwscore") is None:
        print("note: pwscore is not installed; password quality run skipped")
        return
    conf_dir = "/etc/security/pwquality.conf.d"
    conf = os.path.join(conf_dir, "diparnux-test.conf")
    sample = "Hel10-wOrld-77\n"

    def run():
        return subprocess.run(["pwscore"], input=sample, text=True, capture_output=True)

    if os.path.exists(conf):
        os.remove(conf)
    baseline = run()
    if baseline.returncode == 0 or "dictionary" not in (baseline.stdout + baseline.stderr):
        print("note: the container has a cracklib dictionary; the negative control was skipped")
    os.makedirs(conf_dir, exist_ok=True)
    try:
        with open(conf, "w", encoding="utf-8") as fh:
            for item in options:
                fh.write(item.replace("=", " = ", 1) + "\n")
        applied = run()
    finally:
        if os.path.exists(conf):
            os.remove(conf)
    if applied.returncode != 0:
        fail(f"a valid password is rejected with the users.conf options: {(applied.stdout + applied.stderr).strip()}")
    else:
        print("ok: users.conf options accept a valid password without a cracklib dictionary")


def main():
    if not MODULE_DIRS:
        print("FAIL: the calamares package is not installed in this container")
        return 2
    settings = load(os.path.join(ROOT, "settings.conf"))
    for path in glob.glob(os.path.join(ROOT, "modules", "*.conf")):
        load(path)
    branding = load(os.path.join(ROOT, "branding", "branding.desc"))
    if not settings:
        return 1

    instances = {}
    for inst in settings.get("instances") or []:
        mod, ident, cfg = inst.get("module"), inst.get("id"), inst.get("config")
        if not module_dir(mod):
            fail(f"instance {ident}: module {mod} does not exist")
        if not cfg or not os.path.isfile(os.path.join(ROOT, "modules", cfg)):
            fail(f"instance {ident}: config file {cfg} is missing")
        instances[f"{mod}@{ident}"] = cfg

    for phase in settings.get("sequence") or []:
        for kind, names in phase.items():
            if kind not in ("show", "exec"):
                fail(f"unknown sequence phase {kind}")
            used = set()
            for name in names:
                if name in used:
                    fail(f"module {name} appears twice in one {kind} phase")
                used.add(name)
                if "@" in name:
                    if name not in instances:
                        fail(f"{name} is used but not defined in instances")
                    continue
                if not module_dir(name):
                    fail(f"module {name} does not exist in the Calamares package")
                    continue
                cfg = os.path.join(ROOT, "modules", name + ".conf")
                # 구성 파일이 없는 모듈은 Calamares 의 기본값을 쓴다. 쓰는 모듈은 아래 목록에 있는 것만 허용한다.
                if not os.path.isfile(cfg) and name not in ("summary", "keyboard", "locale", "localecfg", "hwclock", "umount", "partition"):
                    fail(f"module {name} has no configuration file in {ROOT}/modules")

    # 스키마가 있는 모듈은 설정을 검증한다.
    if jsonschema is None:
        print("note: python3-jsonschema is missing; schema checks skipped")
    else:
        for path in glob.glob(os.path.join(ROOT, "modules", "*.conf")):
            name = os.path.basename(path)[:-5]
            base = name
            for inst in settings.get("instances") or []:
                if inst.get("config") == os.path.basename(path):
                    base = inst["module"]
            d = module_dir(base)
            schema_path = os.path.join(d, base + ".schema.yaml") if d else None
            if base in STALE_SCHEMAS:
                print("note: schema of", base, "is stale upstream; skipped")
                continue
            if schema_path and os.path.isfile(schema_path):
                with open(schema_path, encoding="utf-8") as f:
                    schema = yaml.safe_load(f)
                try:
                    jsonschema.validate(load(path), schema)
                    print("ok: schema", os.path.basename(path))
                except jsonschema.ValidationError as exc:
                    fail(f"{os.path.basename(path)} violates its schema: {exc.message}")

    if branding:
        if branding.get("componentName") != settings.get("branding"):
            fail("branding componentName does not match settings.conf branding")
        for key, value in (branding.get("images") or {}).items():
            if not os.path.isfile(os.path.join(ROOT, "branding", value)):
                fail(f"branding image {key} -> {value} is missing")
        slideshow = branding.get("slideshow")
        if slideshow and not os.path.isfile(os.path.join(ROOT, "branding", slideshow)):
            fail(f"branding slideshow {slideshow} is missing")

    # 전역 저장소 키가 선택 모듈의 인스턴스 id 와 이어져 있는지 본다.
    prepare = open(os.path.join(ROOT, "modules", "diparnux-prepare.conf"), encoding="utf-8").read()
    m = re.search(r"gs\[packagechooser_([a-z0-9]+)\]", prepare)
    if not m or f"packagechooser@{m.group(1)}" not in instances:
        fail("diparnux-prepare.conf does not read the key of the mode chooser instance")

    # 이미지에는 cracklib 사전이 없다. 사전 검사가 켜져 있으면 비밀번호가 항상 거절된다("error loading dictionary").
    users = yaml.safe_load(open(os.path.join(ROOT, "modules", "users.conf"), encoding="utf-8"))
    options = (users.get("passwordRequirements") or {}).get("libpwquality") or []
    if "dictcheck=0" not in options:
        fail("users.conf must set dictcheck=0 (the image has no cracklib dictionary)")
    check_password_quality(options)

    if failures:
        print(f"{len(failures)} problem(s)")
        return 1
    print("calamares configuration: all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
