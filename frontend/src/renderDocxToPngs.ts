import { renderAsync } from "docx-preview";
import { Previewer } from "pagedjs";
import html2canvas from "html2canvas";

export type RenderStage = "parse" | "paginate" | "capture";

export interface RenderProgress {
  stage: RenderStage;
  done?: number;
  total?: number;
}

export type ProgressHandler = (progress: RenderProgress) => void;

const DEFAULT_TIMEOUT_MS = 30_000;

// A4 高度减去上下各 20mm 页边距，约 971 CSS px。
const PAGE_CONTENT_PX = 1122.5 - (40 * 96) / 25.4;

/** 印刷分页规则：A4、20mm 边距，表格行不拆、短段不拆、控制孤行寡行。 */
const PAGE_CSS = `
@page {
  size: A4;
  margin: 20mm;
}
@media print {
  @page {
    size: A4;
    margin: 20mm;
  }
}
html, body {
  background: #ffffff;
}
section.docx {
  padding: 0 !important;
  margin: 0 !important;
  width: auto !important;
  min-height: 0 !important;
  max-height: none !important;
  display: block !important;
  overflow: visible !important;
  box-shadow: none !important;
}
.docx p {
  margin: 0;
}
.pagedjs_pages {
  background: #ffffff;
}
.pagedjs_page {
  background: #ffffff !important;
}
table {
  border-collapse: collapse;
}
tr, td, th {
  break-inside: avoid;
  page-break-inside: avoid;
}
h1, h2, h3, h4, h5, h6 {
  break-after: avoid;
  page-break-after: avoid;
}
img {
  max-width: 100%;
}
`;

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    let settled = false;
    const done = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    const timer = window.setTimeout(done, 100);
    requestAnimationFrame(() => {
      window.clearInterval(timer);
      done();
    });
  });
}

function settleWithin(promise: Promise<unknown>, ms: number): Promise<void> {
  return Promise.race([
    promise.then(
      () => undefined,
      () => undefined,
    ),
    new Promise<void>((resolve) => window.setTimeout(resolve, ms)),
  ]);
}

function waitForImages(root: Element): Promise<void> {
  const images = Array.from(root.querySelectorAll("img"));
  return Promise.all(
    images.map((img) =>
      settleWithin(
        img.decode ? img.decode() : Promise.resolve(),
        3000,
      ),
    ),
  ).then(() => undefined);
}

async function waitForAssets(root: Element): Promise<void> {
  // WKWebView 的 document.fonts.ready 可能迟迟不 resolve；系统字体本来就能立即排版，
  // 等太久只会把 30 秒预算耗光。
  if (document.fonts.status !== "loaded") {
    await settleWithin(document.fonts.ready, 1500);
  }
  await waitForImages(root);
  await nextFrame();
}

function collectCss(styleHost: Element): string {
  return Array.from(styleHost.querySelectorAll("style"))
    .map((el) => el.textContent ?? "")
    .join("\n");
}

function canvasToBase64(canvas: HTMLCanvasElement): string {
  const dataURL = canvas.toDataURL("image/png");
  const comma = dataURL.indexOf(",");
  return comma >= 0 ? dataURL.slice(comma + 1) : dataURL;
}

/**
 * 把 docx 渲染成分页后的 PNG（base64，不含 data URL 前缀）。
 * 只截图 pagedjs 生成的单页节点，不使用可视区域截图，也不生成 PDF。
 */
