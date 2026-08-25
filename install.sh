#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$root"
bin_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
data_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}/pentgo

# 1) Go 工具链检查：缺失时给出安装指引后退出（go.mod 要求 go 1.25+）。
if ! command -v go >/dev/null 2>&1; then
  echo "错误: 未找到 go 工具链。请先安装 Go 1.25+ 再运行本脚本:" >&2
  echo "  Debian/Ubuntu:  sudo apt install golang-go" >&2
  echo "  或官方发行包:   https://go.dev/dl/" >&2
  exit 1
fi

# 2) 依赖拉齐：按 go.mod 下载模块并生成/校验 go.sum。
go mod tidy

# 3) 编译并安装二进制到用户 bin 目录（无需 sudo）。
mkdir -p "$bin_dir"
go build -o "$bin_dir/pentgo" ./cmd/pentgo

# 4) 首次安装时复制内置 skills；重复安装不覆盖用户已有技能。
if [ ! -d "$data_dir/skills" ]; then
  mkdir -p "$data_dir"
  cp -R "$root/skills" "$data_dir/skills"
fi
