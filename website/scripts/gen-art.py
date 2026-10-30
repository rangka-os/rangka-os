"""Generate the landing page's dithered artwork into public/art/.

Every image is drawn here from simple shapes and the Rangka logo kit:
greyscale shapes -> ordered (Bayer) dither -> each lit cell drawn as a short
horizontal bar, which gives the "scanline" look. Run: python3 scripts/gen-art.py
"""
import os
import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

OUT = os.path.join(os.path.dirname(__file__), "..", "public", "art")
os.makedirs(OUT, exist_ok=True)

BAYER = np.array([[0, 8, 2, 10], [12, 4, 14, 6], [3, 11, 1, 9], [15, 7, 13, 5]]) / 16.0


def dither(gray, alpha, color, cell=(6, 4), bar=(5, 2), name="x.png", invert=False):
    """gray, alpha: float arrays (rows, cols) in 0..1. gray 1 = dark/ink."""
    rows, cols = gray.shape
    th = np.tile(BAYER, (rows // 4 + 1, cols // 4 + 1))[:rows, :cols]
    on = (gray > th) & (alpha > 0.5)
    cw, ch = cell
    img = Image.new("RGBA", (cols * cw, rows * ch), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    for y, x in zip(*np.nonzero(on)):
        d.rectangle([x * cw, y * ch, x * cw + bar[0] - 1, y * ch + bar[1] - 1], fill=color)
    img.save(os.path.join(OUT, name), optimize=True)
    return img


def iso(x, y, z, s, ox, oy):
    return (ox + (x - y) * s * 0.866, oy + (x + y) * s * 0.5 - z * s)


SENDI = [(35, 16), (147, 16), (225, 94), (113, 94), (113, 240), (35, 240)]  # brand/rangka-symbol-small.svg


def sendi_block(w=150, h=170, scale=8, depth=46):
    """The Rangka symbol (Sendi, small cut) extruded into a solid beam joint. Returns gray, alpha."""
    W, H = w * scale, h * scale
    g = Image.new("L", (W, H), 0)
    a = Image.new("L", (W, H), 0)
    dg, da = ImageDraw.Draw(g), ImageDraw.Draw(a)
    k = W / 330.0
    ox, oy = W * 0.16, H * 0.2
    pts = [(ox + x * k, oy + y * k) for x, y in SENDI]
    # floor shadow first
    sh = Image.new("L", (W, H), 0)
    ImageDraw.Draw(sh).ellipse([W * 0.06, H * 0.8, W * 0.94, H * 0.97], fill=80)
    sh = sh.filter(ImageFilter.GaussianBlur(scale * 6))
    # extrusion: copies stepping up and right, darkest at the back
    dx, dy = 0.62 * k, -0.42 * k
    for i in range(depth, 0, -1):
        q = [(x + dx * i, y + dy * i) for x, y in pts]
        dg.polygon(q, fill=210)
        da.polygon(q, fill=255)
    # front face: light at the top, a little denser at the foot
    face = Image.new("L", (W, H), 0)
    ImageDraw.Draw(face).polygon(pts, fill=255)
    grad = np.linspace(40, 115, H)[:, None] * np.ones((1, W))
    m = np.array(face) > 0
    gg = np.array(g, dtype=float)
    gg[m] = grad[m]
    aa = np.maximum(np.array(a), np.array(face))
    sha = np.array(sh, dtype=float)
    out_g = np.where(aa > 0, gg, sha)
    out_a = np.where((aa > 0) | (sha > 20), 255, 0)
    g = Image.fromarray(out_g.astype("uint8")).resize((w, h), Image.BILINEAR)
    a = Image.fromarray(out_a.astype("uint8")).resize((w, h), Image.BILINEAR)
    return np.array(g) / 255.0, np.array(a) / 255.0


def icon(kind, w=56, h=56, scale=10):
    W, H = w * scale, h * scale
    g = Image.new("L", (W, H), 0)
    d = ImageDraw.Draw(g)
    if kind == "beams":  # three beams with 45-degree ends, stacked like a frame
        bh = H * 0.2
        for i, (x0, x1, shade) in enumerate([(0.10, 0.90, 200), (0.18, 0.82, 150), (0.26, 0.74, 110)]):
            y = H * 0.16 + i * (bh + H * 0.06)
            d.polygon([(W * x0, y + bh), (W * x0 + bh, y), (W * x1, y), (W * x1 - bh, y + bh)], fill=shade)
    elif kind == "spark":  # AI spark: four-point star
        cx, cy, r = W / 2, H / 2, W * 0.44
        pts = []
        for i in range(8):
            ang = np.pi / 4 * i - np.pi / 2
            rr = r if i % 2 == 0 else r * 0.22
            pts.append((cx + rr * np.cos(ang), cy + rr * np.sin(ang)))
        d.polygon(pts, fill=200)
        d.polygon([pts[0], pts[1], (cx, cy), pts[7]], fill=110)
        d.polygon([pts[2], pts[3], (cx, cy), pts[1]], fill=150)
    elif kind == "shield":
        cx = W / 2
        pts = [(cx, H * 0.06), (W * 0.88, H * 0.2), (W * 0.84, H * 0.56), (cx, H * 0.94), (W * 0.16, H * 0.56), (W * 0.12, H * 0.2)]
        d.polygon(pts, fill=200)
        d.polygon([(cx, H * 0.06), (W * 0.88, H * 0.2), (W * 0.84, H * 0.56), (cx, H * 0.94)], fill=130)
        d.line([(W * 0.32, H * 0.5), (W * 0.46, H * 0.64), (W * 0.7, H * 0.36)], fill=0, width=int(W * 0.08))
    a = (np.array(g) > 0).astype(float)
    g = g.resize((w, h), Image.BILINEAR)
    a = Image.fromarray((a * 255).astype("uint8")).resize((w, h), Image.BILINEAR)
    return np.array(g) / 255.0, np.array(a) / 255.0


def field(w, h, seed=7, blur=9):
    rng = np.random.default_rng(seed)
    n = Image.fromarray((rng.random((h, w)) * 255).astype("uint8")).filter(ImageFilter.GaussianBlur(blur))
    v = np.array(n, dtype=float)
    v = (v - v.min()) / (v.max() - v.min())
    yy = np.linspace(0, 1, h)[:, None]
    v = np.clip(v * 1.4 - 0.25 + yy * 0.35, 0, 1)
    return v, np.ones_like(v)


def wordmark(w=240, h=76):
    """The real Rangka wordmark (brand/rangka-wordmark.svg), filled even-odd."""
    import re
    src = open(os.path.join(os.path.dirname(__file__), "..", "public", "brand", "rangka-wordmark.svg")).read()
    vb = [float(v) for v in re.search(r'viewBox="([^"]+)"', src).group(1).split()]
    S = 4
    W, H = w * S, h * S
    k = min(W / vb[2], H / vb[3]) * 0.9
    acc = np.zeros((H, W), dtype=bool)
    for d_attr in re.findall(r' d="([^"]+)"', src):
        for sub in re.findall(r"M[^M]+", d_attr):
            nums = [float(v) for v in re.findall(r"-?\d+(?:\.\d+)?", sub)]
            pts = [((nums[i] - vb[0]) * k + (W - vb[2] * k) / 2, (nums[i + 1] - vb[1]) * k + (H - vb[3] * k) / 2) for i in range(0, len(nums) - 1, 2)]
            if len(pts) < 3:
                continue
            m = Image.new("1", (W, H), 0)
            ImageDraw.Draw(m).polygon(pts, fill=1)
            acc ^= np.array(m, dtype=bool)
    arr = np.array(Image.fromarray((acc * 255).astype("uint8")).resize((w, h), Image.BILINEAR)) / 255.0
    grad = np.linspace(0.45, 0.95, w)[None, :]
    return arr * grad, (arr > 0.05).astype(float)


# Rangka brand colours (logo kit v1.0): Ink #1C1917, Bata #B4432A, Bata Terang #E0623F
INK, BATA, BATA_TERANG = (28, 25, 23, 255), (180, 67, 42, 255), (224, 98, 63, 255)

dither(*sendi_block(), INK, name="frame.png")
for k in ("beams", "spark", "shield"):
    dither(*icon(k), BATA, cell=(5, 4), bar=(4, 2), name=f"icon-{k}.png")
dither(*field(160, 130), INK, name="field.png")
dither(*field(220, 90, seed=3, blur=7), BATA_TERANG, name="field-dark.png")
dither(*wordmark(), (255, 255, 255, 255), cell=(6, 5), bar=(5, 3), name="wordmark.png")
print("art written to", os.path.abspath(OUT))
