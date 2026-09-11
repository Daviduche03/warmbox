#!/usr/bin/env python3
"""Generate the hand-rolled Warmbox "Win11" theme assets.

Produces (relative to this file):
  wallpaper.png          Win11-style "bloom" desktop wallpaper
  start.png              Start button icon (four-pane window, like the OS logo)
  xfwm4/*.png            Flat window-manager pixmaps (titlebar, borders, buttons)

Pure Python stdlib only (zlib + struct) so it runs on macOS and in Alpine
without Pillow/ImageMagick. Run from anywhere:  python3 gen_win11.py
"""

import os
import struct
import zlib
import math

HERE = os.path.dirname(os.path.abspath(__file__))


def write_png(path, w, h, rows):
    """rows: list of bytearrays, each length w*channels (RGB or RGBA)."""
    def chunk(typ, data):
        c = struct.pack(">I", len(data)) + typ + data
        c += struct.pack(">I", zlib.crc32(typ + data) & 0xFFFFFFFF)
        return c

    nchan = len(rows[0]) // w
    color_type = 6 if nchan == 4 else 2
    raw = b"".join(b"\x00" + bytes(r) for r in rows)
    ihdr = struct.pack(">IIBBBBB", w, h, 8, color_type, 0, 0, 0)
    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", ihdr)
    png += chunk(b"IDAT", zlib.compress(raw, 9))
    png += chunk(b"IEND", b"")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(png)


# ---------------------------------------------------------------- wallpaper

def gen_wallpaper(path, w=1920, h=1200):
    # Win11-style "bloom": a deep blue vertical gradient with a soft radial
    # glow upper-left of centre. Per-pixel, pure python — a few seconds at 2MP.
    top = (0x0A, 0x1E, 0x4D)
    mid = (0x13, 0x36, 0x72)
    bot = (0x2B, 0x5F, 0xA8)
    glow_cx, glow_cy, glow_r = 0.36 * w, 0.30 * h, 0.62 * h
    glow_col = (0x8F, 0xB8, 0xFF)

    rows = []
    for y in range(h):
        row = bytearray(w * 3)
        t = y / (h - 1)
        if t < 0.5:
            tt = t / 0.5
            bg = [int(top[i] + (mid[i] - top[i]) * tt) for i in range(3)]
        else:
            tt = (t - 0.5) / 0.5
            bg = [int(mid[i] + (bot[i] - mid[i]) * tt) for i in range(3)]
        for x in range(w):
            dx = (x - glow_cx) / glow_r
            dy = (y - glow_cy) / glow_r
            d = math.sqrt(dx * dx + dy * dy)
            g = max(0.0, 1.0 - d)
            g = g * g * 0.55
            o = x * 3
            row[o] = min(255, int(bg[0] + glow_col[0] * g))
            row[o + 1] = min(255, int(bg[1] + glow_col[1] * g))
            row[o + 2] = min(255, int(bg[2] + glow_col[2] * g))
        rows.append(row)
    write_png(path, w, h, rows)
    print(f"  wrote {path} ({w}x{h})")


# ------------------------------------------------------------------ start icon

def gen_start_icon(path, size=64):
    # Four-pane window "start" logo in accent blue with rounded-ish panes.
    rows = []
    for y in range(size):
        row = bytearray(size * 4)
        for x in range(size):
            # pane grid: 4 cells with small gaps
            cell = 2
            if size == 0:
                continue
            px, py = x / size, y / size
            i = int(px * 2)
            j = int(py * 2)
            fx = (px * 2) - i
            fy = (py * 2) - j
            o = x * 4
            if 0.06 < fx < 0.94 and 0.06 < fy < 0.94:
                row[o:o + 4] = (0x00, 0x78, 0xD4, 255)
            else:
                row[o:o + 4] = (0, 0, 0, 0)
        rows.append(row)
    write_png(path, size, size, rows)
    print(f"  wrote {path}")


# ------------------------------------------------------------ xfwm4 pixmaps

def flat_png(path, w, h, color, alpha=255):
    rows = [bytearray([color[0], color[1], color[2], alpha] * w) for _ in range(h)]
    write_png(path, w, h, rows)


