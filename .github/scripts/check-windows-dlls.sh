#!/usr/bin/env bash
# Fails when a Windows build imports a MinGW runtime DLL.
#
# The build links cgo code — the tree-sitter grammars — with the runner's MinGW gcc. The runner has
# MinGW's DLLs on PATH, so a build that needs one starts there and fails on a user's machine, which
# has none; `task build` links them statically (`-extldflags=-static`) and this reads the .exe's
# import table to prove it. It also refuses a build without cgo, which would have no grammars.
#
# Usage: check-windows-dlls.sh <path to the .exe>
set -euo pipefail

exe="${1:?usage: check-windows-dlls.sh <path to the .exe>}"

if [ "$(go env CGO_ENABLED)" != "1" ]; then
  echo "error: cgo is off on this runner — the build would have no tree-sitter grammars" >&2
  exit 1
fi
command -v objdump >/dev/null || { echo "error: objdump not found; MinGW's binutils are needed" >&2; exit 1; }

imports=$(objdump -p "$exe" | sed -n 's/^[[:space:]]*DLL Name: //p')
echo "$exe imports:"
while IFS= read -r dll; do echo "  $dll"; done <<<"${imports}"

if echo "${imports}" | grep -Eiq '^(libgcc|libwinpthread|libstdc\+\+)'; then
  echo "error: $exe needs a MinGW runtime DLL; link it with -extldflags=-static" >&2
  exit 1
fi
