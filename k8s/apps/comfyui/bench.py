#!/usr/bin/env python3
"""Benchmark a ComfyUI server: the same fixed graphs on every GPU.

    bench.py --url http://127.0.0.1:8188 --label desktop-5070ti \
             [--cases krea2_turbo,h3_image,h3_t2v] [--warm 2] [--set k=v ...]

Per case: one COLD run (models dropped with /free first, so it pays
disk load + text encode + sampling + decode) and --warm runs with a
new seed and prompt (weights resident; pays text encode + sampling +
decode). Reported per run: wall time, per-node-class time, sampler
s/it (first step excluded: it carries the model move to the GPU), peak
VRAM. "Cold" is cold for the PROCESS — the page cache may still hold
the file; the first-ever run after boot is the true disk number, and
`load_s` shows it.

The graphs mirror the stock templates in comfyui-workflow-templates
(image_krea2_turbo_t2i, image_minimax_h3_image_t2i,
video_minimax_h3_t2v), flattened to API form with the prompt enhancer
left out. Model files default to the tier-1 set in models.txt — the
one every GPU can hold — and are overridable with --set, e.g.
    --set krea2_turbo.unet=krea2_turbo_int8_convrot.safetensors
Needs aiohttp (it is in the ComfyUI venv). Results append to
bench-results/<label>.jsonl; `bench.py --table` prints the comparison.
"""
import argparse
import asyncio
import glob
import json
import os
import time
import uuid

import aiohttp

PROMPTS = [
    "A lighthouse on a basalt cliff at dusk, long exposure, sea mist, a single warm window, 35mm film photograph",
    "A red fox asleep in a hollow log, autumn leaves, soft overcast light, macro lens, shallow depth of field",
    "A night market street in the rain, neon signs reflected on wet asphalt, people with umbrellas, cinematic",
    "A glass greenhouse full of ferns in morning fog, sunbeams through dusty panes, medium format photograph",
]

DEFAULTS = {
    "krea2_turbo": dict(unet="krea2_turbo_nvfp4.safetensors", clip="qwen3vl_4b_fp8_scaled.safetensors",
                        vae="qwen_image_vae.safetensors", width=1024, height=1024, steps=8),
    "h3_image": dict(unet="minimax_h3_image_pruned_w4a8.safetensors",
                     clip="qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors",
                     vae="minimax_h3_image_vae_fp16.safetensors", width=2048, height=2048, steps=6),
    "h3_t2v": dict(unet="minimax_h3_fl2va_pruned_w6a8.safetensors",
                   clip="qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors",
                   vae="minimax_h3_video_vae_int8_convrot.safetensors",
                   audio_vae="minimax_h3_audio_vae_fp32.safetensors",
                   lora="minimax_h3_fl2v_turbo_8step_v1.0_comfyui_bf16.safetensors",
                   width=832, height=480, length=56, steps=6, fps=24),
}


def ref(node, slot=0):
    return [str(node), slot]


def krea2_turbo(p, prompt, seed):
    return {
        "1": {"class_type": "UNETLoader", "inputs": {"unet_name": p["unet"], "weight_dtype": "default"}},
        "2": {"class_type": "CLIPLoader", "inputs": {"clip_name": p["clip"], "type": "krea2", "device": "default"}},
        "3": {"class_type": "VAELoader", "inputs": {"vae_name": p["vae"]}},
        "4": {"class_type": "CLIPTextEncode", "inputs": {"text": prompt, "clip": ref(2)}},
        "5": {"class_type": "ConditioningZeroOut", "inputs": {"conditioning": ref(4)}},
        "6": {"class_type": "EmptyLatentImage", "inputs": {"width": p["width"], "height": p["height"], "batch_size": 1}},
        "7": {"class_type": "KSampler", "inputs": {
            "model": ref(1), "positive": ref(4), "negative": ref(5), "latent_image": ref(6), "seed": seed,
            "steps": p["steps"], "cfg": 1, "sampler_name": "euler", "scheduler": "simple", "denoise": 1}},
        "8": {"class_type": "VAEDecode", "inputs": {"samples": ref(7), "vae": ref(3)}},
        "9": {"class_type": "SaveImage", "inputs": {"images": ref(8), "filename_prefix": "bench/krea2_turbo"}},
    }


