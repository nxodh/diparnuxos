#!/usr/bin/env python3
"""ISO 에 들어간 패키지의 대응 소스를 Debian 저장소에서 받아 릴리스에 올릴 묶음으로 만든다.

GPL 등이 요구하는 "대응하는 소스 코드를 함께 제공"(GPLv2 3a)을 이행한다. 컨테이너(debian:trixie) 안에서 실행한다
(scripts/collect-sources-in-docker.sh). 입력은 build-iso.sh 가 만든 package-manifest.tsv 이다.

  collect-sources.py --manifest FILE --out DIR --prefix opereta-0.6.1-sources [--max-part-mib 1800]

규칙(실패를 닫는 방식이다. 하나라도 받지 못하면 실패한다).
  - 이 프로젝트의 패키지(관리자가 Opereta Developers)는 소스가 이 저장소(릴리스의 소스 묶음)에 있다.
  - NVIDIA 저장소의 독점 패키지(관리자가 nvidia.com)는 소스 제공 의무가 없다. 오픈 모듈의 소스는 DKMS 패키지가 이미지 안에
    함께 가지고 있다(/usr/src). 두 경우 모두 목록에 이유와 함께 기록한다.
  - 나머지(Debian)는 소스 패키지를 정확한 버전으로 받는다. apt(서명 검증)를 먼저 쓰고, 저장소에서 사라졌으면
    snapshot.debian.org 에서 받아 .dsc 의 SHA-256 으로 확인한다.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.request

SNAPSHOT = "https://snapshot.debian.org"


def die(msg):
    print("collect-sources: ERROR: " + msg, file=sys.stderr)
    sys.exit(1)


def classify(maintainer, package):
    m = maintainer.lower()
    if "opereta developers" in m or package.startswith("opereta-"):
        return "opereta"
    if "nvidia.com" in m:
        return "nvidia"
    return "debian"


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def dsc_files(dsc_path):
    """.dsc 의 Checksums-Sha256 에서 (이름, 해시) 목록을 돌려준다."""
    text = open(dsc_path, encoding="utf-8", errors="replace").read()
    m = re.search(r"^Checksums-Sha256:\n((?: .*\n)+)", text, re.M)
    if not m:
        return []
    out = []
    for line in m.group(1).splitlines():
        parts = line.split()
        if len(parts) == 3:
            out.append((parts[2], parts[0]))
    return out


def fetch_apt(src, ver, dest):
    os.makedirs(dest, exist_ok=True)
    r = subprocess.run(["apt-get", "source", "--download-only", "-q", f"{src}={ver}"], cwd=dest,
                       capture_output=True, text=True)
    return r.returncode == 0, (r.stderr or r.stdout)[-400:]


def fetch_snapshot(src, ver, dest):
    """snapshot.debian.org 에서 소스 파일을 받는다. .dsc 가 적은 SHA-256 으로 모든 파일을 확인한다."""
    os.makedirs(dest, exist_ok=True)
    url = f"{SNAPSHOT}/mr/package/{src}/{ver}/srcfiles?fileinfo=1"
    try:
        with urllib.request.urlopen(url, timeout=60) as r:
            info = json.load(r)
    except Exception as e:  # noqa: BLE001
        return False, f"snapshot lookup failed: {e}"
    names = {}
    for h, entries in info.get("fileinfo", {}).items():
        for e in entries:
            names[e["name"]] = h
    dscs = [n for n in names if n.endswith(".dsc")]
    if not dscs:
        return False, "snapshot has no .dsc for this version"
    for name, h in names.items():
        p = os.path.join(dest, name)
        if os.path.exists(p):
            continue
        try:
            urllib.request.urlretrieve(f"{SNAPSHOT}/file/{h}", p)
        except Exception as e:  # noqa: BLE001
            return False, f"download of {name} failed: {e}"
    dsc = os.path.join(dest, dscs[0])
    for name, want in dsc_files(dsc):
        p = os.path.join(dest, name)
        if not os.path.exists(p):
            return False, f"file listed in the .dsc is missing: {name}"
        if sha256(p) != want:
            return False, f"checksum mismatch: {name}"
    return True, ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--prefix", required=True)
    ap.add_argument("--max-part-mib", type=int, default=1800)
    ap.add_argument("--no-apt", action="store_true", help="skip apt and use snapshot only (tests)")
    a = ap.parse_args()

    rows = []
    with open(a.manifest, encoding="utf-8") as f:
        for line in f:
            p = line.rstrip("\n").split("\t")
            if len(p) < 6:
                die(f"bad manifest line: {line!r}")
            rows.append(dict(pkg=p[0], ver=p[1], arch=p[2], src=p[3] or p[0], srcver=p[4] or p[1], maint=p[5]))
    if not rows:
        die("the package manifest is empty")

    work = os.path.join(a.out, "_work")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)

    wanted = {}  # (src, srcver) -> [binary packages]
    skipped = []
    for r in rows:
        kind = classify(r["maint"], r["pkg"])
        if kind != "debian":
            skipped.append((r["pkg"], r["ver"], kind))
            continue
        wanted.setdefault((r["src"], r["srcver"]), []).append(r["pkg"])
    print(f"collect-sources: {len(rows)} installed packages, {len(wanted)} Debian source packages, {len(skipped)} skipped")

    failures, collected = [], []
    for (src, ver), bins in sorted(wanted.items()):
        d = os.path.join(work, src)
        ok, why = (False, "")
        if not a.no_apt:
            ok, why = fetch_apt(src, ver, d)
        if not ok:
            ok, why2 = fetch_snapshot(src, ver, d)
            why = why2 if not ok else ""
        if not ok:
            failures.append(f"{src}={ver}: {why.strip()[:200]}")
            continue
        files = sorted(os.listdir(d))
        if not any(n.endswith(".dsc") for n in files):
            failures.append(f"{src}={ver}: no .dsc was downloaded")
            continue
        collected.append((src, ver, bins, files))
        print(f"  ok {src} {ver} ({len(files)} files)")
    if failures:
        print("collect-sources: could not obtain the source of:", file=sys.stderr)
        for f in failures:
            print("  " + f, file=sys.stderr)
        sys.exit(1)

    # 묶음으로 나눈다(GitHub 릴리스 자산은 파일 하나가 2 GiB 미만이어야 한다). 소스 패키지는 쪼개지 않는다.
    limit = a.max_part_mib * 1024 * 1024
    parts, cur, cur_size = [], [], 0
    for src, ver, bins, files in collected:
        size = sum(os.path.getsize(os.path.join(work, src, n)) for n in files)
        if size > limit:
            die(f"a single source package is larger than a part: {src} ({size} bytes)")
        if cur and cur_size + size > limit:
            parts.append(cur)
            cur, cur_size = [], 0
        cur.append((src, ver, bins, files))
        cur_size += size
    if cur:
        parts.append(cur)

    os.makedirs(a.out, exist_ok=True)
    tsv_path = os.path.join(a.out, f"{a.prefix}.tsv")
    names = []
    with open(tsv_path, "w", encoding="utf-8") as tsv:
        tsv.write("source\tversion\tpart\tfiles\tbinary_packages\n")
        for i, part in enumerate(parts, 1):
            name = f"{a.prefix}-{i:02d}.tar"
            names.append(name)
            with tarfile.open(os.path.join(a.out, name), "w") as tar:
                for src, ver, bins, files in part:
                    for n in files:
                        tar.add(os.path.join(work, src, n), arcname=f"{src}/{n}")
                    tsv.write(f"{src}\t{ver}\t{name}\t{' '.join(files)}\t{' '.join(sorted(set(bins)))}\n")
    with open(os.path.join(a.out, "SOURCES-README.txt"), "w", encoding="utf-8") as f:
        f.write(
            "Corresponding source code of the packages in the Opereta image\n"
            "================================================================\n\n"
            "These archives contain the Debian source packages (.dsc, upstream tarballs, Debian changes) of every\n"
            "Debian package installed in the image, at exactly the installed versions.\n"
            f"- {a.prefix}.tsv lists, for each source package, the archive part and the binary packages built from it.\n"
            f"- {', '.join(names)}: extract with `tar -xf`; each source package is a directory.\n"
            "- Packages of this project are in the repository source archive of the same release.\n"
            "- NVIDIA packages from the NVIDIA repository (proprietary driver user space, firmware) have no source\n"
            "  obligation. The NVIDIA open kernel module source (MIT/GPL) is inside the image at /usr/src/.\n"
            "- To rebuild a package: dpkg-source -x <name>.dsc && cd <dir> && dpkg-buildpackage -us -uc.\n\n"
            "Skipped packages (not Debian source):\n"
            + "".join(f"  {p} {v}: {k}\n" for p, v, k in sorted(skipped))
        )
    shutil.rmtree(work, ignore_errors=True)
    print(f"collect-sources: {len(collected)} source packages in {len(parts)} part(s): {', '.join(names)}")


if __name__ == "__main__":
    main()
