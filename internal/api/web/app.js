// Dashboard do GoNginx Observer — JavaScript puro, sem dependências.
// Todo texto vindo da API (caminhos, IPs) é inserido via textContent,
// nunca via innerHTML, pois tem origem em requisições de clientes.
"use strict";

const $ = (id) => document.getElementById(id);
const nf = new Intl.NumberFormat("pt-BR");
const nf1 = new Intl.NumberFormat("pt-BR", { maximumFractionDigits: 1, minimumFractionDigits: 1 });
const SVG = "http://www.w3.org/2000/svg";
const state = {
  mode: "beginner",
  search: "",
  searchScope: "all",
  port: "all",
  status: "all",
  period: "window",
};

function el(tag, attrs = {}, text) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text !== undefined) e.textContent = text;
  return e;
}
function svg(tag, attrs = {}) {
  const e = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
}
function fmtBytes(b) {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return (i === 0 ? nf.format(b) : nf1.format(b)) + " " + u[i];
}
const pct = (x) => nf1.format(x * 100) + "%";
const fmtMs = (ms) => (ms >= 1000 ? nf1.format(ms / 1000) + " s" : nf1.format(ms) + " ms");

// ---------------------------------------------------------------------------
function renderTiles(m) {
  $("k-total").textContent = nf.format(m.cumulative.total_requests);
  if (state.period === "cumulative") {
    $("k-rps").textContent = nf.format(m.cumulative.total_requests);
    $("k-rps-hint").textContent = "acessos em todo o período";
    $("k-err").textContent = pct(m.cumulative.error_rate);
    $("k-err-hint").textContent = "acessos com resposta diferente de sucesso";
    $("k-lat").textContent = fmtMs(m.cumulative.avg_response_ms);
    $("k-lat-hint").textContent = "tempo médio em todo o período";
  } else {
    $("k-rps").textContent = nf1.format(m.window.requests_per_second);
    $("k-rps-hint").textContent = `${nf.format(m.window.requests)} acessos nos últimos ${m.window.seconds} s`;
    $("k-err").textContent = pct(m.window.error_rate);
    $("k-err-hint").textContent = "acessos que não tiveram uma resposta de sucesso";
    $("k-lat").textContent = fmtMs(m.window.avg_response_ms);
    $("k-lat-hint").textContent = `janela de 60 s · acumulado ${fmtMs(m.cumulative.avg_response_ms)}`;
  }
  $("k-bytes").textContent = fmtBytes(m.cumulative.bytes_sent);
  const problemRate = state.period === "cumulative" ? m.cumulative.error_rate : m.window.error_rate;
  const situation = problemRate >= 0.20
    ? "Há muitos acessos com problema. Vale verificar as páginas e os alertas."
    : problemRate > 0
      ? "Alguns acessos tiveram problema, mas a maior parte funcionou normalmente."
      : "Os acessos estão funcionando normalmente.";
  $("simple-summary").textContent = `${situation} Foram registrados ${nf.format(m.cumulative.total_requests)} acessos desde o início.`;
}

// Gráfico de barras empilhadas (por segundo): 2xx/3xx, 4xx, 5xx.
function renderHistory(hist) {
  const box = $("history");
  const W = box.clientWidth || 800, H = box.clientHeight || 220;
  const padL = 40, padB = 20, padT = 6;
  const plotW = W - padL, plotH = H - padB - padT;
  const maxV = Math.max(5, ...hist.map((p) => p.requests));
  const step = niceStep(maxV / 4);
  const top = Math.ceil(maxV / step) * step;
  const y = (v) => padT + plotH - (v / top) * plotH;

  const s = svg("svg", { viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: "none" });
  for (let v = 0; v <= top; v += step) {
    s.append(svg("line", { class: "grid", x1: padL, x2: W, y1: y(v), y2: y(v) }));
    const t = svg("text", { class: "axis", x: padL - 6, y: y(v) + 4, "text-anchor": "end" });
    t.textContent = nf.format(v);
    s.append(t);
  }
  const n = hist.length, slot = plotW / n, bw = Math.max(2, slot - 2); // 2px de espaço entre barras
  const css = getComputedStyle(document.documentElement);
  const colors = { ok: css.getPropertyValue("--ok"), w: css.getPropertyValue("--warn"), c: css.getPropertyValue("--crit") };
  hist.forEach((p, i) => {
    const x = padL + i * slot + 1;
    const ok = p.requests - p.errors_4xx - p.errors_5xx;
    let base = y(0);
    // Ordem de baixo para cima: ok, 4xx, 5xx; 2px de separação entre segmentos.
    for (const [v, c] of [[ok, colors.ok], [p.errors_4xx, colors.w], [p.errors_5xx, colors.c]]) {
      if (v <= 0) continue;
      const h = Math.max(1, (v / top) * plotH);
      s.append(svg("rect", { x, y: base - h, width: bw, height: Math.max(1, h - 1), rx: Math.min(2, bw / 2), fill: c }));
      base -= h + 1;
    }
    const hit = svg("rect", { class: "hit", x: padL + i * slot, y: padT, width: slot, height: plotH });
    hit.addEventListener("mousemove", (e) => showTip(e, p));
    hit.addEventListener("mouseleave", hideTip);
    s.append(hit);
  });
  const ago = (i) => n - 1 - i;
  for (const i of [0, Math.floor(n / 2), n - 1]) {
    const t = svg("text", { class: "axis", x: padL + i * slot + slot / 2, y: H - 4, "text-anchor": i === 0 ? "start" : i === n - 1 ? "end" : "middle" });
    if (state.period === "cumulative") {
      t.textContent = new Date(hist[i].t * 1000).toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
    } else {
      t.textContent = ago(i) === 0 ? "agora" : `-${ago(i)} s`;
    }
    s.append(t);
  }
  box.replaceChildren(s);
}
function niceStep(raw) {
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  for (const m of [1, 2, 5, 10]) if (raw <= m * p) return m * p;
  return 10 * p;
}
function showTip(e, p) {
  const tip = $("tip");
  const card = tip.parentElement.getBoundingClientRect();
  const t = new Date(p.t * 1000).toLocaleTimeString("pt-BR");
  tip.replaceChildren(
    el("strong", {}, t), el("br"),
    document.createTextNode(`${nf.format(p.requests)} acessos · problemas: ${nf.format(p.errors_4xx + p.errors_5xx)}`),
  );
  tip.hidden = false;
  const x = Math.min(e.clientX - card.left + 12, card.width - tip.offsetWidth - 8);
  tip.style.left = `${Math.max(8, x)}px`;
  tip.style.top = `${e.clientY - card.top - 50}px`;
}
function hideTip() { $("tip").hidden = true; }

