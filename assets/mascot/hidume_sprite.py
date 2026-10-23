"""ヒヅメ (Hidume) マスコットのドット絵ジェネレータ（32x32）。

python3 hidume_sprite.py <出力ディレクトリ> [--preview <確認用ディレクトリ>]

出力:
  hidume{,_angry,_laugh,_sad}.png        32x32 透過 PNG
  hidume{,_angry,_laugh,_sad}_x8.png     8倍
  hidume{,_angry,_laugh,_sad}_x16.png    16倍
  hidume{,_angry,_laugh,_sad}.svg        ベクター
  hidume_sheet.png                       4表情を横に並べたシート（128x32 透過）
--preview を付けると、緑背景の確認用 PNG を指定ディレクトリに書き出す。
"""
import math, struct, sys, zlib

PALETTE = {
    '.': None,
    'o': (0x3A, 0x2E, 0x2A),  # 輪郭
    'W': (0xFB, 0xF8, 0xF0),  # 羊毛
    'w': (0xEC, 0xE6, 0xD9),  # 羊毛の影
    'F': (0xF7, 0xDF, 0xC2),  # 顔・脚
    'f': (0xE6, 0xC4, 0x9F),  # 顔の影
    'd': (0xD2, 0xA9, 0x82),  # 顔の濃い影
    'L': (0xFC, 0xEC, 0xD8),  # 顔のハイライト
    'E': (0x3A, 0x2E, 0x2A),  # 目
    'B': (0x7A, 0x58, 0x44),  # 眉毛
    'C': (0xF4, 0xA9, 0xA4),  # ほっぺ
    'M': (0x3A, 0x2E, 0x2A),  # 口
    'P': (0xA8, 0x5C, 0x5A),  # 鼻
    'T': (0x86, 0xC9, 0xF2),  # 涙
    'G': (0xFF, 0xD9, 0x4A),  # キラキラ
    'N': (0xE2, 0xB2, 0x6E),  # 角
    'n': (0xB9, 0x82, 0x3E),  # 角の影
    'Y': (0xF2, 0xD0, 0x96),  # 角のハイライト
    'R': (0xC9, 0x3B, 0x49),  # 首輪・怒りマーク
    'r': (0xE8, 0x5E, 0x6B),  # 首輪のハイライト
    'h': (0x4E, 0x3B, 0x33),  # 蹄
    'K': (0x9C, 0x82, 0x72),  # 蹄の光沢
    'j': (0x3E, 0x2E, 0x28),  # 蹄の濃い影
}

BW, H = 24, 32     # 体の描画幅・キャンバス高さ
OX = 4             # 横余白（角のぶん）
OY = 1             # 上余白
W = BW + OX*2      # キャンバス幅 = 32
CX = 11.5          # 体の左右対称の中心（体座標）

# ---- 体（羊毛の卵型）: 行ごとの輪郭の列（体座標）----
EGG = {2:(7,16),3:(6,17),4:(5,18),5:(4,19),6:(3,20),7:(3,20),8:(2,21)}
for y in range(9, 22): EGG[y] = (1,22)
EGG.update({22:(2,21),23:(3,20),24:(4,19)})

def egg_edge(y):
    l, r = EGG[y]
    if 9 <= y <= 23 and y % 4 in (1, 2): l, r = l-1, r+1   # 縁をスカラップ状に
    return l, r