def h3_image(p, prompt, seed):
    return {
        "1": {"class_type": "UNETLoader", "inputs": {"unet_name": p["unet"], "weight_dtype": "default"}},
        "2": {"class_type": "CLIPLoader", "inputs": {"clip_name": p["clip"], "type": "minimax", "device": "default"}},
        "3": {"class_type": "VAELoader", "inputs": {"vae_name": p["vae"]}},
        "5": {"class_type": "MiniMaxH3ImageConditioning", "inputs": {
            "clip": ref(2), "vae": ref(3), "prompt": prompt, "width": p["width"], "height": p["height"]}},
        "6": {"class_type": "ConditioningZeroOut", "inputs": {"conditioning": ref(5)}},
        "7": {"class_type": "KSampler", "inputs": {
            "model": ref(1), "positive": ref(5), "negative": ref(6), "latent_image": ref(5, 1), "seed": seed,
            "steps": p["steps"], "cfg": 1, "sampler_name": "euler", "scheduler": "simple", "denoise": 1}},
        "8": {"class_type": "VAEDecode", "inputs": {"samples": ref(7), "vae": ref(3)}},
        "9": {"class_type": "SaveImage", "inputs": {"images": ref(8), "filename_prefix": "bench/h3_image"}},
    }


def h3_t2v(p, prompt, seed):
    g = {
        "1": {"class_type": "UNETLoader", "inputs": {"unet_name": p["unet"], "weight_dtype": "default"}},
        "2": {"class_type": "CLIPLoader", "inputs": {"clip_name": p["clip"], "type": "minimax", "device": "default"}},
        "3": {"class_type": "VAELoader", "inputs": {"vae_name": p["vae"]}},
        "4": {"class_type": "VAELoader", "inputs": {"vae_name": p["audio_vae"]}},
        "5": {"class_type": "MiniMaxH3ImageToVideo", "inputs": {
            "clip": ref(2), "vae": ref(3), "prompt": prompt,
            "width": p["width"], "height": p["height"], "length": p["length"]}},
        "7": {"class_type": "BasicGuider", "inputs": {"model": ref(1), "conditioning": ref(5)}},
        "8": {"class_type": "BasicScheduler", "inputs": {
            "model": ref(1), "scheduler": "simple", "steps": p["steps"], "denoise": 1}},
        "9": {"class_type": "KSamplerSelect", "inputs": {"sampler_name": "res_multistep"}},
        "10": {"class_type": "RandomNoise", "inputs": {"noise_seed": seed}},
        "11": {"class_type": "SamplerCustomAdvanced", "inputs": {
            "noise": ref(10), "guider": ref(7), "sampler": ref(9), "sigmas": ref(8), "latent_image": ref(5, 1)}},
        "12": {"class_type": "VAEDecode", "inputs": {"samples": ref(11), "vae": ref(3)}},
        "13": {"class_type": "VAEDecodeAudio", "inputs": {"samples": ref(11), "vae": ref(4)}},
        "14": {"class_type": "CreateVideo", "inputs": {"images": ref(12), "audio": ref(13), "fps": p["fps"]}},
        "15": {"class_type": "SaveVideo", "inputs": {
            "video": ref(14), "filename_prefix": "bench/h3_t2v", "format": "auto", "codec": "auto"}},
    }
    if p.get("lora"):
        g["6"] = {"class_type": "LoraLoaderModelOnly", "inputs": {
            "model": ref(1), "lora_name": p["lora"], "strength_model": 1}}
        g["7"]["inputs"]["model"] = ref(6)
        g["8"]["inputs"]["model"] = ref(6)
    return g