// Lista de barras horizontais com rótulo e valor visíveis.
function renderBars(container, items, { cls, emptyText = "Sem dados ainda." } = {}) {
  if (!items.length) {
    container.replaceChildren(el("p", { class: "empty" }, emptyText));
    return;
  }
  const max = Math.max(...items.map((i) => i.count));
  container.replaceChildren(...items.map((it) => {
    const row = el("div", { class: "bar-row" });
    row.append(el("span", { class: "bar-label", title: it.key }, it.key), el("span", { class: "bar-val" }, nf.format(it.count)));
    const track = el("div", { class: "bar-track" });
    const fill = el("div", { class: "bar-fill" + (cls ? " " + cls(it) : "") });
    fill.style.width = `${max ? (it.count / max) * 100 : 0}%`;
    track.append(fill);
    row.append(track);
    return row;
  }));
}

function renderStatus(sc) {
  const total = Object.values(sc.classes).reduce((a, b) => a + b, 0);
  const classes = state.status === "all"
    ? ["2xx", "3xx", "4xx", "5xx"]
    : [state.status];
  const items = classes.map((k) => ({ key: k, count: sc.classes[k] }));
  if (state.status === "all" && sc.classes["1xx"]) items.unshift({ key: "1xx", count: sc.classes["1xx"] });
  if (state.status !== "all") {
    items.splice(0, items.length,
      ...classes.map((k) => ({ key: k, count: sc.classes[k] })).filter((it) => it.count));
  }
  renderBars($("classes"), total ? items : [], { cls: (it) => "c-" + it.key });
  const search = state.search.trim();
  const codes = sc.codes.filter((c) =>
    state.status === "all" || String(c.code).startsWith(state.status[0])
  ).filter((c) =>
    (state.searchScope !== "ips" && state.searchScope !== "alerts")
    && (!search || String(c.code).includes(search))
  );
  $("codes").replaceChildren(...codes.map((c) => {
    const tr = el("tr");
    tr.append(el("td", {}, String(c.code)), el("td", {}, nf.format(c.count)));
    return tr;
  }));
}

function renderAlerts(a) {
  $("alerts-total").textContent = `(${nf.format(a.total)} desde a inicialização)`;
  const list = $("alerts");
  const search = state.search.trim().toLowerCase();
  const items = a.items.filter((it) => {
    if (state.searchScope === "pages" || state.searchScope === "ips") return false;
    if (!search) return true;
    return [it.type, it.entity, it.description].join(" ").toLowerCase().includes(search);
  });
  if (!items.length) {
    list.replaceChildren(el("li", { class: "empty" }, "Nenhum alerta. Regras: > " +
      nf.format(a.rules.ip_request_threshold) + " req por IP em 60 s; 5xx > " +
      pct(a.rules.server_error_rate) + " com ≥ " + nf.format(a.rules.min_samples) + " amostras."));
    return;
  }
  list.replaceChildren(...items.map((it) => {
    const crit = it.type === "server_error_rate";
    const li = el("li", { class: "alert" + (crit ? " crit" : "") });
    const label = crit ? "⚠ Taxa de 5xx elevada" : "⚠ Volume elevado por IP";
    const obs = crit ? `${pct(it.observed)} (limite ${pct(it.threshold)})` : `${nf.format(it.observed)} req (limite ${nf.format(it.threshold)})`;
    li.append(
      el("span", { class: "badge" }, label),
      el("span", { class: "ent", title: it.entity }, `${it.entity} · ${obs}`),
      el("time", { datetime: it.detected_at }, new Date(it.detected_at).toLocaleTimeString("pt-BR")),
      el("span", { class: "desc" }, it.description),
    );
    return li;
  }));
}

