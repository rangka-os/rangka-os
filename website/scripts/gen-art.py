"""Generate the landing page's dithered artwork into public/art/.

Every image is drawn here from simple shapes (no third-party artwork):
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


def frame_cube(w=150, h=170, scale=8):
    """Isometric open cube frame (rangka = frame). Returns gray, alpha."""
    W, H = w * scale, h * scale
    g = Image.new("L", (W, H), 0)
    a = Image.new("L", (W, H), 0)
    dg, da = ImageDraw.Draw(g), ImageDraw.Draw(a)
    s, ox, oy = W * 0.4, W * 0.5, H * 0.33

    def face(pts, shade):
        p = [iso(*q, s, ox, oy) for q in pts]
        dg.polygon(p, fill=shade)
        da.polygon(p, fill=255)

    t = 0.16  # beam thickness
    # back inner faces (seen through the windows), darkest
    face([(0, 0, 0), (1, 0, 0), (1, 0, 1), (0, 0, 1)], 235)
    face([(0, 0, 0), (0, 1, 0), (0, 1, 1), (0, 0, 1)], 210)
    face([(0, 0, 0), (1, 0, 0), (1, 1, 0), (0, 1, 0)], 250)
    # front faces with a window cut, as four beams each
    def beams(axis_pts, shade):
        A, B, C, D = axis_pts
        lerp = lambda p, q, u: tuple(p[i] + (q[i] - p[i]) * u for i in range(3))
        # outer ring polygon minus inner ring = frame
        inner = [lerp(lerp(A, C, t), lerp(B, D, t), 0) , None]
        o = [A, B, C, D]
        cx = tuple(sum(p[i] for p in o) / 4 for i in range(3))
        inn = [lerp(p, cx, t * 2.2) for p in o]
        for i in range(4):
            face([o[i], o[(i + 1) % 4], inn[(i + 1) % 4], inn[i]], shade)
    beams([(1, 0, 1), (1, 1, 1), (0, 1, 1), (0, 0, 1)], 40)    # top
    beams([(1, 0, 0), (1, 1, 0), (1, 1, 1), (1, 0, 1)], 130)   # right (x=1)
    beams([(0, 1, 0), (1, 1, 0), (1, 1, 1), (0, 1, 1)], 85)   # left (y=1)
    # floor shadow
    sh = Image.new("L", (W, H), 0)
    ImageDraw.Draw(sh).ellipse([W * 0.08, H * 0.62, W * 0.92, H * 0.86], fill=90)
    sh = sh.filter(ImageFilter.GaussianBlur(scale * 6))
    g = Image.fromarray(np.maximum(np.array(g), np.array(sh) * (np.array(a) == 0)).astype("uint8"))
    a = Image.fromarray(np.maximum(np.array(a), (np.array(sh) > 20) * 255).astype("uint8"))
    g, a = g.resize((w, h), Image.BILINEAR), a.resize((w, h), Image.BILINEAR)
    return (np.array(g) / 255.0) ** 1.0, np.array(a) / 255.0


def icon(kind, w=56, h=56, scale=10):
    W, H = w * scale, h * scale
    g = Image.new("L", (W, H), 0)
    d = ImageDraw.Draw(g)
    if kind == "blocks":  # stacked isometric blocks
        s = W * 0.2
        for (x, y, z) in [(0, 0, 0), (1, 0, 0), (0, 1, 0), (0, 0, 1)]:
            ox, oy = W * 0.5, H * 0.52
            P = lambda a, b, c: iso(a, b, c, s, ox, oy)
            d.polygon([P(x, y, z + 1), P(x + 1, y, z + 1), P(x + 1, y + 1, z + 1), P(x, y + 1, z + 1)], fill=110)
            d.polygon([P(x + 1, y, z), P(x + 1, y + 1, z), P(x + 1, y + 1, z + 1), P(x + 1, y, z + 1)], fill=200)
            d.polygon([P(x, y + 1, z), P(x + 1, y + 1, z), P(x + 1, y + 1, z + 1), P(x, y + 1, z + 1)], fill=160)
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


def wordmark(text="rangka", w=240, h=60):
    W, H = w * 8, h * 8
    g = Image.new("L", (W, H), 0)
    d = ImageDraw.Draw(g)
    try:
        f = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", int(H * 0.9))
    except OSError:
        f = ImageFont.load_default()
    bb = d.textbbox((0, 0), text, font=f)
    d.text(((W - (bb[2] - bb[0])) / 2 - bb[0], (H - (bb[3] - bb[1])) / 2 - bb[1]), text, font=f, fill=255)
    grad = np.linspace(0.55, 1.0, W)[None, :]
    arr = np.array(g.resize((w, h), Image.BILINEAR)) / 255.0
    grad = np.linspace(0.45, 0.95, w)[None, :]
    return arr * grad, (arr > 0.05).astype(float)


INK, BLUE, TEAL = (23, 26, 32, 255), (12, 102, 228, 255), (13, 148, 136, 255)

dither(*frame_cube(), INK, name="frame.png")
for k in ("blocks", "spark", "shield"):
    dither(*icon(k), BLUE, cell=(5, 4), bar=(4, 2), name=f"icon-{k}.png")
dither(*field(160, 130), (23, 26, 32, 255), name="field.png")
dither(*field(220, 90, seed=3, blur=7), (87, 157, 255, 255), name="field-dark.png")
dither(*wordmark(), (255, 255, 255, 255), cell=(6, 5), bar=(5, 3), name="wordmark.png")
print("art written to", os.path.abspath(OUT))
