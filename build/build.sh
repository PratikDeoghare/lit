#!/bin/sh
# Rebuild everything from lit.lit and check the result.
#
#   sh build.sh [OUTDIR]
#
# Needs Go, and python3 for the scripts of the skill (that step is skipped without it).  The first run needs a lit to start from: pass LIT=path/to/lit, or have one on the PATH.
# Later runs use the lit this document produced, so the document builds itself.  The page step needs
# BRASHTAG_JS=path/to/parser.js (the brashtag parser as a JavaScript module) and node for the page's own
# tests; without them it is skipped.  The root lit.lit includes the chapter files next to it.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
src="$here/lit.lit"
out=${1:-"$here/build"}
boot=${LIT:-$(command -v lit || true)}
[ -n "$boot" ] || { echo "no lit to start from; set LIT=..." >&2; exit 2; }

rm -rf "$out"
echo "== tangle"
"$boot" tangle "$src" "$out" >/dev/null

echo "== vet and test"
(cd "$out" && go vet ./... && go test ./... 2>&1 | sed 's/^/   /')

echo "== build"
mkdir -p "$out/bin"
(cd "$out" && go build -o bin/lit ./cmd/lit && go build -o bin/mkpage ./cmd/mkpage)
ls "$out/bin" | sed 's/^/   /'

echo "== page: the JavaScript tests, and the landing page"
if [ -n "${BRASHTAG_JS:-}" ] && command -v node >/dev/null 2>&1; then
    stats=$("$out/bin/lit" lint "$src" 2>/dev/null | tail -n 1)
    page="$out.page"
    rm -rf "$page"
    mkdir -p "$page"
    cp -r "$out/web" "$page/web"
    mkdir -p "$page/internal/skill/files"
    cp -r "$out/internal/skill/files/docs" "$page/internal/skill/files/docs"     # the page shows the overview figure
    (
        cd "$page/web"
        "$out/bin/mkpage" --parser-only > parser.inline.js
        cat parser.inline.js core.js core.tests.mjs > core.test.mjs
        node core.test.mjs > test.log
        if grep -q FAIL test.log; then cat test.log >&2; exit 1; fi
        echo "   $(grep -c '^ok' test.log) JavaScript checks passed"
        LIT_STATS="$stats" "$out/bin/mkpage" > landing.html
    )
    mkdir -p "$out/dist"
    cp "$page/web/landing.html" "$out/dist/landing.html"
    rm -rf "$page"
else
    echo "   skipped (needs node, and BRASHTAG_JS=path/to/brashtag/parser.js)"
fi

echo "== fixed point: the new lit must reproduce every file"
again="$out.again"
rm -rf "$again"
"$out/bin/lit" tangle "$src" "$again" >/dev/null
if diff -r -x '*.map' -x '*.sync' -x bin -x dist "$out" "$again"; then
    echo "   identical"
else
    echo "FAILED: the document does not reproduce itself" >&2
    exit 1
fi
rm -rf "$again"

echo "== python scripts: their tests, and the Go program and the scripts must agree"
if command -v python3 >/dev/null 2>&1; then
    export PYTHONDONTWRITEBYTECODE=1
    scripts="$out/internal/skill/files/scripts"
    python3 "$out/internal/skill/pytests/test_fill_review.py" "$scripts" > "$out.py.log" 2>&1 \
        || { cat "$out.py.log" >&2; exit 1; }
    echo "   $(grep '^Ran' "$out.py.log")"
    pyout="$out.py"
    rm -rf "$pyout"
    python3 "$scripts/tangle.py" "$src" "$pyout" >/dev/null 2>&1
    if diff -r -x '*.map' -x '*.sync' -x bin -x dist "$out" "$pyout" >/dev/null; then
        echo "   tangle.py writes the same files as lit"
    else
        echo "FAILED: tangle.py and lit disagree about the files" >&2
        exit 1
    fi
    [ "$(python3 "$scripts/lint.py" "$src" 2>/dev/null | tail -n 1)" = "$("$out/bin/lit" lint "$src" 2>/dev/null | tail -n 1)" ] \
        || { echo "FAILED: lint.py and lit lint disagree" >&2; exit 1; }
    echo "   lint.py agrees with lit lint"
    rm -rf "$pyout" "$out.py.log"
else
    echo "   skipped (needs python3)"
fi

echo "== lint"
"$out/bin/lit" lint "$src"

echo "== weave"
"$out/bin/lit" weave "$src" -o "$out/woven" >/dev/null
echo "   $out/woven/lit.html"
echo "   $out/woven/lit.tex"
