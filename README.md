# TXT 转图片

使用 Go 与 [MyGo](https://github.com/egoist/mygo) 原生 UI 编写的桌面工具：把 TXT 文本按可配置排版转换成多张 PNG 图片，支持拖入文件、实时预览、系统字体选择与斜向平铺水印。

## 界面预览

![TXT 转图片界面](resources/img.png)

## 功能

- 选择或拖入 `.txt` 文件，可一键清空已选文件
- 可切换是否按换行符区分段落
- 可配置每行字数（1–100）
- 可输入输出图片宽度与高度（320–4000 × 320–6000 px）
- 自动选择系统字体，也可从系统字体列表切换
- 选择图片输出目录，默认与 TXT 文件同目录
- 实时预览实际出图效果，显示图片总数与页码
- 可输入水印文字，支持调整水印字号、透明度与倾斜角度（默认斜向平铺）
- 支持 UTF-8（含 BOM）、UTF-16（含 BOM）与 GB18030 文本
- 支持批量生成 PNG 图片或导出单个 PDF 文件
- 生成完成后弹出成功提示

## 技术架构

```
main.go       应用状态、MyGo 原生界面、预览调度与 PNG 导出
render.go     文本分行分页、字体栅格化、页面绘制、水印旋转平铺
pdf.go        纯 Go PDF 写入：逐页渲染为 RGB 图像并嵌入 PDF
fonts.go      系统字体探测、字体列表扫描、TTC/TTF/OTF 字体解析缓存
textfile.go   TXT 编码识别与解码
mygo.json     MyGo 应用配置
resources/    应用图标与界面截图
```

- **UI**：MyGo `ui` 原生控件，纯 Go，无 WebView。
- **文本排版**：按字符数换行、按换行符形成段落，并根据图片尺寸与每行字数自适应字号、边距与分页；尺寸变化时自动重排，不截断文字。
- **图片渲染**：`golang.org/x/image/font/opentype` 栅格化系统字体，`image.RGBA` 绘制页面，纯 Go 输出 PNG。
- **PDF 导出**：复用同一套分页、字体、尺寸和水印参数，逐页栅格化后以 Flate 压缩的 RGB 图像流写入 PDF；默认按 150 DPI 映射页面尺寸。
- **水印**：把水印文字绘制到透明图层，按可调角度旋转后平铺到页面底层，字号与透明度均可配置。
- **字体支持**：扫描 macOS / Windows / Linux 系统字体目录，支持 `.ttf`、`.otf`、`.ttc`、`.otc`，解析后的字体文件会缓存复用。
- **实时预览**：设置变更后做防抖后台渲染，只生成当前页的缩放预览，避免阻塞 UI；导出时逐页后台生成并更新进度。
- **编码**：优先 UTF-8；失败时用 `x/text` 的 GB18030 解码，同时处理 UTF-16 BOM。

## 环境要求

- Go **1.27.3+**
- asdf 用户可使用项目内的 `.tool-versions`：

```bash
asdf install golang 1.27.3
asdf set golang 1.27.3
go version
```

如果使用 GoLand，请把项目 Go SDK / GOROOT 切换为 asdf 的 `1.27.3`，否则 MyGo 会触发工具链下载并可能因 `GOSUMDB=off` 失败。

## 运行

```bash
go mod tidy
go run .
```

## 构建桌面应用

```bash
go tool mygo build
```

产物位于 `build/`。也可以直接用 `go build` 编译当前平台的命令行可执行文件。
