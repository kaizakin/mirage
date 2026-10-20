#!/usr/bin/env python3
"""Draws one product image per row of catalog.tsv into this directory as <slug>.jpg, plus the
home page's banner, hero.jpg.

The image column is kind:body:accent[:label]. Each kind is a small SVG illustration; rsvg-convert
renders it and ImageMagick compresses it, so the store's catalog needs no stock photos.

Run it after editing catalog.tsv; the JPEGs are committed, and the setup Job imports them.
Needs rsvg-convert and magick on PATH.
"""

import csv
import random
import subprocess
from pathlib import Path

HERE = Path(__file__).resolve().parent
SIZE = 800


def shade(hex_color, factor):
    """Lightens (factor > 1) or darkens (factor < 1) a #rrggbb color."""
    r, g, b = (int(hex_color[i : i + 2], 16) for i in (1, 3, 5))
    if factor >= 1:
        r, g, b = (round(c + (255 - c) * (factor - 1)) for c in (r, g, b))
    else:
        r, g, b = (round(c * factor) for c in (r, g, b))
    return f"#{min(r, 255):02x}{min(g, 255):02x}{min(b, 255):02x}"


def cylinder(gid, color):
    """A left-to-right gradient that makes a flat shape read as a lit cylinder."""
    return f"""<linearGradient id="{gid}" x1="0" x2="1">
      <stop offset="0" stop-color="{shade(color, 0.62)}"/>
      <stop offset="0.35" stop-color="{shade(color, 1.12)}"/>
      <stop offset="0.55" stop-color="{color}"/>
      <stop offset="1" stop-color="{shade(color, 0.55)}"/>
    </linearGradient>"""


def mug(body, accent, label, speckled=False):
    specks = ""
    if speckled:
        rng = random.Random(7)
        specks = "".join(
            f'<circle cx="{rng.uniform(268, 520):.0f}" cy="{rng.uniform(318, 600):.0f}" r="{rng.uniform(1.5, 4):.1f}" fill="{accent}" opacity="0.55"/>'
            for _ in range(90)
        )
    return f"""
    <defs>{cylinder("b", body)}</defs>
    <path d="M530 380 c95 0 95 170 0 170" fill="none" stroke="{shade(body, 0.7)}" stroke-width="34" stroke-linecap="round"/>
    <path d="M255 310 h290 v270 q0 40 -40 40 h-210 q-40 0 -40 -40 z" fill="url(#b)"/>
    {specks}
    <ellipse cx="400" cy="310" rx="145" ry="26" fill="{shade(body, 1.18)}"/>
    <ellipse cx="400" cy="314" rx="128" ry="18" fill="{accent}"/>
    <rect x="285" y="340" width="16" height="230" rx="8" fill="#fff" opacity="0.18"/>"""


def cup(body, accent, label):
    return f"""
    <defs>{cylinder("b", body)}</defs>
    <ellipse cx="400" cy="585" rx="230" ry="46" fill="{shade(body, 0.8)}"/>
    <ellipse cx="400" cy="575" rx="210" ry="36" fill="{shade(body, 1.25)}"/>
    <path d="M505 430 c70 0 70 100 0 100" fill="none" stroke="{shade(body, 0.75)}" stroke-width="26" stroke-linecap="round"/>
    <path d="M290 400 h220 q0 170 -110 170 q-110 0 -110 -170 z" fill="url(#b)"/>
    <ellipse cx="400" cy="400" rx="110" ry="20" fill="{shade(body, 1.3)}"/>
    <ellipse cx="400" cy="403" rx="98" ry="14" fill="#3b2314"/>
    <path d="M292 400 h216" stroke="{accent}" stroke-width="5"/>"""


def glass_mug(body, accent, label):
    return f"""
    <path d="M530 360 c90 0 90 180 0 180" fill="none" stroke="#ffffff" stroke-opacity="0.75" stroke-width="22"/>
    <path d="M260 300 h280 v290 q0 30 -30 30 h-220 q-30 0 -30 -30 z" fill="#ffffff" fill-opacity="0.45" stroke="#ffffff" stroke-width="5"/>
    <path d="M285 390 h230 v180 q0 25 -25 25 h-180 q-25 0 -25 -25 z" fill="{accent}" fill-opacity="0.85"/>
    <ellipse cx="400" cy="390" rx="115" ry="14" fill="{shade(accent, 1.35)}"/>
    <ellipse cx="400" cy="300" rx="140" ry="22" fill="none" stroke="#ffffff" stroke-width="5"/>
    <rect x="282" y="320" width="14" height="250" rx="7" fill="#fff" opacity="0.6"/>"""


