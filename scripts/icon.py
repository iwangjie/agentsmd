#!/usr/bin/env python3
"""Draw the AgentsMD icon: a dark rounded tile of prompt modules, each with its
rail, the active one accented with a switch beside it."""

import os
from PIL import Image, ImageDraw, ImageFilter

SIZE = 1024
SS = 4
W = SIZE * SS

BG_TOP = (40, 41, 48)
BG_BOTTOM = (21, 22, 26)
ACCENT = (255, 92, 118)
WHITE = (252, 252, 254)


def rounded_mask(w, radius, inset=0):
    m = Image.new("L", (w, w), 0)
    ImageDraw.Draw(m).rounded_rectangle(
        [inset, inset, w - inset, w - inset], radius=radius, fill=255
    )
    return m


def tile(w, top, bottom, radius, inset):
    grad = Image.new("RGB", (w, w))
    d = ImageDraw.Draw(grad)
    for y in range(w):
        t = y / (w - 1)
        d.line([(0, y), (w, y)], fill=tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))
    out = Image.new("RGBA", (w, w), (0, 0, 0, 0))
    out.paste(grad, (0, 0), rounded_mask(w, radius, inset))
    return out


def main():
    img = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    radius = int(W * 0.225)
    inset = int(W * 0.06)
    img.alpha_composite(tile(W, BG_TOP, BG_BOTTOM, radius, inset))

    edge = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    ImageDraw.Draw(edge).rounded_rectangle(
        [inset, inset, W - inset, W - inset],
        radius=radius,
        outline=(255, 255, 255, 40),
        width=int(W * 0.0045),
    )
    img.alpha_composite(edge)

    # Content: four module rows, left rail and a bar each.
    left = int(W * 0.235)
    right = int(W * 0.765)
    width = right - left
    row_h = int(W * 0.072)
    pitch = int(W * 0.125)
    rows = [
        (0.62, WHITE, 240, False),
        (0.40, WHITE, 120, False),
        (0.34, ACCENT, 255, True),
        (0.50, WHITE, 90, False),
    ]
    total = pitch * (len(rows) - 1) + row_h
    y = (W - total) // 2
    rail_w = int(W * 0.024)
    rail_gap = int(W * 0.042)

    for frac, color, alpha, active in rows:
        rail = Image.new("RGBA", (W, W), (0, 0, 0, 0))
        ImageDraw.Draw(rail).rounded_rectangle(
            [left, y + row_h * 0.08, left + rail_w, y + row_h * 0.92],
            radius=rail_w / 2,
            fill=(*color, 255 if active else 70),
        )
        img.alpha_composite(rail)

        x0 = left + rail_w + rail_gap
        bar_w = width * frac
        bar = Image.new("RGBA", (W, W), (0, 0, 0, 0))
        ImageDraw.Draw(bar).rounded_rectangle(
            [x0, y, x0 + bar_w, y + row_h], radius=row_h / 2, fill=(*color, alpha)
        )
        if active:
            glow = bar.filter(ImageFilter.GaussianBlur(int(W * 0.02)))
            img.alpha_composite(glow)
        img.alpha_composite(bar)

        if active:
            # A switch at the row's right end, on: the module is enabled.
            sw_h = int(row_h * 1.02)
            sw_w = int(sw_h * 1.9)
            sx = right - sw_w
            sy = y + (row_h - sw_h) / 2
            sw = Image.new("RGBA", (W, W), (0, 0, 0, 0))
            sd = ImageDraw.Draw(sw)
            sd.rounded_rectangle([sx, sy, sx + sw_w, sy + sw_h], radius=sw_h / 2, fill=(*ACCENT, 255))
            r = sw_h * 0.37
            cx = sx + sw_w - sw_h / 2
            cy = sy + sw_h / 2
            sd.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(255, 255, 255, 255))
            img.alpha_composite(sw)

        y += pitch

    out = img.resize((SIZE, SIZE), Image.LANCZOS)
    here = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    os.makedirs(os.path.join(here, "resources"), exist_ok=True)
    out.save(os.path.join(here, "resources", "icon.png"))
    print("wrote resources/icon.png")


if __name__ == "__main__":
    main()