def glyph_btn(path, w, h, glyph, fg, bg_alpha=0):
    """Draw a simple glyph on a (possibly transparent) button background.

    glyph: 'x' close | 'min' | 'max' | 'menu' | 'restore'
    """
    rows = []
    for y in range(h):
        row = bytearray(w * 4)
        for x in range(w):
            on = False
            if glyph == "x":
                d = abs((x - w / 2) - (y - h / 2))
                d2 = abs((x - w / 2) + (y - h / 2))
                on = d <= 1.6 or d2 <= 1.6
            elif glyph == "min":
                on = h // 2 - 1 <= y <= h // 2 and w // 4 <= x < 3 * w // 4
            elif glyph == "max":
                m = 3
                on = (m <= x < w - m and y in (m, h - m - 1)) or \
                     (m <= y < h - m and x in (m, w - m - 1))
            elif glyph == "restore":
                m = 3
                on = (m + 3 <= x < w - m and y in (m, m + 2)) or \
                     (m + 3 <= y < h - m and x in (m, m + 2))
            elif glyph == "menu":
                for i, yy in enumerate((h // 4, h // 2, 3 * h // 4)):
                    if yy - 1 <= y <= yy and w // 4 <= x < 3 * w // 4:
                        on = True
            o = x * 4
            if on:
                row[o:o + 4] = (fg[0], fg[1], fg[2], 255)
            elif bg_alpha:
                row[o:o + 4] = (bg[0], bg[1], bg[2], bg_alpha)
            else:
                row[o:o + 4] = (0, 0, 0, 0)
        rows.append(row)
    write_png(path, w, h, rows)


TITLE_H = 29
BTN_W, BTN_H = 21, TITLE_H
TITLE_ACTIVE = (0xF3, 0xF3, 0xF3)
TITLE_INACTIVE = (0xE6, 0xE6, 0xE6)
BORDER = (0xD4, 0xD4, 0xD4)
FG = (0x33, 0x33, 0x33)
FG_HOVER = (0x11, 0x11, 0x11)
CLOSE_HOVER = (0xE8, 0x11, 0x23)


def gen_xfwm(theme_dir):
    os.makedirs(theme_dir, exist_ok=True)

    # Titlebar strips: solid flat colour, tiled by xfwm4.
    for i in range(1, 6):
        flat_png(f"{theme_dir}/title-{i}-active.png", 8, TITLE_H, TITLE_ACTIVE)
        flat_png(f"{theme_dir}/title-{i}-inactive.png", 8, TITLE_H, TITLE_INACTIVE)

    # Corners + edges.
    flat_png(f"{theme_dir}/top-left-active.png", 8, TITLE_H, TITLE_ACTIVE)
    flat_png(f"{theme_dir}/top-right-active.png", 8, TITLE_H, TITLE_ACTIVE)
    flat_png(f"{theme_dir}/top-left-inactive.png", 8, TITLE_H, TITLE_INACTIVE)
    flat_png(f"{theme_dir}/top-right-inactive.png", 8, TITLE_H, TITLE_INACTIVE)
    flat_png(f"{theme_dir}/left-active.png", 5, 24, BORDER)
    flat_png(f"{theme_dir}/right-active.png", 5, 24, BORDER)
    flat_png(f"{theme_dir}/left-inactive.png", 5, 24, BORDER)
    flat_png(f"{theme_dir}/right-inactive.png", 5, 24, BORDER)
    flat_png(f"{theme_dir}/bottom-active.png", 24, 5, BORDER)
    flat_png(f"{theme_dir}/bottom-inactive.png", 24, 5, BORDER)
    flat_png(f"{theme_dir}/bottom-left-active.png", 16, 16, BORDER)
    flat_png(f"{theme_dir}/bottom-right-active.png", 16, 16, BORDER)
    flat_png(f"{theme_dir}/bottom-left-inactive.png", 16, 16, BORDER)
    flat_png(f"{theme_dir}/bottom-right-inactive.png", 16, 16, BORDER)

    # Buttons: transparent bg, dark glyph; close hover turns red.
    for state in ("active", "inactive", "prelight", "pressed"):
        for name, glyph in (("close", "x"), ("hide", "min"),
                            ("maximize", "max"), ("menu", "menu")):
            fg = FG
            if state == "prelight":
                fg = FG_HOVER
            if name == "close" and state in ("prelight", "pressed"):
                flat_png(f"{theme_dir}/close-{state}.png", BTN_W, BTN_H, CLOSE_HOVER)
                continue
            glyph_btn(f"{theme_dir}/{name}-{state}.png", BTN_W, BTN_H, glyph, fg)
    # Toggled variants (maximized/restore), reuse.
    for state in ("active", "inactive", "prelight", "pressed"):
        glyph_btn(f"{theme_dir}/maximize-toggled-{state}.png", BTN_W, BTN_H, "restore", FG if state != "prelight" else FG_HOVER)

    print(f"  wrote xfwm4 pixmaps to {theme_dir}")


def main():
    gen_wallpaper(os.path.join(HERE, "wallpaper.png"))
    gen_start_icon(os.path.join(HERE, "start.png"))
    gen_xfwm(os.path.join(HERE, "xfwm4"))


if __name__ == "__main__":
    main()