export async function renderDocxToPngs(
  buffer: ArrayBuffer,
  onProgress?: ProgressHandler,
  timeoutMs = DEFAULT_TIMEOUT_MS,
  onLog?: (message: string) => void,
): Promise<string[]> {
  const log = (message: string) => {
    try {
      onLog?.(message);
    } catch {
      // 日志失败不影响转换。
    }
  };
  const started = performance.now();
  const elapsed = () => Math.round(performance.now() - started);
  log(`开始：${buffer.byteLength} 字节`);
  let cancelled = false;
  let timer = 0;
  let lastProgress = performance.now();
  const progress = (p: RenderProgress) => {
    lastProgress = performance.now();
    onProgress?.(p);
  };

  const stage = document.createElement("div");
  stage.className = "render-stage";
  const source = document.createElement("div");
  source.className = "render-source";
  const styleHost = document.createElement("div");
  styleHost.className = "render-styles";
  const pagesHost = document.createElement("div");
  pagesHost.className = "render-pages";
  stage.append(source, pagesHost);
  const cover = document.createElement("div");
  cover.className = "render-cover";
  cover.textContent = "正在渲染 Word 文档…";
  document.body.append(stage, cover);
  document.head.appendChild(styleHost);

  const failIfCancelled = () => {
    if (cancelled) throw new Error("渲染已取消");
  };

  const run = async (): Promise<string[]> => {
    log("阶段 1/4：docx-preview 解析");
    progress({ stage: "parse" });
    await renderAsync(buffer, source, styleHost, {
      className: "docx",
      inWrapper: false,
      hideWrapperOnPrint: true,
      breakPages: true,
      ignoreWidth: true,
      ignoreHeight: true,
      ignoreLastRenderedPageBreak: true,
      experimental: true,
      useBase64URL: true,
    });
    // docx-preview 的外壳默认是 flex + overflow:hidden；WKWebView 下 pagedjs
    // 按这个外壳测量会提前断页，这里统一改成普通块级流。
    const shells = Array.from(source.querySelectorAll<HTMLElement>("section.docx"));
    shells.forEach((shell) => {
      shell.style.padding = "0";
      shell.style.margin = "0";
      shell.style.width = "auto";
      shell.style.height = "auto";
      shell.style.minHeight = "0";
      shell.style.maxHeight = "none";
      shell.style.display = "block";
      shell.style.overflow = "visible";
      shell.style.boxShadow = "none";
    });
    if (shells[0]) {
      const cs = getComputedStyle(shells[0]);
      log(`外壳 ${shells.length} 个，display=${cs.display} overflow=${cs.overflow} pad=${cs.padding}`);
    }
    failIfCancelled();
    log("等待字体与图片");
    await waitForAssets(source);
    log(`资源就绪：${elapsed()}ms`);

    // docx-preview 用多个 <section> 表示 Word 的显式分页，但不写 CSS；
    // 这里补上分页规则，交给 pagedjs 处理。
    const sections = Array.from(
      source.querySelectorAll<HTMLElement>(":scope > section.docx"),
    );
    sections.slice(1).forEach((section) => {
      section.dataset.breakBefore = "page";
      section.style.breakBefore = "page";
    });
    failIfCancelled();

    log("阶段 2/4：pagedjs 分页");
    const estimatedPages = Math.max(
      1,
      Math.ceil(source.scrollHeight / PAGE_CONTENT_PX),
    );
    progress({ stage: "paginate", done: 0, total: estimatedPages });
    const css = collectCss(styleHost) + PAGE_CSS;
    const previewer = new Previewer();
    let paginated = 0;
    previewer.on("page", () => {
      paginated += 1;
      progress({
        stage: "paginate",
        done: paginated,
        total: Math.max(estimatedPages, paginated),
      });
      if (paginated === 1 || paginated % 10 === 0) {
        log(`已分页 ${paginated} 页`);
      }
    });
    await previewer.preview(source, [{ "docx.css": css }], pagesHost);
    log(`分页完成：${paginated} 页，${elapsed()}ms`);
    failIfCancelled();
    await waitForAssets(pagesHost);

    const pages = Array.from(
      pagesHost.querySelectorAll<HTMLElement>(".pagedjs_page"),
    );
    log(`分页 DOM：${pages.length} 页，source 高 ${Math.round(source.scrollHeight)}px`);
    if (pages.length === 0) {
      throw new Error("分页结果为空，文档可能没有可渲染内容");
    }

    log(`阶段 3/4：截图 ${pages.length} 页，scale=2`);
    const pngs: string[] = [];
    for (let i = 0; i < pages.length; i++) {
      failIfCancelled();
      const page = pages[i];
      const rect = page.getBoundingClientRect();
      const shotStart = performance.now();
      if (i === 0 || (i + 1) % 10 === 0) {
        log(`截图第 ${i + 1} / ${pages.length} 页，${Math.ceil(rect.width)}x${Math.ceil(rect.height)}`);
      }
      const canvas = await html2canvas(page, {
        scale: 2,
        backgroundColor: "#ffffff",
        width: Math.ceil(rect.width),
        height: Math.ceil(rect.height),
        windowWidth: Math.ceil(rect.width),
        windowHeight: Math.ceil(rect.height),
        useCORS: true,
        logging: false,
        scrollX: 0,
        scrollY: 0,
      });
      pngs.push(canvasToBase64(canvas));
      canvas.width = 0;
      canvas.height = 0;
      if ((i + 1) % 10 === 0 || i + 1 === pages.length) {
        log(`截图第 ${i + 1} 页完成：${Math.round(performance.now() - shotStart)}ms`);
      }
      progress({ stage: "capture", done: i + 1, total: pages.length });
      await nextFrame();
    }
    log(`全部完成：${elapsed()}ms`);
    return pngs;
  };

  // 30 秒无进展才判定超时：分页、截图每完成一页都会续期。
  const hardDeadline = started + Math.max(timeoutMs * 6, 180_000);
  const timeout = new Promise<never>((_, reject) => {
    const id = window.setInterval(() => {
      const now = performance.now();
      if (now > hardDeadline) {
        cancelled = true;
        window.clearInterval(id);
        reject(new Error(`渲染超时：总耗时超过 ${Math.round((hardDeadline - started) / 1000)} 秒`));
        return;
      }
      if (now - lastProgress > timeoutMs) {
        cancelled = true;
        window.clearInterval(id);
        reject(new Error(`渲染超时：超过 ${Math.round(timeoutMs / 1000)} 秒没有进展`));
      }
    }, 500);
    timer = id;
  });

  try {
    return await Promise.race([run(), timeout]);
  } catch (error) {
    log(`失败：${error instanceof Error ? error.stack ?? error.message : String(error)}`);
    throw error;
  } finally {
    log(`清理，总耗时 ${elapsed()}ms`);
    window.clearInterval(timer);
    stage.remove();
    cover.remove();
    styleHost.remove();
  }
}