# ---- 表情の画素: (行, 列, 色) 体座標。顔の中心は (11.5, 10.5)、鼻は常に 2x2 (12-13, 11-12) ----
_NOSE   = [(12,11,'P'), (12,12,'P'), (13,11,'M'), (13,12,'M')]   # 2x2 の鼻（下半分は口の色）
_CHEEKS = [(11,7,'C'), (11,16,'C')]
_BROWS  = [(9,10,'B'), (9,13,'B')]                               # 八の字眉（全表情で共通）
_EYES   = [(10,8,'E'), (10,9,'E'), (10,14,'E'), (10,15,'E')]     # 一の字の目
_OMEGA  = [(13,9,'M'), (14,10,'M'), (13,14,'M'), (14,13,'M')]    # 鼻の下に続く ω
_FROWN  = [(14,10,'M'), (15,9,'M'), (14,13,'M'), (15,14,'M')]    # への字
FACES = {
    'normal': _BROWS + _EYES + _CHEEKS + _NOSE + _OMEGA,
    # 怒り: 目を内側が下がる向きに傾ける + への字 + 怒りマーク
    'angry': _BROWS + [(10,8,'E'), (11,9,'E'), (10,15,'E'), (11,14,'E')]
             + _FROWN + [(6,14,'R'), (6,16,'R'), (7,15,'R'), (8,14,'R'), (8,16,'R')]
             + _CHEEKS + _NOSE,
    # 笑い: >< の目 + 大きな U の口 + キラキラ
    'laugh': _BROWS + [(9,8,'E'), (10,9,'E'), (11,8,'E'), (9,15,'E'), (10,14,'E'), (11,15,'E')]
             + [(13,9,'M'), (13,14,'M'), (14,10,'M'), (14,11,'M'), (14,12,'M'), (14,13,'M')]
             + [(12,19,'G'), (13,18,'G'), (13,19,'G'), (13,20,'G'), (14,19,'G'),  # 右のキラキラ
                (11,4,'G'), (12,3,'G'), (12,4,'G'), (12,5,'G'), (13,4,'G')]       # 左のキラキラ
             + _CHEEKS + _NOSE,
    # 悲しみ: 目を外側が下がる向きに傾ける + への字 + 涙
    'sad': _BROWS + [(10,9,'E'), (11,8,'E'), (10,14,'E'), (11,15,'E')]
           + _FROWN + [(12,8,'T'), (13,8,'T')] + _CHEEKS + _NOSE,
}

# ---- 太い一巻きの角。光源は左上で固定（右の角は形だけ反転し、陰影は反転しない）----
HORN_L = ["...ooooooooo", "..oYYNNNNNNN", ".oYNNNNNNNNN", "oNNNNNNoooo.", "oNNNNooNNo..",
          "oNNNNooNNo..", "oNNNNNNNNo..", "oNNNNNNnno..", ".onnnnnno...", "..oooooo...."]
HORN_R = ["ooooooooo...", "YYNNNNNNNo..", "YNNNNNNNNNo.", ".ooooNNNNNNo", "..oNNooNNNNo",
          "..oNNooNNNNo", "..oNNNNNNNNo", "..oNNNNNnnno", "...oNnnnnno.", "....oooooo.."]
HORN_Y = 2 + OY    # 角の上端の行

def blit(g, rows, x0, y0):
    for dy, r in enumerate(rows):
        for dx, ch in enumerate(r):
            if ch != '.':
                g[y0+dy][x0+dx] = ch

def mirror(rows):
    return [r[::-1] for r in rows]

def build_body(expr):
    g = [['.']*BW for _ in range(H)]
    # 体
    for y in EGG:
        l, r = egg_edge(y)
        g[y][l] = g[y][r] = 'o'
        for x in range(l+1, r): g[y][x] = 'W'
    for x in range(5, 19): g[25][x] = 'o'
    blit(g, ["..oo..oo..", "ooWWooWWoo"], 7, 0)   # 頭頂の毛の房
    # 羊毛の影（下側の縁）と毛のカール
    for y in range(17, 25):
        for x in range(0, BW-1):
            if g[y][x]=='W' and (g[y+1][x]=='o' or g[y][x+1]=='o'):
                g[y][x]='w'
    curl = [".ww", "w.."]
    for (x,y) in [(4,14),(5,19),(17,15),(16,21),(9,22)]:
        for dy,r in enumerate(curl):
            for dx,ch in enumerate(r):
                if ch!='.' and g[y+dy][x+dx]=='W': g[y+dy][x+dx]='w'
    # 顔（真円、左上からの光で球面の陰影）
    cy, rad = 10.5, 6.0
    face = {(x,y) for y in range(H) for x in range(BW) if math.hypot(x-CX, y-cy) <= rad}
    for (x,y) in face:
        edge = any((x+dx,y+dy) not in face for dx,dy in ((1,0),(-1,0),(0,1),(0,-1)))
        g[y][x] = 'o' if edge else 'F'
    lx, ly = CX-2.0, cy-2.2
    for (x,y) in face:
        if g[y][x] != 'F': continue
        d = math.hypot(x-lx, y-ly)
        if d > rad+2.4: g[y][x] = 'd'
        elif d > rad+0.8: g[y][x] = 'f'
        elif d < 1.9: g[y][x] = 'L'
    # 表情
    for (y, x, ch) in FACES[expr]:
        g[y][x] = ch
    # 首輪（あご下に沿った帯）
    band = {(x,y) for y in range(H) for x in range(BW)
            if rad-0.6 <= math.hypot(x-CX, y-cy) <= rad+2.5 and y >= cy+4.2}
    for (x,y) in band:
        edge = any((x+dx,y+dy) not in band for dx,dy in ((1,0),(-1,0),(0,1),(0,-1)))
        g[y][x] = 'o' if edge else ('r' if x < CX-1 else 'R')
    # 腕（右と下に毛の影、先は割れ蹄。右腕も光は左上から）
    arm_l = ["..ooo.", ".oWWwo", "oWWWwo", "owwwwo", "oKhojo", ".oo.o."]
    arm_r = [".ooo..", "oWWwo.", "oWWWwo", "owwwwo", "oKohjo", ".o.oo."]
    blit(g, arm_l, 0, 18); blit(g, arm_r, 18, 18)
    # 脚（真っ直ぐ・裸足・割れ蹄。左ハイライト・右影）
    leg = ["oLFfo", "oKhjo", "ohojo", ".ooo."]
    blit(g, leg, 6, 26); blit(g, leg, 13, 26)
    return g