CASES = {"krea2_turbo": krea2_turbo, "h3_image": h3_image, "h3_t2v": h3_t2v}
LOADERS = {"UNETLoader", "CLIPLoader", "VAELoader", "LoraLoaderModelOnly"}
ENCODERS = {"CLIPTextEncode", "MiniMaxH3ImageConditioning", "MiniMaxH3ImageToVideo", "ConditioningZeroOut"}
SAMPLERS = {"KSampler", "SamplerCustomAdvanced"}
DECODERS = {"VAEDecode", "VAEDecodeAudio", "CreateVideo", "SaveImage", "SaveVideo"}


async def vram_poller(session, url, peak, stop):
    while not stop.is_set():
        try:
            async with session.get(f"{url}/system_stats") as r:
                d = (await r.json())["devices"][0]
                peak["gpu"] = max(peak["gpu"], d["vram_total"] - d["vram_free"])
        except Exception:
            pass
        try:
            await asyncio.wait_for(stop.wait(), 0.5)
        except asyncio.TimeoutError:
            pass


async def run_one(session, url, graph, timeout):
    cid = uuid.uuid4().hex
    peak, stop = {"gpu": 0}, asyncio.Event()
    poll = asyncio.create_task(vram_poller(session, url, peak, stop))
    node_t, steps, cur, cur_t0, err = {}, [], None, None, None
    try:
        async with session.ws_connect(f"{url}/ws?clientId={cid}", max_msg_size=0, heartbeat=30) as ws:
            t0 = time.monotonic()
            async with session.post(f"{url}/prompt", json={"prompt": graph, "client_id": cid}) as r:
                body = await r.json()
                if r.status != 200:
                    return {"error": json.dumps(body)[:600]}
                pid = body["prompt_id"]
            while True:
                msg = await asyncio.wait_for(ws.receive(), timeout)
                if msg.type in (aiohttp.WSMsgType.CLOSED, aiohttp.WSMsgType.ERROR):
                    err = "websocket closed (server died? OOM?)"
                    break
                if msg.type != aiohttp.WSMsgType.TEXT:
                    continue
                m = json.loads(msg.data)
                d, now = m.get("data", {}), time.monotonic()
                if d.get("prompt_id") not in (None, pid):
                    continue
                if m["type"] == "executing":
                    if cur is not None:
                        cls = graph[cur]["class_type"]
                        node_t[cls] = node_t.get(cls, 0) + now - cur_t0
                    cur, cur_t0 = d.get("node"), now
                    if cur is None:
                        break
                elif m["type"] == "progress" and cur and graph.get(cur, {}).get("class_type") in SAMPLERS:
                    steps.append((d["value"], now))
                elif m["type"] == "execution_error":
                    err = f'{d.get("node_type")}: {d.get("exception_message", "")[:400]}'
                    break
                elif m["type"] == "execution_success":
                    break
            total = time.monotonic() - t0
    finally:
        stop.set()
        await poll
    if err:
        return {"error": err, "total_s": round(time.monotonic() - t0, 2)}
    grp = lambda names: round(sum(v for k, v in node_t.items() if k in names), 2)
    its = [b[1] - a[1] for a, b in zip(steps, steps[1:])]
    return {
        "total_s": round(total, 2), "load_s": grp(LOADERS), "encode_s": grp(ENCODERS),
        "sample_s": grp(SAMPLERS), "decode_s": grp(DECODERS),
        "s_per_it": round(sum(its) / len(its), 3) if its else None, "steps": len(steps),
        "peak_vram_gb": round(peak["gpu"] / 2**30, 2),
    }


