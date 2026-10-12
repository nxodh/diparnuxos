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
    prepare = open(os.path.join(ROOT, "modules", "lightlix-prepare.conf"), encoding="utf-8").read()
    m = re.search(r"gs\[packagechooser_([a-z0-9]+)\]", prepare)
    if not m or f"packagechooser@{m.group(1)}" not in instances:
        fail("lightlix-prepare.conf does not read the key of the mode chooser instance")

    if failures:
        print(f"{len(failures)} problem(s)")
        return 1
    print("calamares configuration: all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
