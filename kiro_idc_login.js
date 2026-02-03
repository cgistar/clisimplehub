/**
 * worker.js — Kiro Manual Auth (Device Flow) Web Tool
 * Full UI (no cuts): Vercel/shadcn/Inspira-ish + Acrylic hover actions
 * https://kiro.endpoint.cc.cd/
 *
 * ✅ Flow:
 * Login -> get CID/CS -> get Device Flow link (hover: Copy | Open Link) -> background polling
 * -> success modal pops with Tabs: Viewer / JSON
 *
 * ✅ Viewer tab:
 * - Shows CID / CS + ALL keys from last successful /api/poll JSON
 * - Each row hover shows acrylic blur + "Copy" (same logic as URL hover, no OpenLink)
 *
 * ✅ Poll logic:
 * - No overlapping polls: next poll only after previous finished
 * - If backend returns 400 {"error":"authorization_pending"...}, treat as Pending (yellow) not error
 *
 * ✅ NEW (your request):
 * - Modal content is scrollable (so long tokens won't squeeze/overflow the screen)
 * - Each KV value area is also scrollable (does not cut value; copy still copies full value)
 */

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url)
    const path = url.pathname

    // ===== Config =====
    const OIDC = 'https://oidc.us-east-1.amazonaws.com'
    const PORTAL = 'https://view.awsapps.com'
    const START_URL = `${PORTAL}/start`

    // ===== CORS =====
    const corsHeaders = {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET,POST,OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type',
    }

    if (request.method === 'OPTIONS') {
      return new Response(null, { status: 204, headers: corsHeaders })
    }

    // ===== UI =====
    if (request.method === 'GET' && path === '/') {
      return new Response(renderHTML(), {
        headers: {
          'content-type': 'text/html; charset=utf-8',
          'cache-control': 'no-store',
        },
      })
    }

    // ===== Helpers =====
    const json = (obj, status = 200, extraHeaders = {}) =>
      new Response(JSON.stringify(obj), {
        status,
        headers: {
          'content-type': 'application/json; charset=utf-8',
          'cache-control': 'no-store',
          ...corsHeaders,
          ...extraHeaders,
        },
      })

    const proxyJson = async (targetUrl, bodyObj) => {
      const r = await fetch(targetUrl, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(bodyObj),
      })
      const text = await r.text()
      return new Response(text, {
        status: r.status,
        headers: {
          'content-type': 'application/json; charset=utf-8',
          'cache-control': 'no-store',
          ...corsHeaders,
        },
      })
    }

    // ===== API: register =====
    if (request.method === 'POST' && path === '/api/register') {
      return proxyJson(`${OIDC}/client/register`, {
        clientName: 'Amazon Q Developer for command line',
        clientType: 'public',
        scopes: ['codewhisperer:completions', 'codewhisperer:analysis', 'codewhisperer:conversations'],
      })
    }

    // ===== API: device authorization =====
    if (request.method === 'POST' && path === '/api/device') {
      let req
      try {
        req = await request.json()
      } catch {
        req = {}
      }
      const { clientId, clientSecret } = req || {}
      if (!clientId || !clientSecret) {
        return json({ error: 'missing clientId/clientSecret' }, 400)
      }
      return proxyJson(`${OIDC}/device_authorization`, {
        clientId,
        clientSecret,
        startUrl: START_URL,
      })
    }

    // ===== API: poll token (device_code) =====
    // UI handles 400 authorization_pending as "Pending"
    if (request.method === 'POST' && path === '/api/poll') {
      let req
      try {
        req = await request.json()
      } catch {
        req = {}
      }
      const { clientId, clientSecret, deviceCode } = req || {}
      if (!clientId || !clientSecret || !deviceCode) {
        return json({ error: 'missing clientId/clientSecret/deviceCode' }, 400)
      }
      return proxyJson(`${OIDC}/token`, {
        clientId,
        clientSecret,
        deviceCode,
        grantType: 'urn:ietf:params:oauth:grant-type:device_code',
      })
    }

    // ===== API: refresh (optional) =====
    if (request.method === 'POST' && path === '/api/refresh') {
      let req
      try {
        req = await request.json()
      } catch {
        req = {}
      }
      const { clientId, clientSecret, refreshToken } = req || {}
      if (!clientId || !clientSecret || !refreshToken) {
        return json({ error: 'missing clientId/clientSecret/refreshToken' }, 400)
      }
      return proxyJson(`${OIDC}/token`, {
        clientId,
        clientSecret,
        refreshToken,
        grantType: 'refresh_token',
      })
    }

    return new Response('Not Found', { status: 404 })
  },
}

