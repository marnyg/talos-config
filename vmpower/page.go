package main

import (
	"bytes"
	"html/template"
	"log"
)

// pageHTML is the whole front end: no assets, no build step, one fetch
// loop. It is served behind the mesh gate, so there is no login of its
// own — the device's member cert is the identity (ADR-0032), exactly as
// for every other *.gw.mesh.internal page.
var pageHTML = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.VM}} power</title>
<style>
  :root { color-scheme: dark }
  * { box-sizing: border-box }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    background: #0b1017; color: #d6e0ea;
    font: 15px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
  }
  main { width: min(30rem, 92vw) }
  h1 { margin: 0 0 .25rem; font-size: 1.4rem; letter-spacing: .01em }
  .sub { margin: 0 0 1.5rem; color: #7d8b9c; font-size: .85rem }
  .status {
    display: flex; align-items: baseline; gap: .6rem;
    padding: 1rem 1.1rem; border-radius: .7rem;
    background: #131a23; border: 1px solid #1b2430;
  }
  .dot { width: .6rem; height: .6rem; border-radius: 50%; background: #6b7787; flex: none }
  .running .dot { background: #3fb950 }
  .halted .dot { background: #6b7787 }
  .busy .dot { background: #d29922; animation: pulse 1s ease-in-out infinite }
  @keyframes pulse { 50% { opacity: .25 } }
  .label { font-size: 1.15rem; font-weight: 600 }
  .note { margin-left: auto; color: #7d8b9c; font-size: .8rem }
  .row { display: flex; gap: .6rem; margin-top: 1rem }
  button {
    flex: 1; padding: .7rem 1rem; border-radius: .6rem; cursor: pointer;
    border: 1px solid #26313f; background: #1b2430; color: #d6e0ea;
    font: inherit; font-weight: 600;
  }
  button:hover:not(:disabled) { background: #24303f }
  button:disabled { opacity: .4; cursor: default }
  #stop { border-color: #4a2b2b }
  #stop:hover:not(:disabled) { background: #38201f }
  .err { margin-top: 1rem; padding: .7rem .9rem; border-radius: .6rem;
         background: #2a1717; border: 1px solid #4a2b2b; color: #ffb4a8; font-size: .85rem }
  .foot { margin-top: 1.6rem; color: #7d8b9c; font-size: .8rem }
  a { color: #6ea8fe }
</style>
<main>
  <h1>{{.VM}}</h1>
  <p class="sub">KubeVirt guest · 24 GiB + the passed-through GPU while it runs</p>

  <div class="status" id="status">
    <span class="dot"></span>
    <span class="label" id="label">…</span>
    <span class="note" id="note"></span>
  </div>

  <div class="row">
    <button id="start">Start</button>
    <button id="stop">Stop</button>
  </div>

  <div class="err" id="err" hidden></div>

  <p class="foot">
    <a href="{{.RDP}}">open the RDP session</a> ·
    stopping is graceful (the guest agent gets ~30&nbsp;s to flush; 24 GiB takes it) ·
    the GPU returns to the cluster while halted.
  </p>
</main>
<script>
const el = id => document.getElementById(id);
const STATUS_NOTE = {Starting: "booting", Stopping: "shutting down", Provisioning: "provisioning"};

function render(s) {
  const box = el("status"), err = el("err");
  if (s.error) { err.hidden = false; err.textContent = s.error; }
  else { err.hidden = true; }
  box.className = "status " + (s.busy ? "busy" : s.running ? "running" : "halted");
  el("label").textContent = s.status || "Unknown";
  el("note").textContent = STATUS_NOTE[s.status] || (s.running ? "in use" : "parked");
  el("start").disabled = s.busy || s.running;
  el("stop").disabled = s.busy || !s.running;
}

async function refresh() {
  try {
    const r = await fetch("state", {cache: "no-store"});
    render(await r.json());
  } catch (e) {
    el("err").hidden = false;
    el("err").textContent = "cannot reach the cluster: " + e;
  }
}

async function act(which) {
  // One mis-tap on a phone must not end a 24 GiB session.
  if (which === "stop" && !confirm("Stop {{.VM}}? Open sessions end and the guest shuts down.")) return;
  el("start").disabled = el("stop").disabled = true;
  try {
    // The header is the CSRF guard (main.go sameSite): a cross-site
    // fetch cannot send it without a preflight nothing answers.
    const r = await fetch(which, {method: "POST", headers: {"X-Vmpower": "1"}});
    render(await r.json());
  } catch (e) {
    el("err").hidden = false;
    el("err").textContent = which + " failed: " + e;
  }
}

el("start").onclick = () => act("start");
el("stop").onclick = () => act("stop");
refresh();
setInterval(refresh, 3000);
</script>
</html>
`))

type pageData struct {
	VM  string
	RDP string
}

func page(vmName, rdp string) string {
	var b bytes.Buffer
	if err := pageHTML.Execute(&b, pageData{VM: vmName, RDP: rdp}); err != nil {
		log.Printf("render page: %v", err)
		return "<!doctype html><title>vmpower</title><p>render error"
	}
	return b.String()
}
