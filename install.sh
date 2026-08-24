#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bin_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
data_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}/pentgo

mkdir -p "$bin_dir"
go build -o "$bin_dir/pentgo" "$root/cmd/pentgo"

if [ ! -d "$data_dir/skills" ]; then
  mkdir -p "$data_dir"
  cp -R "$root/skills" "$data_dir/skills"
fi
