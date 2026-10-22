#!/usr/bin/env python3
# Copyright 2026 The pgtenant Authors
# SPDX-License-Identifier: MIT
"""Render the README visual. Requires Python 3.10+ and Pillow 11+."""
import json
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

HERE = Path(__file__).resolve().parent
DATA = json.loads((HERE / "scenes.json").read_text())
W, H = 1100, 650
BG, INK, MUTED = "#f5f3ee", "#192c32", "#63727a"
ACCENT = DATA["accent"]

def font(size, mono=False, bold=False):
    candidates = (["/System/Library/Fonts/Menlo.ttc", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"] if mono else
                  (["/System/Library/Fonts/Supplemental/Arial Bold.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"] if bold else
                   ["/System/Library/Fonts/Supplemental/Arial.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"]))
    for name in candidates:
        if Path(name).exists():
            return ImageFont.truetype(name, size)
    raise RuntimeError("Install Arial/Menlo or DejaVu Sans/Mono fonts.")

def text(d, xy, value, size=24, fill=INK, mono=False, bold=False, width=None):
    f = font(size, mono, bold)
    lines = []
    for para in str(value).split("\n"):
        line = ""
        for word in para.split(" "):
            trial = (line + " " + word).strip()
            if width and d.textlength(trial, font=f) > width:
                if not line:
                    raise ValueError("Unbreakable text exceeds its panel: " + word)
                lines.append(line)
                line = word
            else:
                line = trial
        lines.append(line)
    for i, line in enumerate(lines):
        d.text((xy[0], xy[1] + i*(size+9)), line, fill=fill, font=f)
    return len(lines)*(size+9)

def pill(d, xy, label, color, dark=False):
    f=font(18,bold=True); w=d.textlength(label,font=f)+26
    d.rounded_rectangle((xy[0],xy[1],xy[0]+w,xy[1]+32),radius=12,fill=color if dark else "#eaf0ee")
    text(d,(xy[0]+13,xy[1]+5),label,18,fill="white" if dark else color,bold=True)

def panel(im, d, block, phase):
    x,y,w,h = block["box"]
    kind=block.get("kind","rows")
    dark=kind=="code"
    d.rounded_rectangle((x,y,x+w,y+h),radius=18,fill="#172b34" if dark else "#ffffff",outline="#d8dfdb",width=1)
    color="#ecf4f5" if dark else INK
    text(d,(x+24,y+22),block["label"],20,fill="#9eb8c0" if dark else MUTED,bold=True)
    if kind=="image":
        src=Image.open(HERE/block["file"]).convert("RGB")
        src.thumbnail((w-24,h-72),Image.Resampling.LANCZOS)
        im.paste(src,(x+(w-src.width)//2,y+58))
        return
    if kind=="steps":
        rows=block["rows"]
        for i,row in enumerate(rows):
            yy=y+82+i*74
            if yy+42>y+h-10:raise ValueError("Steps overflow")
            d.ellipse((x+25,yy,x+59,yy+34),fill=ACCENT if i<=phase else "#dce4e1")
            text(d,(x+36,yy+5),str(i+1),18,fill="white",bold=True)
            text(d,(x+77,yy+3),row,23,fill=color,width=w-100)
            if i<len(rows)-1:d.line((x+42,yy+36,x+42,yy+72),fill="#dce4e1",width=2)
        return
    if kind=="metric":
        text(d,(x+24,y+85),block["value"],66,fill=block.get("color",ACCENT),bold=True)
        text(d,(x+26,y+173),block["detail"],22,fill=MUTED,width=w-48)
        return
    if kind=="code":
        yy=y+72
        for line in block["rows"]:
            if isinstance(line,dict):value=line["text"];c=line.get("color",color)
            else:value=line;c=color
            yy+=text(d,(x+24,yy),value,block.get("size",22),fill=c,mono=True,width=w-48)+10
        if yy>y+h+5:raise ValueError("Code overflows: "+block["label"])
        return
    yy=y+70
    for row in block["rows"]:
        row={"text":row} if isinstance(row,str) else row
        state=row.get("state","neutral")
        colors={"good":("#e6f2eb","#186c4d"),"bad":("#fae7e5","#ac3b39"),"muted":("#edf0ef","#8c999c"),"neutral":("#eef2f3",INK),"accent":("#eeebfa",ACCENT)}
        bg,fg=colors[state]
        f=font(block.get("size",23));height=60
        if d.textlength(row["text"],font=f)>w-82:height=92
        d.rounded_rectangle((x+18,yy,x+w-18,yy+height),radius=10,fill=bg)
        text(d,(x+32,yy+17),row["text"],block.get("size",23),fill=fg,width=w-64,bold=state in ("good","bad"))
        yy+=height+12
    if yy>y+h+5:raise ValueError("Rows overflow: "+block["label"])

def render(scene, index, tick):
    im=Image.new("RGB",(W,H),BG);d=ImageDraw.Draw(im)
    text(d,(36,25),DATA["name"],22,fill=ACCENT,bold=True)
    text(d,(36,70),scene["title"],36,bold=True)
    if len(DATA["scenes"]) > 1:
        pill(d,(958,28),f"{index+1} / {len(DATA['scenes'])}",ACCENT)
    for block in scene["blocks"]:panel(im,d,block,index)
    for path in scene.get("arrows",[]):
        x1,y1,x2,y2=path
        d.line(path,fill=ACCENT,width=3)
        d.polygon([(x2,y2),(x2-10,y2-6),(x2-10,y2+6)],fill=ACCENT)
        t=tick/8;px=x1+(x2-x1)*t;py=y1+(y2-y1)*t
        d.ellipse((px-5,py-5,px+5,py+5),fill=ACCENT)
    text(d,(36,604),DATA["disclosure"],17,fill=MUTED,width=1028)
    for i in range(len(DATA["scenes"])):
        xx=36+i*(1028/len(DATA["scenes"]))
        d.rounded_rectangle((xx,637,xx+1028/len(DATA["scenes"])-8,641),radius=2,fill=ACCENT if i<=index else "#dce2df")
    return im

if __name__ == "__main__":
    output=HERE.parent/DATA["output"]
    if output.suffix==".png":render(DATA["scenes"][0],0,4).save(output)
    else:
        frames=[]
        for i,scene in enumerate(DATA["scenes"]):
            frames.extend(render(scene,i,t).quantize(colors=128) for t in range(9))
        frames[0].save(output,save_all=True,append_images=frames[1:],duration=450,loop=0,optimize=False,disposal=2)
    print(output.name)