async def bench(a):
    over = {}
    for kv in a.set or []:
        k, v = kv.split("=", 1)
        case, key = k.split(".", 1)
        over.setdefault(case, {})[key] = int(v) if v.isdigit() else (None if v in ("", "none") else v)
    os.makedirs(a.out, exist_ok=True)
    out = os.path.join(a.out, f"{a.label}.jsonl")
    tmo = aiohttp.ClientTimeout(total=None, sock_connect=10)
    async with aiohttp.ClientSession(timeout=tmo) as s:
        async with s.get(f"{a.url}/system_stats") as r:
            st = await r.json()
        dev = st["devices"][0]
        host = {"gpu": dev["name"], "vram_gb": round(dev["vram_total"] / 2**30, 1),
                "ram_gb": round(st["system"]["ram_total"] / 2**30, 1),
                "comfyui": st["system"]["comfyui_version"], "torch": st["system"]["pytorch_version"]}
        print(f"# {a.label}: {host}")
        for name in a.cases.split(","):
            p = {**DEFAULTS[name], **over.get(name, {})}
            for i in range(1 + a.warm):
                kind = "cold" if i == 0 else "warm"
                if i == 0:
                    async with s.post(f"{a.url}/free", json={"unload_models": True, "free_memory": True}):
                        pass
                    await asyncio.sleep(3)
                async with s.get(f"{a.url}/system_stats") as r:
                    d = (await r.json())["devices"][0]
                # What the card already holds before this run. After /free
                # (cold) that is everything that is not ComfyUI — a desktop
                # compositor, a game; ~0 on a headless node.
                base = round((d["vram_total"] - d["vram_free"]) / 2**30, 2)
                res = await run_one(s, a.url, CASES[name](p, PROMPTS[i % len(PROMPTS)], 1000 + i), a.timeout)
                rec = {"label": a.label, "case": name, "run": kind, "ts": int(time.time()), **host,
                       "params": p, "vram_before_gb": base, **res}
                with open(out, "a") as f:
                    f.write(json.dumps(rec) + "\n")
                print(f"{name:12} {kind:4} " + " ".join(f"{k}={v}" for k, v in res.items()), flush=True)
                if "error" in res:
                    break


def table(a):
    rows = []
    for f in sorted(glob.glob(os.path.join(a.out, "*.jsonl"))):
        rows += [json.loads(l) for l in open(f) if l.strip()]
    cols = ["label", "case", "unet", "run", "n", "total_s", "load_s", "encode_s", "sample_s", "s_per_it",
            "decode_s", "peak_vram_gb", "vram_before_gb"]
    agg = {}
    for r in rows:
        if "error" in r:
            agg[(r["label"], r["case"], r["params"]["unet"], "FAILED")] = [r]
            continue
        agg.setdefault((r["label"], r["case"], r["params"]["unet"], r["run"]), []).append(r)
    print("| " + " | ".join(cols) + " |\n|" + "---|" * len(cols))
    for (label, case, unet, run), rs in sorted(agg.items()):
        if run == "FAILED":
            print(f"| {label} | {case} | {unet} | FAILED | | {rs[-1]['error'][:90]} |")
            continue
        med = lambda k: sorted(x[k] for x in rs if x.get(k) is not None)[len(rs) // 2] if any(
            x.get(k) is not None for x in rs) else ""
        print("| " + " | ".join(str(x) for x in [
            label, case, unet.replace(".safetensors", ""), run, len(rs)] + [med(c) for c in cols[5:]]) + " |")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://127.0.0.1:8188")
    ap.add_argument("--label")
    ap.add_argument("--cases", default=",".join(CASES))
    ap.add_argument("--warm", type=int, default=2)
    ap.add_argument("--set", action="append")
    ap.add_argument("--timeout", type=int, default=3600, help="seconds without any server event")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "bench-results"))
    ap.add_argument("--table", action="store_true")
    a = ap.parse_args()
    if a.table:
        table(a)
    else:
        if not a.label:
            ap.error("--label is required")
        asyncio.run(bench(a))
