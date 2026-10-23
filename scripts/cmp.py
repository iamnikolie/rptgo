import sys, fitz, collections
def pages(p):
    d = fitz.open(p)
    return [[w[4] for w in pg.get_text("words")] for pg in d]
g, o = pages(sys.argv[1]), pages(sys.argv[2])
print(f"pages golden={len(g)} ours={len(o)}")
tot_g = tot_o = diff = 0
for i in range(max(len(g), len(o))):
    a = collections.Counter(g[i] if i < len(g) else [])
    b = collections.Counter(o[i] if i < len(o) else [])
    tot_g += sum(a.values()); tot_o += sum(b.values())
    miss, extra = a - b, b - a
    if miss or extra:
        diff += 1
        print(f"p{i+1}: golden-only {dict(miss)}  ours-only {dict(extra)}")
print(f"words golden={tot_g} ours={tot_o} pages-with-diff={diff}")