def iron_teapot(body, accent, label):
    rng = random.Random(3)
    dots = "".join(
        f'<circle cx="{rng.uniform(250, 550):.0f}" cy="{rng.uniform(380, 590):.0f}" r="5" fill="{shade(body, 1.35)}" opacity="0.6"/>'
        for _ in range(70)
    )
    return f"""
    <defs><radialGradient id="b" cx="0.38" cy="0.35" r="0.7">
      <stop offset="0" stop-color="{shade(body, 1.6)}"/><stop offset="0.6" stop-color="{body}"/><stop offset="1" stop-color="{shade(body, 0.6)}"/>
    </radialGradient></defs>
    <path d="M300 300 q100 -170 200 0" fill="none" stroke="{accent}" stroke-width="16" stroke-linecap="round"/>
    <path d="M250 470 q-110 -20 -140 -110 l30 -10 q40 70 120 70 z" fill="{shade(body, 0.85)}"/>
    <ellipse cx="400" cy="480" rx="165" ry="130" fill="url(#b)"/>
    <g clip-path="url(#c)">{dots}</g>
    <clipPath id="c"><ellipse cx="400" cy="480" rx="160" ry="125"/></clipPath>
    <ellipse cx="400" cy="358" rx="95" ry="22" fill="{shade(body, 1.3)}"/>
    <circle cx="400" cy="338" r="18" fill="{accent}"/>"""


def glass_teapot(body, accent, label):
    return f"""
    <path d="M555 420 c80 0 80 150 0 150" fill="none" stroke="#ffffff" stroke-opacity="0.8" stroke-width="22"/>
    <path d="M250 470 q-100 -10 -130 -100 l26 -8 q36 60 110 60 z" fill="#ffffff" fill-opacity="0.6"/>
    <ellipse cx="400" cy="480" rx="165" ry="135" fill="#ffffff" fill-opacity="0.4" stroke="#ffffff" stroke-width="5"/>
    <path d="M240 500 q160 30 320 0 q-10 110 -160 115 q-150 -5 -160 -115 z" fill="{accent}" fill-opacity="0.8"/>
    <rect x="365" y="340" width="70" height="230" rx="12" fill="#9aa3a8" fill-opacity="0.85"/>
    <g stroke="#6f7a80" stroke-width="3">{"".join(f'<line x1="372" x2="428" y1="{y}" y2="{y}"/>' for y in range(360, 560, 18))}</g>
    <ellipse cx="400" cy="345" rx="100" ry="22" fill="#d9dee0"/>
    <circle cx="400" cy="322" r="16" fill="{shade(body, 0.6)}"/>"""


def tray(body, accent, label):
    slats = "".join(
        f'<path d="M{170 + i * 32} 430 l{-20 + i * 2.5} 150" stroke="{shade(body, 0.78)}" stroke-width="5"/>' for i in range(15)
    )
    return f"""
    <path d="M170 420 h460 l40 170 h-540 z" fill="{body}"/>
    {slats}
    <path d="M130 590 h540 v30 h-540 z" fill="{shade(body, 0.7)}"/>
    <path d="M170 420 h460 l40 170 h-540 z" fill="none" stroke="{accent}" stroke-width="8"/>
    <path d="M330 430 h80 q0 70 -40 70 q-40 0 -40 -70 z" fill="#f4efe6"/>
    <path d="M440 430 h80 q0 70 -40 70 q-40 0 -40 -70 z" fill="#f4efe6"/>
    <ellipse cx="370" cy="430" rx="40" ry="8" fill="#8a5a2b"/>
    <ellipse cx="480" cy="430" rx="40" ry="8" fill="#8a5a2b"/>"""


def tin(body, accent, label):
    return f"""
    <defs>{cylinder("b", body)}</defs>
    <path d="M280 260 h240 v370 q0 22 -120 22 q-120 0 -120 -22 z" fill="url(#b)"/>
    <rect x="280" y="380" width="240" height="150" fill="{accent}"/>
    <rect x="280" y="380" width="240" height="150" fill="url(#b)" opacity="0.25"/>
    <text x="400" y="468" text-anchor="middle" font-family="DejaVu Serif, serif" font-size="{44 if len(label) < 8 else 32}" font-weight="bold" letter-spacing="3" fill="{body}">{label}</text>
    <line x1="310" x2="490" y1="490" y2="490" stroke="{body}" stroke-width="2"/>
    <ellipse cx="400" cy="260" rx="120" ry="22" fill="{shade(body, 1.25)}"/>
    <path d="M276 240 h248 v26 h-248 z" fill="{shade(body, 0.8)}"/>
    <ellipse cx="400" cy="240" rx="124" ry="22" fill="{shade(body, 1.35)}"/>"""