function renderHTML() {
  const AWS_SVG = `<svg fill="currentColor" fill-rule="evenodd" height="18" viewBox="0 0 24 24" width="18" xmlns="http://www.w3.org/2000/svg" style="flex: 0 0 auto; line-height: 1;"><title>AWS</title><path d="M6.763 11.212c0 .296.032.535.088.71.064.176.144.368.256.576.04.063.056.127.056.183 0 .08-.048.16-.152.24l-.503.335a.383.383 0 01-.208.072c-.08 0-.16-.04-.239-.112a2.47 2.47 0 01-.287-.375 6.18 6.18 0 01-.248-.471c-.622.734-1.405 1.101-2.347 1.101-.67 0-1.205-.191-1.596-.574-.39-.384-.59-.894-.59-1.533 0-.678.24-1.23.726-1.644.487-.415 1.133-.623 1.955-.623.272 0 .551.024.846.064.296.04.6.104.918.176v-.583c0-.607-.127-1.03-.375-1.277-.255-.248-.686-.367-1.3-.367-.28 0-.568.031-.863.103-.295.072-.583.16-.862.272a2.4 2.4 0 01-.28.104.488.488 0 01-.127.023c-.112 0-.168-.08-.168-.247v-.391c0-.128.016-.224.056-.28a.597.597 0 01.224-.167 4.577 4.577 0 011.005-.36 4.84 4.84 0 011.246-.151c.95 0 1.644.216 2.091.647.44.43.662 1.085.662 1.963v2.586h.016zm-3.24 1.214c.263 0 .534-.048.822-.144a1.78 1.78 0 00.758-.51 1.27 1.27 0 00.272-.512c.047-.191.08-.423.08-.694v-.335a6.66 6.66 0 00-.735-.136 6.02 6.02 0 00-.75-.048c-.535 0-.926.104-1.19.32-.263.215-.39.518-.39.917 0 .375.095.655.295.846.191.2.47.296.838.296zm6.41.862c-.144 0-.24-.024-.304-.08-.064-.048-.12-.16-.168-.311L7.586 6.726a1.398 1.398 0 01-.072-.32c0-.128.064-.2.191-.2h.783c.151 0 .255.025.31.08.065.048.113.16.16.312l1.342 5.284 1.245-5.284c.04-.16.088-.264.151-.312a.549.549 0 01.32-.08h.638c.152 0 .256.025.32.08.063.048.12.16.151.312l1.261 5.348 1.381-5.348c.048-.16.104-.264.16-.312a.52.52 0 01.311-.08h.743c.127 0 .2.065.2.2 0 .04-.009.08-.017.128a1.137 1.137 0 01-.056.2l-1.923 6.17c-.048.16-.104.263-.168.311a.51.51 0 01-.303.08h-.687c-.15 0-.255-.024-.32-.08-.063-.056-.119-.16-.15-.32L12.32 7.747l-1.23 5.14c-.04.16-.087.264-.15.32-.065.056-.177.08-.32.08l-.686.001zm10.256.215c-.415 0-.83-.048-1.229-.143-.399-.096-.71-.2-.918-.32-.128-.071-.215-.151-.247-.223a.563.563 0 01-.048-.224v-.407c0-.167.064-.247.183-.247.048 0 .096.008.144.024.048.016.12.048.2.08.271.12.566.215.878.279.32.064.63.096.95.096.502 0 .894-.088 1.165-.264a.86.86 0 00.415-.758.777.777 0 00-.215-.559c-.144-.151-.416-.287-.807-.415l-1.157-.36c-.583-.183-1.014-.454-1.277-.813a1.902 1.902 0 01-.4-1.158c0-.335.073-.63.216-.886.144-.255.335-.479.575-.654.24-.184.51-.32.83-.415.32-.096.655-.136 1.006-.136.175 0 .36.008.535.032.183.024.35.056.518.088.16.04.312.08.455.127.144.048.256.096.336.144a.69.69 0 01.24.2.43.43 0 01.071.263v.375c0 .168-.064.256-.184.256a.83.83 0 01-.303-.096 3.652 3.652 0 00-1.532-.311c-.455 0-.815.071-1.062.223-.248.152-.375.383-.375.71 0 .224.08.416.24.567.16.152.454.304.877.44l1.134.358c.574.184.99.44 1.237.767.247.327.367.702.367 1.117 0 .343-.072.655-.207.926a2.157 2.157 0 01-.583.703c-.248.2-.543.343-.886.447-.36.111-.734.167-1.142.167z"></path><path d="M.378 15.475c3.384 1.963 7.56 3.153 11.877 3.153 2.914 0 6.114-.607 9.06-1.852.44-.2.814.287.383.607-2.626 1.94-6.442 2.969-9.722 2.969-4.598 0-8.74-1.7-11.87-4.526-.247-.223-.024-.527.272-.351zm23.531-.2c.287.36-.08 2.826-1.485 4.007-.215.184-.423.088-.327-.151l.175-.439c.343-.88.802-2.198.52-2.555-.336-.43-2.22-.207-3.074-.103-.255.032-.295-.192-.063-.36 1.5-1.053 3.967-.75 4.254-.399z" fill="#F90"></path></svg>`

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>Kiro Manual Auth (Worker)</title>
<style>
  :root{
    --bg: 10 10 12;
    --card: 18 18 22;
    --muted: 160 160 175;
    --text: 240 240 245;
    --border: 255 255 255;
    --shadow: 0 10px 30px rgba(0,0,0,.35);
    --ring: 99 102 241;
    --ok: 16 185 129;
    --bad: 239 68 68;
    --warn: 245 158 11;
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{
    margin:0;
    font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Helvetica, Arial;
    color: rgb(var(--text));
    background:
      radial-gradient(1200px 600px at 20% 10%, rgba(99,102,241,.22), transparent 60%),
      radial-gradient(900px 520px at 80% 30%, rgba(16,185,129,.18), transparent 55%),
      radial-gradient(900px 520px at 40% 90%, rgba(236,72,153,.14), transparent 55%),
      linear-gradient(180deg, rgba(0,0,0,0) 0%, rgba(0,0,0,.35) 55%, rgba(0,0,0,.75) 100%),
      rgb(var(--bg));
    overflow-x:hidden;
  }

  .container{max-width:980px;margin:0 auto;padding:40px 16px 60px}
  .header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:16px}
  .title{font-size:22px;font-weight:650;letter-spacing:-.02em;margin:0}
  .subtitle{margin:8px 0 0;color:rgba(var(--muted),.9);font-size:13px;line-height:1.5}

  .card{
    border:1px solid rgba(var(--border),.10);
    background: rgba(var(--card), .55);
    backdrop-filter: blur(16px);
    -webkit-backdrop-filter: blur(16px);
    border-radius: 18px;
    box-shadow: var(--shadow);
    overflow:hidden;
    position:relative;
  }
  .card-inner{padding:18px}
  .row{display:flex;gap:12px;flex-wrap:wrap;align-items:center;justify-content:space-between}
  .muted{color:rgba(var(--muted),.9);font-size:12px;line-height:1.5}
  .sep{height:1px;background:rgba(var(--border),.10);margin:16px 0}

  .btn{
    display:inline-flex;align-items:center;gap:10px;
    padding:10px 14px;
    border-radius:14px;
    border:1px solid rgba(var(--border),.14);
    background: rgba(255,255,255,.06);
    color: rgb(var(--text));
    cursor:pointer;
    font-size:13px;font-weight:600;
    transition: transform .12s ease, background .12s ease, border-color .12s ease, box-shadow .12s ease;
    user-select:none;
  }
  .btn:hover{background: rgba(255,255,255,.09);border-color: rgba(var(--border),.20);transform: translateY(-1px)}
  .btn:active{transform: translateY(0px)}
  .btn:focus{outline:none;box-shadow: 0 0 0 4px rgba(var(--ring), .25)}
  .btn[disabled]{opacity:.55;cursor:not-allowed;transform:none}
  .btn-primary{background: rgba(255,255,255,.10);border-color: rgba(255,255,255,.16)}
  .btn-ghost{background: transparent;border-color: rgba(255,255,255,.10)}
  .btn-secondary{background: rgba(255,255,255,.07)}
  .btn-xs{padding:7px 10px;border-radius:12px;font-size:12px}

  .grid{display:grid;grid-template-columns:1fr;gap:12px}
  @media (min-width: 860px){ .grid{grid-template-columns:1fr 1fr} }

  .field{
    border:1px solid rgba(var(--border),.10);
    background: rgba(0,0,0,.22);
    border-radius: 16px;
    padding:12px 14px;
  }
  .label{font-size:11px;color:rgba(var(--muted),.9);margin-bottom:6px}
  .value{font-size:13px;word-break:break-all}
  .mono{font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace; font-size:12px}

  .link-panel{
    position:relative;
    border:1px solid rgba(var(--border),.10);
    background: rgba(0,0,0,.20);
    border-radius: 18px;
    padding:14px;
    overflow:hidden;
    transition: box-shadow .16s ease, border-color .16s ease, transform .16s ease;
  }
  .link-panel:hover{box-shadow: 0 12px 30px rgba(0,0,0,.35);border-color: rgba(255,255,255,.16);transform: translateY(-1px)}
  .link-text{margin-top:8px;font-size:13px;opacity:.92;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}

  /* Acrylic hover overlay */
  .acrylic{position:absolute;inset:0;opacity:0;transition: opacity .16s ease;pointer-events:none;}
  .link-panel:hover .acrylic{opacity:1}
  .acrylic::before{
    content:"";position:absolute;inset:0;
    background: rgba(255,255,255,.10);
    backdrop-filter: blur(18px);
    -webkit-backdrop-filter: blur(18px);
  }
  .acrylic::after{
    content:"";position:absolute;inset:-40px;
    background: radial-gradient(420px 220px at 20% 20%, rgba(255,255,255,.12), transparent 55%),
                radial-gradient(420px 220px at 80% 40%, rgba(255,255,255,.08), transparent 55%);
    opacity:.9;mix-blend-mode: overlay;
  }
  .link-actions{
    position:absolute; inset:0;
    display:flex;align-items:center;justify-content:center;gap:10px;
    opacity:0;transition: opacity .16s ease;
    pointer-events:none;
  }
  .link-panel:hover .link-actions{opacity:1}
  .link-actions .btn{pointer-events:auto}

  .status{
    border:1px solid rgba(var(--border),.10);
    background: rgba(0,0,0,.20);
    border-radius: 16px;
    padding:12px 14px;
    display:flex;gap:12px;align-items:flex-start;justify-content:space-between;
  }
  .badge{
    display:inline-flex;align-items:center;gap:8px;
    font-size:11px;color:rgba(var(--muted),.95);
    padding:6px 10px;border-radius:999px;
    border:1px solid rgba(var(--border),.12);
    background: rgba(255,255,255,.05);
    white-space:nowrap;
  }
  .dot{width:8px;height:8px;border-radius:999px;background: rgba(255,255,255,.55);position:relative;}
  .dot.ping::after{
    content:"";position:absolute;inset:-6px;border-radius:999px;
    border:1px solid rgba(255,255,255,.35);
    animation: ping 1.2s ease-out infinite;opacity:.8;
  }
  @keyframes ping{0%{transform:scale(.4);opacity:.8} 100%{transform:scale(1.5);opacity:0}}

  .status.ok{border-color: rgba(var(--ok), .35); background: rgba(var(--ok), .10)}
  .status.bad{border-color: rgba(var(--bad), .35); background: rgba(var(--bad), .10)}
  .status.warn{border-color: rgba(var(--warn), .35); background: rgba(var(--warn), .08)}

  /* Modal */
  .modal-backdrop{
    position:fixed;inset:0;
    background: rgba(0,0,0,.60);
    display:none;
    align-items:center;justify-content:center;
    padding:18px;
    z-index:50;
  }
  .modal-backdrop.show{display:flex}
  .modal{
    width:min(900px, 100%);
    max-height: 88vh; /* NEW: keep modal within viewport */
    border-radius: 18px;
    border:1px solid rgba(var(--border),.14);
    background: rgba(var(--card), .86);
    backdrop-filter: blur(18px);
    -webkit-backdrop-filter: blur(18px);
    box-shadow: var(--shadow);
    overflow:hidden;
    display:flex;
    flex-direction:column; /* NEW: allow internal scroll areas */
  }
  .modal-head{padding:16px 18px;border-bottom:1px solid rgba(var(--border),.10);flex:0 0 auto}
  .modal-title{margin:0;font-size:16px;font-weight:750}
  .modal-desc{margin:6px 0 0;color:rgba(var(--muted),.9);font-size:12px;line-height:1.45}

  .modal-body{
    padding:16px 18px;
    flex: 1 1 auto;          /* NEW */
    min-height: 0;           /* NEW: critical for flex scroll children */
    display:flex;            /* NEW */
    flex-direction:column;   /* NEW */
    gap:12px;                /* NEW */
  }

  /* NEW: scrollable content area inside modal body */
  .modal-scroll{
    flex: 1 1 auto;
    min-height: 0;
    overflow:auto;
    padding-right: 4px;
    border-radius: 14px;
  }
  /* nicer scrollbar (webkit only) */
  .modal-scroll::-webkit-scrollbar{width:10px}
  .modal-scroll::-webkit-scrollbar-thumb{background: rgba(255,255,255,.12); border-radius: 999px; border:2px solid rgba(0,0,0,.15)}
  .modal-scroll::-webkit-scrollbar-track{background: rgba(0,0,0,.10); border-radius: 999px}

  pre{
    margin:0;border-radius: 14px;border:1px solid rgba(var(--border),.10);
    background: rgba(0,0,0,.30);
    padding:14px;
    overflow:auto;
    font-size:12px; line-height:1.5;
  }

  /* Footer pinned inside modal */
  .footer-actions{
    display:flex;gap:10px;flex-wrap:wrap;
    padding-top: 12px;
    margin-top: 0;
    position: sticky;     /* NEW */
    bottom: 0;            /* NEW */
    background: linear-gradient(to bottom, rgba(0,0,0,0), rgba(0,0,0,.20) 30%, rgba(0,0,0,.28));
    backdrop-filter: blur(10px);
    -webkit-backdrop-filter: blur(10px);
    border-top: 1px solid rgba(255,255,255,.08);
  }
  .right{margin-left:auto}

  /* Tabs */
  .tabs{display:flex;gap:8px;align-items:center}
  .tab{
    padding:8px 10px;
    border-radius: 12px;
    border:1px solid rgba(var(--border),.12);
    background: rgba(255,255,255,.05);
    color: rgba(var(--text), .88);
    font-size:12px;font-weight:650;
    cursor:pointer;
    transition: background .12s ease, border-color .12s ease, transform .12s ease;
    user-select:none;
  }
  .tab:hover{background: rgba(255,255,255,.08);border-color: rgba(255,255,255,.18);transform: translateY(-1px)}
  .tab.active{
    background: rgba(255,255,255,.12);
    border-color: rgba(255,255,255,.22);
    color: rgb(var(--text));
  }

  /* Viewer list */
  .viewer{display:flex;flex-direction:column;gap:10px;}
  .kv-row{
    position:relative;
    border:1px solid rgba(var(--border),.10);
    background: rgba(0,0,0,.22);
    border-radius: 16px;
    padding:12px 14px;
    overflow:hidden;
  }
  .kv-row:hover{border-color: rgba(255,255,255,.16)}
  .kv-key{font-size:11px;color:rgba(var(--muted),.92)}
  .kv-val{
    margin-top:6px;
    font-size:12px;
    word-break: break-all;
    opacity:.92;

    /* NEW: prevent a single token from taking the entire screen */
    max-height: 140px;
    overflow:auto;
    padding-right: 6px;
  }
  .kv-val::-webkit-scrollbar{width:10px}
  .kv-val::-webkit-scrollbar-thumb{background: rgba(255,255,255,.10); border-radius: 999px; border:2px solid rgba(0,0,0,.15)}
  .kv-val::-webkit-scrollbar-track{background: rgba(0,0,0,.10); border-radius: 999px}

  .kv-actions{
    position:absolute;inset:0;
    display:flex;align-items:center;justify-content:center;
    opacity:0;transition: opacity .16s ease;
    pointer-events:none;
  }
  .kv-row:hover .kv-actions{opacity:1}
  .kv-actions .btn{pointer-events:auto}

  .kv-acrylic{position:absolute;inset:0;opacity:0;transition: opacity .16s ease;pointer-events:none;}
  .kv-row:hover .kv-acrylic{opacity:1}
  .kv-acrylic::before{
    content:"";position:absolute;inset:0;
    background: rgba(255,255,255,.10);
    backdrop-filter: blur(18px);
    -webkit-backdrop-filter: blur(18px);
  }
  .kv-acrylic::after{
    content:"";position:absolute;inset:-40px;
    background: radial-gradient(380px 200px at 25% 30%, rgba(255,255,255,.12), transparent 58%),
                radial-gradient(380px 200px at 75% 45%, rgba(255,255,255,.08), transparent 60%);
    opacity:.9;mix-blend-mode: overlay;
  }

  .small-note{margin-top:12px;color:rgba(var(--muted),.85);font-size:12px;line-height:1.5}
  .glow{
    position:absolute; inset:-200px;
    background: radial-gradient(600px 260px at 20% 10%, rgba(99,102,241,.20), transparent 55%),
                radial-gradient(500px 240px at 80% 20%, rgba(16,185,129,.14), transparent 60%);
    pointer-events:none;
    opacity:.9;
  }
</style>
</head>

<body>
  <div class="container">
    <div class="header">
      <div>
        <h1 class="title">Kiro Manual Auth</h1>
        <p class="subtitle">
          Click <b>Login</b> → get CID/CS → device flow link. Hover link for <b>Copy | Open Link</b>.
          We poll automatically and pop credentials when authorized.
        </p>
      </div>
      <button id="btnLogin" class="btn btn-primary">
        ${AWS_SVG}
        <span>Login</span>
      </button>
    </div>

    <div class="card">
      <div class="glow"></div>
      <div class="card-inner">
        <div class="row" style="gap:10px">
          <div class="muted">
            OIDC requests are proxied server-side to bypass browser CORS.
            <span style="display:block;opacity:.85">Treat Refresh Token / Client Secret as secrets.</span>
          </div>
          <div class="row" style="justify-content:flex-end">
            <button id="btnClear" class="btn btn-ghost">Clear</button>
          </div>
        </div>

        <div class="sep"></div>

        <div class="link-panel" id="linkPanel">
          <div class="label">Device Flow Link</div>
          <div class="link-text" id="verifyUrl" title="">—</div>

          <div class="acrylic"></div>
          <div class="link-actions" id="linkActions">
            <button id="btnCopyLink" class="btn btn-secondary">Copy</button>
            <button id="btnOpenLink" class="btn btn-secondary">Open Link</button>
          </div>
        </div>

        <div class="sep"></div>

        <div class="grid">
          <div class="field">
            <div class="label">User Code</div>
            <div class="value" id="userCode">—</div>
          </div>
          <div class="field">
            <div class="label">Device Code</div>
            <div class="value mono" id="deviceCode">—</div>
          </div>
        </div>

        <div class="sep"></div>

        <div class="status" id="statusBox">
          <div>
            <div style="font-weight:700;font-size:13px">Status</div>
            <div class="muted" id="statusText" style="margin-top:4px">Click Login to start.</div>
          </div>
          <div class="badge" id="statusBadge">
            <span class="dot" id="statusDot"></span>
            <span id="statusLabel">IDLE</span>
          </div>
        </div>

        <div class="small-note">
          Tip: hover the link panel to copy/open. Polling starts immediately after link generation.
        </div>
      </div>
    </div>
  </div>

  <!-- Modal -->
  <div class="modal-backdrop" id="modalBackdrop" role="dialog" aria-modal="true">
    <div class="modal">
      <div class="modal-head">
        <div class="row" style="align-items:flex-start">
          <div>
            <h2 class="modal-title">Authorized ✅</h2>
            <p class="modal-desc">Viewer: copy per key (CID/CS included). JSON: raw token.json.</p>
          </div>
          <div class="tabs" aria-label="result tabs">
            <div id="tabViewer" class="tab active">Viewer</div>
            <div id="tabJson" class="tab">JSON</div>
          </div>
        </div>
      </div>

      <div class="modal-body">
        <!-- NEW: scroll container -->
        <div class="modal-scroll" id="modalScroll">
          <div id="panelViewer" class="viewer"></div>

          <div id="panelJson" style="display:none">
            <pre id="resultPre">{}</pre>
          </div>
        </div>

        <div class="footer-actions">
          <button id="btnCopyJson" class="btn btn-secondary">Copy JSON</button>
          <button id="btnDownload" class="btn btn-secondary">Download token.json</button>
          <button id="btnCloseModal" class="btn btn-ghost right">Close</button>
        </div>
      </div>
    </div>
  </div>

<script>
(() => {
  const $ = (id) => document.getElementById(id);

  const state = {
    loading: false,
    clientId: "",
    clientSecret: "",
    verifyUrl: "",
    userCode: "",
    deviceCode: "",

    // polling control: no overlap; next only after last finished
    pollTimer: null,
    pollIntervalMs: 2000,
    pollInFlight: false,
    pollingEnabled: false,

    lastPollOk: null,   // last successful poll JSON (accessToken present)
    resultJson: "",     // token.json content shown in JSON tab
  };

  const ui = {
    btnLogin: $("btnLogin"),
    btnClear: $("btnClear"),
    verifyUrl: $("verifyUrl"),
    userCode: $("userCode"),
    deviceCode: $("deviceCode"),
    btnCopyLink: $("btnCopyLink"),
    btnOpenLink: $("btnOpenLink"),

    statusBox: $("statusBox"),
    statusText: $("statusText"),
    statusDot: $("statusDot"),
    statusLabel: $("statusLabel"),

    modalBackdrop: $("modalBackdrop"),
    modalScroll: $("modalScroll"),
    panelViewer: $("panelViewer"),
    panelJson: $("panelJson"),
    resultPre: $("resultPre"),
    btnCopyJson: $("btnCopyJson"),
    btnDownload: $("btnDownload"),
    btnCloseModal: $("btnCloseModal"),

    tabViewer: $("tabViewer"),
    tabJson: $("tabJson"),
  };

  function escapeHtml(str) {
    return String(str)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function setBusy(b) {
    state.loading = b;
    ui.btnLogin.disabled = b;
    ui.btnClear.disabled = b;
  }

  function setStatus(kind, text, label) {
    ui.statusText.textContent = text;
    ui.statusLabel.textContent = label;

    ui.statusBox.classList.remove("ok", "bad", "warn");
    ui.statusDot.classList.remove("ping");
    ui.statusDot.style.background = "rgba(255,255,255,.55)";

    if (kind === "pending" || kind === "polling") {
      ui.statusBox.classList.add("warn");
      ui.statusDot.classList.add("ping");
      ui.statusDot.style.background = "rgba(245,158,11,.9)";
    } else if (kind === "ok") {
      ui.statusBox.classList.add("ok");
      ui.statusDot.style.background = "rgba(16,185,129,.95)";
    } else if (kind === "error") {
      ui.statusBox.classList.add("bad");
      ui.statusDot.style.background = "rgba(239,68,68,.95)";
    }
  }

  function stopPolling() {
    state.pollingEnabled = false;
    if (state.pollTimer) clearTimeout(state.pollTimer);
    state.pollTimer = null;
    state.pollInFlight = false;
  }

  function scheduleNextPoll() {
    if (!state.pollingEnabled) return;
    if (state.pollTimer) clearTimeout(state.pollTimer);

    state.pollTimer = setTimeout(async () => {
      await pollOnce();
      scheduleNextPoll();
    }, state.pollIntervalMs);
  }

  async function apiStrict(path, body) {
    const r = await fetch(path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: body ? JSON.stringify(body) : undefined
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(typeof data === "string" ? data : JSON.stringify(data));
    return data;
  }

  async function apiAllowNon200(path, body) {
    const r = await fetch(path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: body ? JSON.stringify(body) : undefined
    });
    const data = await r.json().catch(() => ({}));
    return { ok: r.ok, status: r.status, data };
  }

  function setLink(url) {
    state.verifyUrl = url || "";
    ui.verifyUrl.textContent = url || "—";
    ui.verifyUrl.title = url || "";
    ui.btnCopyLink.disabled = !url;
    ui.btnOpenLink.disabled = !url;
  }

  function setCodes({ userCode, deviceCode }) {
    state.userCode = userCode || "";
    state.deviceCode = deviceCode || "";
    ui.userCode.textContent = userCode || "—";
    ui.deviceCode.textContent = deviceCode || "—";
  }

  async function copy(text) {
    if (!text) return;
    await navigator.clipboard.writeText(text);
  }

  function showModal() {
    ui.modalBackdrop.classList.add("show");
    // NEW: reset scroll to top when opening (optional but nice)
    if (ui.modalScroll) ui.modalScroll.scrollTop = 0;
  }
  function hideModal() {
    ui.modalBackdrop.classList.remove("show");
  }

  function setTab(which) {
    const viewer = which === "viewer";
    ui.tabViewer.classList.toggle("active", viewer);
    ui.tabJson.classList.toggle("active", !viewer);
    ui.panelViewer.style.display = viewer ? "flex" : "none";
    ui.panelJson.style.display = viewer ? "none" : "block";
    // keep scroll at top when switching
    if (ui.modalScroll) ui.modalScroll.scrollTop = 0;
  }

  function kvRow(key, val, onCopy) {
    const row = document.createElement("div");
    row.className = "kv-row";
    row.innerHTML = \`
      <div class="kv-key">\${escapeHtml(key)}</div>
      <div class="kv-val mono">\${escapeHtml(val)}</div>
      <div class="kv-acrylic"></div>
      <div class="kv-actions">
        <button class="btn btn-secondary btn-xs">Copy</button>
      </div>
    \`;
    const btn = row.querySelector("button");
    btn.addEventListener("click", onCopy);
    return row;
  }

  function normalizeValue(v) {
    if (v === null || v === undefined) return "null";
    if (typeof v === "string") return v;
    return JSON.stringify(v);
  }

  function renderViewer({ cid, cs, pollOk }) {
    ui.panelViewer.innerHTML = "";

    const cidVal = cid || "";
    const csVal = cs || "";

    ui.panelViewer.appendChild(
      kvRow("clientId", cidVal || "—", async () => {
        await copy(cidVal);
        setStatus("ok", "Copied: clientId", "SUCCESS");
      })
    );

    ui.panelViewer.appendChild(
      kvRow("clientSecret", csVal || "—", async () => {
        await copy(csVal);
        setStatus("ok", "Copied: clientSecret", "SUCCESS");
      })
    );

    if (!pollOk || typeof pollOk !== "object") {
      const msg = document.createElement("div");
      msg.className = "muted";
      msg.textContent = "No poll response data.";
      ui.panelViewer.appendChild(msg);
      return;
    }

    const entries = Object.entries(pollOk);
    for (const [k, v] of entries) {
      const val = normalizeValue(v);
      ui.panelViewer.appendChild(
        kvRow(k, val, async () => {
          await copy(val);
          setStatus("ok", "Copied: " + k, "SUCCESS");
        })
      );
    }
  }

  function buildTokenJson(cid, cs, pollOk) {
    const out = {
      client_id: cid,
      client_secret: cs,
      refresh_token: pollOk && pollOk.refreshToken ? pollOk.refreshToken : null,
      poll_response: pollOk || null
    };
    return JSON.stringify(out, null, 2);
  }

  async function pollOnce() {
    if (!state.pollingEnabled) return;
    if (state.pollInFlight) return;

    state.pollInFlight = true;

    try {
      const { ok, status, data } = await apiAllowNon200("/api/poll", {
        clientId: state.clientId,
        clientSecret: state.clientSecret,
        deviceCode: state.deviceCode,
      });

      if (data && data.accessToken) {
        state.lastPollOk = data;
        stopPolling();
        setStatus("ok", "Authorized! Refresh token received.", "SUCCESS");

        state.resultJson = buildTokenJson(state.clientId, state.clientSecret, data);
        ui.resultPre.textContent = state.resultJson;

        renderViewer({ cid: state.clientId, cs: state.clientSecret, pollOk: data });

        setTab("viewer");
        showModal();
        return;
      }

      // PENDING (even if backend returns 400)
      if (data && data.error === "authorization_pending") {
        setStatus("pending", "Pending authorization... (complete it in the opened page)", "PENDING");
        return;
      }

      if (data && data.error === "slow_down") {
        state.pollIntervalMs = Math.min(state.pollIntervalMs + 2000, 10000);
        setStatus(
          "pending",
          "Slow down requested. Polling every " + (state.pollIntervalMs / 1000).toFixed(1) + "s",
          "PENDING"
        );
        return;
      }

      if (data && data.error === "expired_token") {
        stopPolling();
        setStatus("error", "Device code expired. Click Login again.", "EXPIRED");
        return;
      }

      if (!ok) {
        stopPolling();
        setStatus("error", "Error: " + JSON.stringify(data), "ERROR");
        return;
      }

      setStatus("pending", "Waiting... " + JSON.stringify(data), "PENDING");
    } catch (e) {
      stopPolling();
      setStatus("error", "Polling error: " + (e && e.message ? e.message : String(e)), "ERROR");
    } finally {
      state.pollInFlight = false;
    }
  }

  function startPolling() {
    stopPolling();
    state.pollingEnabled = true;
    setStatus("pending", "Polling started... authorize in the verification page.", "PENDING");

    pollOnce().finally(() => {
      scheduleNextPoll();
    });
  }

  async function loginFlow() {
    stopPolling();
    setBusy(true);

    setLink("");
    setCodes({ userCode: "", deviceCode: "" });
    state.clientId = "";
    state.clientSecret = "";
    state.lastPollOk = null;
    state.resultJson = "";

    setStatus("polling", "Registering OIDC client...", "WORKING");

    try {
      const reg = await apiStrict("/api/register");
      state.clientId = reg.clientId;
      state.clientSecret = reg.clientSecret;

      setStatus("polling", "Generating device flow link...", "WORKING");

      const dev = await apiStrict("/api/device", {
        clientId: state.clientId,
        clientSecret: state.clientSecret
      });

      const link = dev.verificationUriComplete || dev.verificationUri || "";
      setLink(link);
      setCodes({ userCode: dev.userCode, deviceCode: dev.deviceCode });

      state.pollIntervalMs = Math.max(2000, (dev.interval ? dev.interval * 1000 : 2000));
      setStatus("pending", "Link ready. Hover to Copy/Open. Polling in background...", "PENDING");

      startPolling();
    } catch (e) {
      stopPolling();
      setStatus("error", "Error: " + (e && e.message ? e.message : String(e)), "ERROR");
    } finally {
      setBusy(false);
    }
  }

  function clearAll() {
    stopPolling();
    state.clientId = "";
    state.clientSecret = "";
    state.verifyUrl = "";
    state.userCode = "";
    state.deviceCode = "";
    state.lastPollOk = null;
    state.resultJson = "";
    state.pollIntervalMs = 2000;

    setLink("");
    setCodes({ userCode: "", deviceCode: "" });
    setStatus("idle", "Cleared. Click Login to start.", "IDLE");
    hideModal();
  }

  // ===== Bindings =====
  ui.btnLogin.addEventListener("click", loginFlow);
  ui.btnClear.addEventListener("click", clearAll);

  ui.btnCopyLink.addEventListener("click", async () => {
    if (!state.verifyUrl) return;
    await copy(state.verifyUrl);
    setStatus("pending", "Link copied. Continue authorization in the opened page.", "PENDING");
  });

  ui.btnOpenLink.addEventListener("click", () => {
    if (!state.verifyUrl) return;
    window.open(state.verifyUrl, "_blank", "noopener,noreferrer");
    setStatus("pending", "Link opened. Complete authorization, we are polling...", "PENDING");
  });

  ui.btnCopyJson.addEventListener("click", async () => {
    if (!state.resultJson) return;
    await copy(state.resultJson);
    setStatus("ok", "token.json copied.", "SUCCESS");
  });

  ui.btnDownload.addEventListener("click", () => {
    if (!state.resultJson) return;
    const blob = new Blob([state.resultJson], { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "token.json";
    a.click();
    URL.revokeObjectURL(a.href);
    setStatus("ok", "Downloaded token.json", "SUCCESS");
  });

  ui.btnCloseModal.addEventListener("click", hideModal);

  ui.modalBackdrop.addEventListener("click", (e) => {
    if (e.target === ui.modalBackdrop) hideModal();
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") hideModal();
  });

  ui.tabViewer.addEventListener("click", () => setTab("viewer"));
  ui.tabJson.addEventListener("click", () => setTab("json"));

  // Initial state
  setLink("");
  setCodes({ userCode: "", deviceCode: "" });
  setStatus("idle", "Click Login to start.", "IDLE");
  setTab("viewer");
})();
</script>
</body>
</html>`
}
