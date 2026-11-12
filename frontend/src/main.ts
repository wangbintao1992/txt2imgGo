import { WordRender, events } from "./mygo";
import { renderDocxToPngs, type RenderProgress } from "./renderDocxToPngs";
import "./style.css";

const app = document.querySelector<HTMLDivElement>("#app")!;
app.innerHTML = `
  <main class="shell">
    <header class="head">
      <h1 id="title">等待 Word 文件</h1>
      <p id="hint">在左侧拖入 .docx，或点击「选择 Word 文件」。A4 分页，一页一张 PNG，不生成 PDF。</p>
    </header>
    <section class="card">
      <div id="status" class="status">尚未开始转换</div>
      <div class="bar"><div id="fill"></div></div>
      <div id="detail" class="detail"></div>
    </section>
    <footer class="foot">A4 · 20mm 边距 · 2x 高清 · 白底 · 30 秒无进展超时</footer>
  </main>
`;

const titleEl = document.querySelector<HTMLHeadingElement>("#title")!;
const hintEl = document.querySelector<HTMLParagraphElement>("#hint")!;
const statusEl = document.querySelector<HTMLDivElement>("#status")!;
const fill = document.querySelector<HTMLDivElement>("#fill")!;
const detail = document.querySelector<HTMLDivElement>("#detail")!;

let busy = false;

function setStatus(text: string, percent: number, error = false) {
  statusEl.textContent = text;
  statusEl.classList.toggle("error", error);
  fill.style.width = `${Math.max(0, Math.min(100, percent))}%`;
}

function progressText(p: RenderProgress): { text: string; percent: number } {
  switch (p.stage) {
    case "parse":
      return { text: "正在解析文档…", percent: 6 };
    case "paginate": {
      const total = p.total ?? 0;
      const done = p.done ?? 0;
      const ratio = total > 0 ? Math.min(1, done / total) : 0;
      const text = total > 0 ? `正在分页 ${done} / ${total} 页…` : "正在按 A4 分页…";
      return { text, percent: 10 + ratio * 30 };
    }
    case "capture": {
      const total = p.total ?? 0;
      const done = p.done ?? 0;
      const ratio = total > 0 ? done / total : 0;
      return { text: `正在截图 ${done} / ${total} 页…`, percent: 40 + ratio * 58 };
    }
  }
}

function report(stage: string, done = 0, total = 0) {
  void WordRender.report(stage, done, total).catch(() => undefined);
}

function base64ToArrayBuffer(base64: string): ArrayBuffer {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

events.wordDocx.on(async (payload) => {
  if (busy) return;
  busy = true;
  titleEl.textContent = payload.name;
  hintEl.textContent = "正在 WebView 内渲染并分页，完成后会保存到左侧显示的目录。";
  detail.textContent = "";
  setStatus("正在准备…", 2);

  try {
    const pages = await renderDocxToPngs(base64ToArrayBuffer(payload.data), (p) => {
      const { text, percent } = progressText(p);
      setStatus(text, percent);
      report(p.stage, p.done ?? 0, p.total ?? 0);
    }, undefined, (message) => {
      void WordRender.log(message).catch(() => undefined);
    });

    report("save");
    setStatus(`正在保存 ${pages.length} 张图片…`, 96);
    const dir = await WordRender.savePages(pages);
    setStatus(`完成：${pages.length} 张 PNG`, 100);
    detail.textContent = dir;
    detail.title = dir;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    setStatus(message, 0, true);
    detail.textContent = "";
    void WordRender.fail(message).catch(() => undefined);
  } finally {
    busy = false;
  }
});
