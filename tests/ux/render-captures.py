#!/usr/bin/env python3
"""Render captured production Bubble Tea ANSI views as colored PNG cells."""

from __future__ import annotations

import argparse
import re
import unicodedata
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

SGR = re.compile(r"\x1b\[([0-9;]*)m")
DEFAULT_FG = (233, 245, 255)
DEFAULT_BG = (9, 38, 111)


def cell_width(char: str) -> int:
    if unicodedata.combining(char):
        return 0
    return 2 if unicodedata.east_asian_width(char) in {"W", "F"} else 1


def color_cells(content: str) -> list[list[tuple[str, tuple[int, int, int], tuple[int, int, int]]]]:
    fg, bg = DEFAULT_FG, DEFAULT_BG
    bold = False
    rows: list[list[tuple[str, tuple[int, int, int], tuple[int, int, int]]]] = [[]]
    cursor = 0
    for match in SGR.finditer(content):
        for char in content[cursor : match.start()]:
            if char == "\n":
                rows.append([])
            elif char != "\r":
                rows[-1].append((char, fg, bg))
        codes = [int(code) for code in match.group(1).split(";") if code]
        i = 0
        while i < len(codes):
            code = codes[i]
            if code == 0:
                fg, bg, bold = DEFAULT_FG, DEFAULT_BG, False
            elif code == 1:
                bold = True
            elif code == 22:
                bold = False
            elif code in (38, 48) and i + 4 < len(codes) and codes[i + 1] == 2:
                rgb = tuple(codes[i + 2 : i + 5])
                if code == 38:
                    fg = rgb
                else:
                    bg = rgb
                i += 4
            elif code == 39:
                fg = DEFAULT_FG
            elif code == 49:
                bg = DEFAULT_BG
            i += 1
        cursor = match.end()
    for char in content[cursor:]:
        if char == "\n":
            rows.append([])
        elif char != "\r":
            rows[-1].append((char, fg, bg))
    return rows


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("ansi_dir", type=Path)
    parser.add_argument("png_dir", type=Path)
    parser.add_argument("--font", type=Path, default=Path("/System/Library/Fonts/Menlo.ttc"))
    args = parser.parse_args()
    args.png_dir.mkdir(parents=True, exist_ok=True)
    font = ImageFont.truetype(str(args.font), 14)
    cell_w, cell_h = 9, 20
    for source in sorted(args.ansi_dir.glob("*.ansi")):
        rows = color_cells(source.read_text(encoding="utf-8"))
        width = max(1, max((sum(cell_width(ch) for ch, _, _ in row) for row in rows), default=1))
        image = Image.new("RGB", (width * cell_w, max(1, len(rows)) * cell_h), DEFAULT_BG)
        draw = ImageDraw.Draw(image)
        for y, row in enumerate(rows):
            x = 0
            for char, fg, bg in row:
                span = cell_width(char)
                if span == 0:
                    continue
                draw.rectangle((x * cell_w, y * cell_h, (x + span) * cell_w - 1, (y + 1) * cell_h - 1), fill=bg)
                draw.text((x * cell_w, y * cell_h + 1), char, font=font, fill=fg)
                x += span
        target = args.png_dir / (source.stem + ".png")
        image.save(target)
        print(target)


if __name__ == "__main__":
    main()
