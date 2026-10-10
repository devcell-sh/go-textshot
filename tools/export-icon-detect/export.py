"""Export OmniParser icon_detect_v3 (TorchScript YOLOv9-E, MIT) to ONNX.

The upstream weights ship only as TorchScript; this one-off developer step
produces the ONNX file that textshot downloads at runtime. The graph keeps the
raw detection heads (3 strides x [logits, ltrb distances]) with dynamic H/W so
the Go side can pick any input size that is a multiple of 32.

usage: export.py --out DIR [--cache DIR] [--fixture PNG]
"""
import argparse
import hashlib
import sys
import time
import urllib.request
from pathlib import Path

import numpy as np
import onnx
import onnxruntime as ort
import torch
from PIL import Image

HF_REPO = "microsoft/OmniParser-v2.0"
HF_REVISION = "f55d0750e5b94db2125ef0b45b0fa4a85ddc59b4"
PT_FILE = "icon_detect_v3/model.pt"
PT_SHA256 = "11c6cbb77f22569fab22d86c76407a83ec81ab89dbfe28279854822d6e3fb00c"
PT_SIZE = 281182680

OUT_NAME = "icon_detect_v3.onnx"
STRIDES = (8, 16, 32)
OPSET = 17


def sha256(p: Path) -> str:
    h = hashlib.sha256()
    with p.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def download_weights(cache: Path) -> Path:
    dest = cache / f"{HF_REVISION[:12]}-model.pt"
    if dest.exists() and dest.stat().st_size == PT_SIZE and sha256(dest) == PT_SHA256:
        return dest
    url = f"https://huggingface.co/{HF_REPO}/resolve/{HF_REVISION}/{PT_FILE}"
    print(f"downloading {url}")
    cache.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_suffix(".part")
    urllib.request.urlretrieve(url, tmp)
    if (got := sha256(tmp)) != PT_SHA256:
        tmp.unlink()
        sys.exit(f"sha256 mismatch for {PT_FILE}: {got}")
    tmp.rename(dest)
    return dest


def letterbox(img, w, h):
    iw, ih = img.size
    s = min(w / iw, h / ih)
    rw, rh = int(iw * s), int(ih * s)
    pl, pt = (w - rw) // 2, (h - rh) // 2
    canvas = Image.new("RGB", (w, h), (114, 114, 114))
    canvas.paste(img.resize((rw, rh), Image.Resampling.LANCZOS), (pl, pt))
    x = np.asarray(canvas, dtype=np.float32).transpose(2, 0, 1) / 255.0
    return x[None], s, pl, pt


def decode(outs):
    scores, boxes = [], []
    for i, stride in zip(range(0, 6, 2), STRIDES):
        lg, dist = outs[i], outs[i + 1]
        _, c, h, w = lg.shape
        lg = lg.transpose(0, 2, 3, 1).reshape(-1, c)
        dist = dist.transpose(0, 2, 3, 1).reshape(-1, 4) * stride
        gy, gx = np.meshgrid(np.arange(h), np.arange(w), indexing="ij")
        anc = (np.stack((gx, gy), -1).reshape(-1, 2) + 0.5) * stride
        boxes.append(np.concatenate((anc - dist[:, :2], anc + dist[:, 2:]), -1))
        scores.append(lg)
    return 1 / (1 + np.exp(-np.concatenate(scores))), np.concatenate(boxes)


def nms(boxes, scores, iou=0.7):
    order = scores.argsort()[::-1]
    keep = []
    area = (boxes[:, 2] - boxes[:, 0]) * (boxes[:, 3] - boxes[:, 1])
    while order.size:
        i = order[0]
        keep.append(i)
        xx1 = np.maximum(boxes[i, 0], boxes[order[1:], 0])
        yy1 = np.maximum(boxes[i, 1], boxes[order[1:], 1])
        xx2 = np.minimum(boxes[i, 2], boxes[order[1:], 2])
        yy2 = np.minimum(boxes[i, 3], boxes[order[1:], 3])
        inter = np.clip(xx2 - xx1, 0, None) * np.clip(yy2 - yy1, 0, None)
        ov = inter / (area[i] + area[order[1:]] - inter)
        order = order[1:][ov <= iou]
    return keep


def run_torch(m, x):
    with torch.inference_mode(), torch.jit.optimized_execution(False):
        return [t.numpy() for t in m(torch.from_numpy(x))]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--cache", type=Path, default=Path.home() / ".cache" / "textshot-export")
    ap.add_argument("--fixture", type=Path)
    a = ap.parse_args()

    pt = download_weights(a.cache)
    m = torch.jit.load(str(pt), map_location="cpu").eval()

    names = [f"{k}_s{s}" for s in STRIDES for k in ("logits", "ltrb")]
    dyn = {"images": {0: "batch", 2: "height", 3: "width"}}
    dyn.update({n: {0: "batch", 2: "h", 3: "w"} for n in names})
    a.out.mkdir(parents=True, exist_ok=True)
    out = a.out / OUT_NAME
    torch.onnx.export(
        m, torch.zeros(1, 3, 640, 640), str(out),
        input_names=["images"], output_names=names, dynamic_axes=dyn,
        opset_version=OPSET, do_constant_folding=True, dynamo=False,
    )
    onnx.checker.check_model(str(out))

    sess = ort.InferenceSession(str(out), providers=["CPUExecutionProvider"])
    print("inputs: ", [(i.name, i.shape) for i in sess.get_inputs()])
    print("outputs:", [(o.name, o.shape) for o in sess.get_outputs()])

    worst = 0.0
    for w, h in ((640, 640), (1280, 736), (1920, 1088)):
        x = np.random.default_rng(0).random((1, 3, h, w), dtype=np.float32)
        ref = run_torch(m, x)
        t0 = time.perf_counter()
        got = sess.run(None, {"images": x})
        dt = time.perf_counter() - t0
        d = max(float(np.abs(r - g).max()) for r, g in zip(ref, got))
        worst = max(worst, d)
        print(f"  {w}x{h}: onnxruntime {dt*1000:.0f} ms, max|diff| vs torch {d:.2e}")
    if worst > 1e-3:
        sys.exit(f"ONNX output diverges from torch: {worst}")

    digest = sha256(out)
    size = out.stat().st_size
    (a.out / f"{OUT_NAME}.sha256").write_text(f"{digest}  {OUT_NAME}\n")
    print(f"\n{out}\n  sha256 {digest}\n  size   {size}")

    if a.fixture:
        img = Image.open(a.fixture).convert("RGB")
        for w, h in ((1280, 736), (1920, 1088)):
            x, s, pl, pt_ = letterbox(img, w, h)
            t0 = time.perf_counter()
            sc, bx = decode(sess.run(None, {"images": x}))
            dt = time.perf_counter() - t0
            sc = sc.max(-1)
            keep = sc > 0.05
            sc, bx = sc[keep], bx[keep]
            bx[:, [0, 2]] = (bx[:, [0, 2]] - pl) / s
            bx[:, [1, 3]] = (bx[:, [1, 3]] - pt_) / s
            k = nms(bx, sc)
            print(f"\n{a.fixture} @ {w}x{h}: {dt*1000:.0f} ms, {len(k)} boxes")
            for i in sorted(k, key=lambda i: (bx[i, 1], bx[i, 0])):
                print(f"  [{bx[i,0]:6.0f} {bx[i,1]:6.0f} {bx[i,2]:6.0f} {bx[i,3]:6.0f}] {sc[i]:.2f}")


if __name__ == "__main__":
    main()
