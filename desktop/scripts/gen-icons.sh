#!/usr/bin/env sh
# =============================================================================
# 从 app-icon.svg 重新生成全套图标。
#
# 关键：`tauri icon` 产出的 icon.icns 是满幅的，但 macOS 图标栅格要求
# 图形内容占画布 ~80.5%（1024 画布四边各留 100px 透明边距），满幅图标
# 在 Dock 里会比别的应用「大一圈」。本脚本在 tauri icon 之后重做 icns：
# 全幅位图缩到 824 居中贴到 1024 透明画布，再 iconutil 打包。
# （GCMS Pilot 的 icns 同样是 80.47% 占比，此处对齐。）
#
# 依赖：npx、python3 + Pillow、iconutil（macOS 自带）→ 本脚本只能在 macOS 跑。
# =============================================================================
set -eu
cd "$(dirname "$0")/.."

[ -f src-tauri/app-icon.svg ] || { echo "缺少 src-tauri/app-icon.svg"; exit 1; }

echo "» tauri icon 生成全套（png/ico/icns 满幅）…"
npx tauri icon src-tauri/app-icon.svg

echo "» 重做 icon.icns（macOS 栅格边距）…"
python3 - <<'PY'
from PIL import Image
import pathlib, subprocess, tempfile

# tauri icon 的 icns 里 1024 位图是满幅的，取它当源
src_dir = pathlib.Path(tempfile.mkdtemp())
subprocess.run(["iconutil", "-c", "iconset", "src-tauri/icons/icon.icns", "-o", src_dir / "full.iconset"], check=True)
full = Image.open(src_dir / "full.iconset" / "icon_512x512@2x.png").convert("RGBA")

CANVAS, CONTENT = 1024, 824  # Apple 图标栅格：824/1024 ≈ 80.5%
art = full.resize((CONTENT, CONTENT), Image.LANCZOS)
canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
canvas.paste(art, ((CANVAS - CONTENT) // 2, (CANVAS - CONTENT) // 2), art)

out = src_dir / "padded.iconset"
out.mkdir()
for size in (16, 32, 128, 256, 512):
    canvas.resize((size, size), Image.LANCZOS).save(out / f"icon_{size}x{size}.png")
    canvas.resize((size * 2, size * 2), Image.LANCZOS).save(out / f"icon_{size}x{size}@2x.png")
subprocess.run(["iconutil", "-c", "icns", out, "-o", "src-tauri/icons/icon.icns"], check=True)
print("icon.icns 已重做（内容占比 80.5%）")
PY

echo "✓ 完成。改过 app-icon.svg 后重跑本脚本即可。"