function renderPipeline(m) {
  const p = m.pipeline, r = m.runtime;
  const rows = [
    ["Workers de parsing", nf.format(p.workers)],
    ["Linhas lidas", nf.format(p.lines_read)],
    ["Eventos agregados", nf.format(p.aggregated)],
    ["Linhas inválidas", nf.format(p.parse_errors + p.oversized_lines)],
    ["Fila bruta (len/cap)", `${nf.format(p.raw_queue.len)} / ${nf.format(p.raw_queue.cap)}`],
    ["Fila de eventos", `${nf.format(p.event_queue.len)} / ${nf.format(p.event_queue.cap)}`],
    ["Goroutines", nf.format(r.goroutines)],
    ["Heap em uso", fmtBytes(r.heap_alloc_bytes)],
    ["Memória do runtime", fmtBytes(r.sys_bytes)],
    ["Rotações / truncamentos", `${p.rotations} / ${p.truncations}`],
    ["Arquivo de log", p.log_file_present ? "presente" : "ausente"],
    ["Runtime", r.go_version],
  ];
  $("pipe").replaceChildren(...rows.map(([k, v]) => {
    const d = el("div");
    d.append(el("dt", {}, k), el("dd", {}, v));
    return d;
  }));
}

// ---------------------------------------------------------------------------
async function getJSON(path) {
  const r = await fetch(path, { cache: "no-store" });
  if (!r.ok) throw new Error(`${path}: HTTP ${r.status}`);
  return r.json();
}

let lastMetrics = null;
let lastHistory = null;
async function tick() {
  try {
    const [m, sc, ep, ips, al] = await Promise.all([
      getJSON("api/v1/metrics"), getJSON("api/v1/status-codes"), getJSON("api/v1/top-endpoints?limit=100"),
      getJSON("api/v1/top-ips?limit=100"), getJSON("api/v1/alerts?limit=1000"),
    ]);
    renderTiles(m);
    lastMetrics = m;
    lastHistory = state.period === "cumulative" ? m.all_history : m.history;
    renderHistory(lastHistory);
    $("history-title").textContent = state.period === "cumulative"
      ? "Acessos — todo o período"
      : "Acessos — últimos 60 segundos";
    renderStatus(sc);
    const search = state.search.trim().toLowerCase();
    const endpoints = ep.items.filter((it) => {
      if (state.searchScope === "ips" || state.searchScope === "alerts") return false;
      if (state.port !== "all" && !it.key.startsWith(state.port + " ·")) return false;
      return !search || it.key.toLowerCase().includes(search);
    });
    const ipItems = ips.items.filter((it) => {
      if (state.searchScope === "pages" || state.searchScope === "alerts") return false;
      return !search || it.key.toLowerCase().includes(search);
    });
    renderBars($("endpoints"), endpoints.slice(0, 20));
    renderBars($("ips"), ipItems.slice(0, 20));
    $("mask").textContent = ips.mask === "partial" ? "(mascarados por prefixo)" : "";
    renderAlerts(al);
    renderPipeline(m);
    $("conn").className = "conn ok";
    $("conn-text").textContent = "ao vivo · " + new Date().toLocaleTimeString("pt-BR");
  } catch (err) {
    $("conn").className = "conn err";
    $("conn-text").textContent = "sem conexão com a API";
    console.warn(err);
  } finally {
    setTimeout(tick, 1000);
  }
}
window.addEventListener("resize", () => lastHistory && renderHistory(lastHistory));
document.querySelectorAll(".mode").forEach((button) => {
  button.addEventListener("click", () => {
    state.mode = button.dataset.mode;
    document.body.dataset.mode = state.mode;
    document.querySelectorAll(".mode").forEach((item) => {
      item.classList.toggle("active", item === button);
    });
  });
});
$("search").addEventListener("input", (event) => {
  state.search = event.target.value;
});
$("search-scope").addEventListener("change", (event) => {
  state.searchScope = event.target.value;
});
$("port-filter").addEventListener("change", (event) => {
  state.port = event.target.value;
});
$("clear-search").addEventListener("click", () => {
  state.search = "";
  state.searchScope = "all";
  state.port = "all";
  state.status = "all";
  $("search").value = "";
  $("search-scope").value = "all";
  $("port-filter").value = "all";
  $("status-filter").value = "all";
});
$("status-filter").addEventListener("change", (event) => {
  state.status = event.target.value;
});
$("period-filter").addEventListener("change", (event) => {
  state.period = event.target.value;
  if (lastMetrics) {
    renderTiles(lastMetrics);
    lastHistory = state.period === "cumulative" ? lastMetrics.all_history : lastMetrics.history;
    renderHistory(lastHistory);
    $("history-title").textContent = state.period === "cumulative"
      ? "Acessos — todo o período"
      : "Acessos — últimos 60 segundos";
  }
});
document.body.dataset.mode = state.mode;
tick();
