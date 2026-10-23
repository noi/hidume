"""ヒヅメ (Hidume) のアプリアイコンジェネレータ。

python3 hidume_icon.py <build ディレクトリ>

出力:
  <build>/appicon.png        1024x1024（Wails が macOS の .icns をここから作る）
  <build>/windows/icon.ico   16〜256px を内包した ICO

macOS のアイコングリッド（1024 のキャンバスに 824 の角丸スクエア）に合わせ、
Liquid Glass 風のガラス質な背景（グラデーション・上部の光沢・縁のハイライト）にドット絵のヒヅメを載せる。
"""
import math, struct, sys, zlib
from hidume_sprite import PALETTE, build

TOP    = (0x9A, 0xD8, 0xF7)   # 背景グラデーション上端
BOTTOM = (0x3B, 0x82, 0xD4)   # 背景グラデーション下端
SHADE  = (0x10, 0x2A, 0x4E)   # 影の色

def clamp(v, lo=0.0, hi=1.0): return lo if v < lo else hi if v > hi else v
def mix(a, b, t): return tuple(a[i] + (b[i]-a[i])*t for i in range(3))

def squircle_dist(x, y, half):
    """中心からの超楕円（n=4）距離 − half。輪郭付近ではおおよそピクセル距離になる。"""
    n = 4
    r = (abs(x)**n + abs(y)**n) ** (1/n)
    return r - half

def over(dst, src, a):
    """dst (r,g,b,a: 色は非プリマルチ 0-255, a は 0-1) の上に src を不透明度 a で重ねる。"""
    if a <= 0: return dst
    r, g, b, da = dst
    oa = a + da*(1-a)
    if oa <= 0: return (0, 0, 0, 0)
    k = da*(1-a)
    return ((src[0]*a + r*k)/oa, (src[1]*a + g*k)/oa, (src[2]*a + b*k)/oa, oa)

def sprite_layout(size, rows):
    """スプライトの倍率と左上座標。整数倍で拡大してドットを揃える。"""
    h, w = len(rows), len(rows[0])
    ys = [y for y in range(h) if rows[y].strip('.')]
    xs = [x for x in range(w) if any(r[x] != '.' for r in rows)]
    scale = max(1, int(size * 18 / 1024))
    cx = (xs[0] + xs[-1] + 1) / 2
    cy = (ys[0] + ys[-1] + 1) / 2
    ox = round(size/2 - cx*scale)
    oy = round(size/2 - cy*scale + size*0.01)
    return scale, ox, oy, (ys[-1]+1)*scale + oy

def render(size):
    rows = build('normal')
    s = size / 1024
    half = 412 * s                     # 824 / 2
    c = size / 2
    scale, ox, oy, feet = sprite_layout(size, rows)
    sh_off = 14 * s                    # スプライトの落ち影のずれ
    px = []
    for y in range(size):
        line = []
        for x in range(size):
            fx, fy = x + 0.5 - c, y + 0.5 - c
            col = (0, 0, 0, 0)
            # アイコン全体の影（下にずらしてぼかす）
            ds = squircle_dist(fx, fy - 12*s, half)
            col = over(col, SHADE, 0.30 * (1 - clamp((ds + 18*s) / (44*s))))
            d = squircle_dist(fx, fy, half)
            cov = clamp(0.5 - d)
            if cov > 0:
                t = clamp((fy + half) / (2*half))
                base = mix(TOP, BOTTOM, t)
                # 上部の光沢（左上寄りの楕円）
                ex, ey = (fx + 90*s) / (560*s), (fy + 330*s) / (360*s)
                gloss = clamp(1 - math.hypot(ex, ey)) ** 1.4 * 0.38
                base = mix(base, (255, 255, 255), gloss)
                # 足元の接地影
                gx, gy = fx / (230*s), (y + 0.5 - feet + 6*s) / (30*s)
                base = mix(base, SHADE, clamp(1 - math.hypot(gx, gy)) ** 1.2 * 0.35)
                # スプライトの落ち影
                sx, sy = int((x - ox) // scale), int((y - sh_off - oy) // scale)
                if 0 <= sy < len(rows) and 0 <= sx < len(rows[0]) and x >= ox and y - sh_off >= oy and rows[sy][sx] != '.':
                    base = mix(base, SHADE, 0.28)
                # 縁のハイライト（左上が強く、右下は屈折っぽく弱めに光る）
                r = math.hypot(fx, fy) or 1
                facing = (-fx*0.55 - fy*0.85) / r
                band = clamp(1 - abs(d + 5*s) / (5*s)) if size >= 64 else 0
                rim = band * (0.65*max(0, facing) + 0.30*max(0, -facing))
                base = mix(base, (255, 255, 255), rim)
                col = over(col, base, cov)
            # ヒヅメ本体
            sx, sy = (x - ox) // scale, (y - oy) // scale
            if x >= ox and y >= oy and sy < len(rows) and sx < len(rows[0]):
                p = PALETTE[rows[sy][sx]]
                if p: col = over(col, p, 1)
            line.append(col)
        px.append(line)
    return px

def downsample(px, f):
    n = len(px) // f
    out = []
    for y in range(n):
        line = []
        for x in range(n):
            acc = [0.0]*4
            for yy in range(y*f, y*f+f):
                for xx in range(x*f, x*f+f):
                    r, g, b, a = px[yy][xx]
                    acc[0] += r*a; acc[1] += g*a; acc[2] += b*a; acc[3] += a
            a = acc[3] / (f*f)
            line.append((acc[0]/acc[3], acc[1]/acc[3], acc[2]/acc[3], a) if acc[3] else (0, 0, 0, 0))
        out.append(line)
    return out

def png(px):
    h, w = len(px), len(px[0])
    raw = bytearray()
    for line in px:
        raw.append(0)
        for r, g, b, a in line:
            raw.extend((round(r), round(g), round(b), round(a*255)))
    def chunk(t, d):
        c = t + d
        return struct.pack('>I', len(d)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(bytes(raw), 9)) + chunk(b'IEND', b''))

def ico(images):
    """images: [(size, png_bytes)] を PNG 内包の ICO にまとめる。"""
    head = struct.pack('<HHH', 0, 1, len(images))
    offset = 6 + 16*len(images)
    entries, body = b'', b''
    for size, data in images:
        entries += struct.pack('<BBBBHHII', size % 256, size % 256, 0, 0, 1, 32, len(data), offset)
        body += data; offset += len(data)
    return head + entries + body

if __name__ == '__main__':
    outdir = sys.argv[1] if len(sys.argv) > 1 else '.'
    open(f'{outdir}/appicon.png', 'wb').write(png(render(1024)))
    images = []
    for size in (256, 128, 64, 48, 32, 16):
        # 小さいサイズは 64px 以上で描いてから縮小する（ドット絵の倍率が 1 未満になるため）
        f = 1 if size >= 64 else math.ceil(64 / size)
        px = render(size * f)
        images.append((size, png(downsample(px, f) if f > 1 else px)))
    open(f'{outdir}/windows/icon.ico', 'wb').write(ico(images))
