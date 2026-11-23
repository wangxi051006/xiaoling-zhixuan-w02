"""生成用户端 APK 的图标与启动图（品牌取自 web/src/user/style.css 的 .logo）。

用法（在 apps/user-app 目录）：
    python tools/make_icons.py

输出：
    user/src/main/res/mipmap-*/ic_launcher.png         传统图标（API 24-25）
    user/src/main/res/mipmap-*/ic_launcher_round.png   圆形图标
    user/src/main/res/drawable-*/ic_launcher_foreground.png  自适应图标前景（API 26+）
    user/src/main/res/drawable-nodpi/splash_logo.png   启动画面 LOGO
"""
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
RES = ROOT / "user" / "src" / "main" / "res"

BRAND = (217, 96, 58, 255)      # --red: #d9603a
PAPER = (255, 253, 248, 255)    # #fffdf8
FONT_CANDIDATES = [
    r"C:\Windows\Fonts\msyhbd.ttc",
    r"C:\Windows\Fonts\msyh.ttc",
    r"C:\Windows\Fonts\simhei.ttf",
]
LEGACY = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}
FOREGROUND = {"mdpi": 108, "hdpi": 162, "xhdpi": 216, "xxhdpi": 324, "xxxhdpi": 432}


def font(size: int) -> ImageFont.FreeTypeFont:
    for path in FONT_CANDIDATES:
        if Path(path).exists():
            return ImageFont.truetype(path, size)
    raise SystemExit("找不到中文字体（msyh.ttc / simhei.ttf）")


def draw_glyph(size: int, glyph_ratio: float) -> Image.Image:
    """白色「零」字，略微逆时针旋转 5 度，透明背景。"""
    layer = Image.new("RGBA", (size * 2, size * 2), (0, 0, 0, 0))
    draw = ImageDraw.Draw(layer)
    box = size * 2
    pil_font = font(int(box * glyph_ratio))
    left, top, right, bottom = draw.textbbox((0, 0), "零", font=pil_font)
    draw.text(
        ((box - (right - left)) / 2 - left, (box - (bottom - top)) / 2 - top),
        "零",
        font=pil_font,
        fill=PAPER,
    )
    layer = layer.rotate(5, resample=Image.BICUBIC, center=(box / 2, box / 2))
    return layer.resize((size, size), Image.LANCZOS)


def legacy_icon(size: int, round_icon: bool) -> Image.Image:
    scale = 4
    big = size * scale
    icon = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    mask = Image.new("L", (big, big), 0)
    if round_icon:
        ImageDraw.Draw(mask).ellipse((0, 0, big - 1, big - 1), fill=255)
    else:
        radius = int(big * 0.24)
        ImageDraw.Draw(mask).rounded_rectangle((0, 0, big - 1, big - 1), radius=radius, fill=255)
    plate = Image.new("RGBA", (big, big), BRAND)
    icon.paste(plate, (0, 0), mask)
    glyph = draw_glyph(big, 0.52)
    icon.alpha_composite(glyph)
    return icon.resize((size, size), Image.LANCZOS)


def foreground(size: int) -> Image.Image:
    """自适应图标前景：108dp 画布，内容控制在中间 66dp 安全区。"""
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    inner = int(size * 66 / 108)
    glyph = draw_glyph(inner, 0.62)
    offset = (size - inner) // 2
    canvas.alpha_composite(glyph, (offset, offset))
    return canvas


def main() -> None:
    for density, size in LEGACY.items():
        target = RES / f"mipmap-{density}"
        target.mkdir(parents=True, exist_ok=True)
        legacy_icon(size, False).save(target / "ic_launcher.png")
        legacy_icon(size, True).save(target / "ic_launcher_round.png")
    for density, size in FOREGROUND.items():
        target = RES / f"drawable-{density}"
        target.mkdir(parents=True, exist_ok=True)
        foreground(size).save(target / "ic_launcher_foreground.png")
    splash_dir = RES / "drawable-nodpi"
    splash_dir.mkdir(parents=True, exist_ok=True)
    body = Image.new("RGBA", (288, 288), (0, 0, 0, 0))
    plate = legacy_icon(288, False)
    body.alpha_composite(plate, (0, 0))
    body.save(splash_dir / "splash_logo.png")
    print("icons written to", RES)


if __name__ == "__main__":
    main()