def pouch(body, accent, label):
    return f"""
    <path d="M270 250 h260 l20 380 q-150 30 -300 0 z" fill="{body}"/>
    <path d="M270 250 h260 l4 60 h-268 z" fill="{shade(body, 0.8)}"/>
    <g stroke="{shade(body, 0.65)}" stroke-width="3">{"".join(f'<line x1="{x}" x2="{x + 6}" y1="254" y2="306"/>' for x in range(285, 520, 14))}</g>
    <rect x="300" y="360" width="200" height="190" rx="18" fill="{accent}"/>
    <text x="400" y="445" text-anchor="middle" font-family="DejaVu Serif, serif" font-size="{46 if len(label) < 5 else 36}" font-weight="bold" letter-spacing="4" fill="{body}">{label}</text>
    <text x="400" y="495" text-anchor="middle" font-family="DejaVu Sans, sans-serif" font-size="20" letter-spacing="6" fill="{shade(body, 0.8)}">LOOSE LEAF</text>
    <path d="M300 250 l-10 380" stroke="#fff" stroke-opacity="0.2" stroke-width="14"/>"""


KINDS = {
    "mug": mug,
    "mug-speckled": lambda b, a, l: mug(b, a, l, speckled=True),
    "cup": cup,
    "glass-mug": glass_mug,
    "iron-teapot": iron_teapot,
    "glass-teapot": glass_teapot,
    "tray": tray,
    "tin": tin,
    "pouch": pouch,
}

BACKGROUNDS = ["#f4ede4", "#eef0ea", "#efe9f0", "#e9eef2", "#f2eee6", "#f0ebe3"]


def svg(kind, body, accent, label, background):
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{SIZE}" height="{SIZE}" viewBox="0 0 800 800">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="{shade(background, 1.04)}"/><stop offset="1" stop-color="{shade(background, 0.94)}"/>
    </linearGradient>
    <filter id="blur"><feGaussianBlur stdDeviation="14"/></filter>
  </defs>
  <rect width="800" height="800" fill="url(#bg)"/>
  <ellipse cx="400" cy="650" rx="250" ry="34" fill="#000" opacity="0.16" filter="url(#blur)"/>
  {KINDS[kind](body, accent, label)}
</svg>"""


def hero():
    """A wide banner for the home page: three of the catalog's illustrations on a table, kept to
    the right half so the cover block's headline has room on the left."""
    pieces = [
        ("iron-teapot", "#2c2f33", "#8a6f4d", "", 560, 150, 0.9),
        ("tin", "#7b2d26", "#f2d6a2", "ASSAM", 860, 210, 0.75),
        ("glass-mug", "#e9f1f2", "#c26a1b", "", 1060, 265, 0.7),
    ]
    groups = "".join(
        f'<g transform="translate({x} {y}) scale({k})">{KINDS[kind](body, accent, label)}</g>'
        for kind, body, accent, label, x, y, k in pieces
    )
    # Each illustration declares its own gradient ids; suffixing them per piece keeps them apart.
    for n, (kind, *_rest) in enumerate(pieces):
        groups = groups.replace('id="b"', f'id="b{n}"', 1).replace("url(#b)", f"url(#b{n})", 2 if kind == "tin" else 1)
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="800" viewBox="0 0 1600 800">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#f6ead6"/><stop offset="1" stop-color="#e6d2b5"/></linearGradient>
    <filter id="blur"><feGaussianBlur stdDeviation="18"/></filter>
  </defs>
  <rect width="1600" height="800" fill="url(#bg)"/>
  <rect y="690" width="1600" height="110" fill="#c9a77c"/>
  <ellipse cx="800" cy="700" rx="760" ry="40" fill="#000" opacity="0.12" filter="url(#blur)"/>
  {groups}
</svg>"""


def render(source, out):
    png = subprocess.run(["rsvg-convert", "--format=png"], input=source.encode(), capture_output=True, check=True).stdout
    subprocess.run(["magick", "png:-", "-strip", "-quality", "82", "-sampling-factor", "4:2:0", str(out)], input=png, check=True)
    print(f"{out.name}: {out.stat().st_size // 1024} KB")


def main():
    with open(HERE / "catalog.tsv", newline="") as f:
        rows = [r for r in csv.reader(f, delimiter="\t") if r and not r[0].startswith("#")]
    for i, (slug, _name, _price, _category, image, _description) in enumerate(rows):
        kind, body, accent, *label = image.split(":")
        render(svg(kind, body, accent, label[0] if label else "", BACKGROUNDS[i % len(BACKGROUNDS)]), HERE / f"{slug}.jpg")
    render(hero(), HERE / "hero.jpg")


if __name__ == "__main__":
    main()
