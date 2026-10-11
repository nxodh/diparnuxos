#!/usr/bin/env bash
# 고정 소스의 제3자 라이선스와 헤더 안의 고지를 패키지 문서에 모은다.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: copy-vendor-licenses.sh <source> <doc-directory>' >&2; exit 2; }
source_dir="$1"
destination="$2/vendor-licenses"
mkdir -p "$destination"
for component in cpp-httplib hash/sha256 hash/xxhash; do
    name="${component//\//-}"
    install -m 644 "$source_dir/vendor/$component/LICENSE" "$destination/$name.txt"
done
# JSON의 합쳐진 헤더에는 포함된 코드의 저작권 표시도 있다. 모두 같은 MIT 식별자를 쓴다.
json_header="$source_dir/vendor/nlohmann/json.hpp"
[[ "$(grep 'SPDX-License-Identifier:' "$json_header" | LC_ALL=C sort -u)" == '// SPDX-License-Identifier: MIT' ]]
{
    grep 'SPDX-FileCopyrightText:' "$json_header" | LC_ALL=C sort -u
    echo
    sed -n '/^Permission is hereby granted/,$p' "$source_dir/LICENSE"
} > "$destination/nlohmann-json.txt"
# 소스 내부의 고지를 행 번호에 의존하지 않고 고유한 시작 문구부터 추출한다.
awk '/This software is available as a choice of the following licenses/{copy=1} copy' \
    "$source_dir/vendor/miniaudio/miniaudio.h" > "$destination/miniaudio.txt"
awk '/This software is available under 2 licenses/{copy=1} copy' \
    "$source_dir/vendor/stb/stb_image.h" > "$destination/stb-image.txt"
awk '/This is free and unencumbered software released into the public domain/{copy=1} copy {print; if ($0 ~ /\*\//) exit}' \
    "$source_dir/vendor/sheredom/subprocess.h" > "$destination/sheredom-subprocess.txt"
for notice in "$destination"/*.txt; do
    [[ -s "$notice" ]] || { echo "ERROR: empty vendor notice: $notice" >&2; exit 1; }
done
grep -q 'THE SOFTWARE IS PROVIDED' "$destination/nlohmann-json.txt"
grep -q 'MIT No Attribution' "$destination/miniaudio.txt"
grep -q 'MIT License' "$destination/stb-image.txt"
grep -q 'unlicense.org' "$destination/sheredom-subprocess.txt"
