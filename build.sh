#!/usr/bin/env bash
#
# 一键构建并拷贝 sub2api 二进制到本脚本所在目录。
#
# 流程：
#   1. 调用 deploy/build_image.sh，用 Docker 编译前后端
#      （前端 vite 构建 + 后端 go build -tags embed 把前端打包进二进制）
#   2. 从 sub2api:latest 镜像中取出 /app/sub2api 二进制，覆盖到本脚本同级目录的 sub2api
#
# 用法：
#   ./build.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_BIN="${SCRIPT_DIR}/sub2api"

if ! command -v docker >/dev/null 2>&1; then
  echo "错误：未找到 docker，请先安装 Docker。" >&2
  exit 1
fi

echo "=========================================="
echo "  第 1 步：Docker 构建 sub2api:latest"
echo "=========================================="
"${SCRIPT_DIR}/deploy/build_image.sh"

echo ""
echo "=========================================="
echo "  第 2 步：从镜像拷贝二进制"
echo "=========================================="
CID="$(docker create sub2api:latest)"
cleanup() { docker rm -f "${CID}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker cp "${CID}:/app/sub2api" "${OUT_BIN}"

echo ""
echo "✓ 构建完成：${OUT_BIN}"
"${OUT_BIN}" -version 2>&1 | head -1 | sed 's/^/  版本：/' || true