def build(expr='normal'):
    body = build_body(expr)
    g = [['.']*W for _ in range(H)]
    for y in range(H-OY):
        for x in range(BW):
            if body[y][x] != '.':
                g[y+OY][x+OX] = body[y][x]
    blit(g, HORN_L, 1, HORN_Y); blit(g, HORN_R, W-1-len(HORN_R[0]), HORN_Y)
    ear = [".ooo.", "oFFFo", ".ooo."]
    blit(g, ear, 2, 10+OY); blit(g, mirror(ear), 25, 10+OY)
    for (y, x, ch) in FACES[expr]:   # 表情の小物は角・耳より手前に出す
        g[y+OY][x+OX] = ch
    return [''.join(r) for r in g]

def png(rows, scale, bg=None):
    h, w = len(rows), len(rows[0])
    raw = bytearray()
    for y in range(h):
        line = bytearray([0])
        for x in range(w):
            c = PALETTE[rows[y][x]]
            px = (*c, 255) if c else ((*bg, 255) if bg else (0, 0, 0, 0))
            line.extend(bytes(px) * scale)
        raw.extend(line * scale)
    def chunk(t, d):
        c = t + d
        return struct.pack('>I', len(d)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w*scale, h*scale, 8, 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(bytes(raw), 9)) + chunk(b'IEND', b''))

def svg(rows):
    h, w = len(rows), len(rows[0])
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w*8}" height="{h*8}" shape-rendering="crispEdges">']
    for y in range(h):
        x = 0
        while x < w:
            ch = rows[y][x]; x2 = x
            while x2 < w and rows[y][x2] == ch: x2 += 1
            c = PALETTE[ch]
            if c: out.append(f'<rect x="{x}" y="{y}" width="{x2-x}" height="1" fill="#{c[0]:02x}{c[1]:02x}{c[2]:02x}"/>')
            x = x2
    out.append('</svg>')
    return '\n'.join(out)

if __name__ == '__main__':
    args = sys.argv[1:]
    preview_dir = None
    if '--preview' in args:
        i = args.index('--preview'); preview_dir = args[i+1]; del args[i:i+2]
    outdir = args[0] if args else '.'
    BG = (0xA8, 0xD8, 0xA0)
    sheet = [''] * H
    for name in FACES:
        rows = build(name)
        stem = 'hidume' if name == 'normal' else f'hidume_{name}'
        open(f'{outdir}/{stem}.png', 'wb').write(png(rows, 1))
        open(f'{outdir}/{stem}_x8.png', 'wb').write(png(rows, 8))
        open(f'{outdir}/{stem}_x16.png', 'wb').write(png(rows, 16))
        open(f'{outdir}/{stem}.svg', 'w').write(svg(rows))
        if preview_dir:
            open(f'{preview_dir}/{stem}_preview.png', 'wb').write(png(rows, 12, bg=BG))
        for y in range(H): sheet[y] += rows[y]
    open(f'{outdir}/hidume_sheet.png', 'wb').write(png(sheet, 1))
    if preview_dir:
        open(f'{preview_dir}/hidume_sheet_preview.png', 'wb').write(png(sheet, 8, bg=BG))
    print('expressions:', ', '.join(FACES))
