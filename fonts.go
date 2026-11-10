package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/image/font/sfnt"
)

type faceInfo struct {
	Family string
	Style  string
	Index  int
}

type fontOption struct {
	Label string
	Ref   FontRef
}

func detectSystemFont() (FontRef, bool) {
	for _, path := range systemFontCandidates() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if faces, err := inspectFontFile(path); err == nil && len(faces) > 0 {
				index := pickRegularFace(faces)
				return FontRef{Family: faces[index].Family, Path: path, Index: index}, true
			}
		}
	}
	for _, root := range systemFontDirs() {
		var found FontRef
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !isFontFile(path) || skipFontFile(path) {
				return nil
			}
			faces, err := inspectFontFile(path)
			if err != nil || len(faces) == 0 {
				return nil
			}
			index := pickRegularFace(faces)
			found = FontRef{Family: faces[index].Family, Path: path, Index: index}
			return filepath.SkipAll
		})
		if found.Path != "" {
			return found, true
		}
	}
	return FontRef{}, false
}

func scanFontOptions(defaultRef FontRef, defaultLabel string) []fontOption {
	type candidate struct {
		ref   FontRef
		score int
	}
	families := make(map[string]candidate)
	for _, root := range systemFontDirs() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !isFontFile(path) || skipFontFile(path) {
				return nil
			}
			faces, err := inspectFontFile(path)
			if err != nil {
				return nil
			}
			for _, face := range faces {
				family := strings.TrimSpace(face.Family)
				if family == "" {
					continue
				}
				ref := FontRef{Family: family, Path: path, Index: face.Index}
				if sameFont(ref, defaultRef) {
					continue
				}
				score := fontStyleScore(face.Style)
				if old, ok := families[family]; !ok || score < old.score {
					families[family] = candidate{ref: ref, score: score}
				}
			}
			return nil
		})
	}

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	options := make([]fontOption, 0, len(names)+1)
	if defaultRef.Path != "" {
		options = append(options, fontOption{Label: defaultLabel, Ref: defaultRef})
	}
	for _, name := range names {
		options = append(options, fontOption{Label: name, Ref: families[name].ref})
	}
	return options
}

func inspectFontFile(path string) ([]faceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	collection, err := sfnt.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	faces := make([]faceInfo, 0, collection.NumFonts())
	for i := 0; i < collection.NumFonts(); i++ {
		f, err := collection.Font(i)
		if err != nil {
			continue
		}
		family := fontName(f, sfnt.NameIDTypographicFamily)
		if family == "" {
			family = fontName(f, sfnt.NameIDFamily)
		}
		if family == "" {
			family = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		style := fontName(f, sfnt.NameIDTypographicSubfamily)
		if style == "" {
			style = fontName(f, sfnt.NameIDSubfamily)
		}
		faces = append(faces, faceInfo{Family: family, Style: style, Index: i})
	}
	if len(faces) == 0 {
		return nil, errors.New("字体不包含可用字形")
	}
	return faces, nil
}

func fontName(f *sfnt.Font, id sfnt.NameID) string {
	name, err := f.Name(nil, id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(name, "\x00", ""))
}

func pickRegularFace(faces []faceInfo) int {
	best := 0
	bestScore := fontStyleScore(faces[0].Style)
	for i := 1; i < len(faces); i++ {
		score := fontStyleScore(faces[i].Style)
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func fontStyleScore(style string) int {
	s := strings.ToLower(style)
	switch {
	case strings.Contains(s, "regular"), strings.Contains(s, "book"), strings.Contains(s, "roman"),
		strings.Contains(s, "normal"), strings.Contains(s, " w3"), strings.HasSuffix(s, "w3"):
		return 0
	case strings.Contains(s, "medium"):
		return 1
	case strings.Contains(s, "light"), strings.Contains(s, "thin"), strings.Contains(s, "hairline"):
		return 2
	case strings.Contains(s, "semibold"), strings.Contains(s, "demibold"):
		return 3
	case strings.Contains(s, "italic"), strings.Contains(s, "oblique"):
		return 5
	case strings.Contains(s, "bold"), strings.Contains(s, "black"), strings.Contains(s, "heavy"),
		strings.Contains(s, "ultra"):
		return 4
	default:
		return 2
	}
}

func sameFont(a, b FontRef) bool {
	return b.Path != "" && a.Path == b.Path && a.Index == b.Index
}

func isFontFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

func skipFontFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	for _, word := range []string{"emoji", "symbol", "braille", "lastresort", "webdings", "wingdings", "marlett"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func systemFontCandidates() []string {
	home, _ := os.UserHomeDir()
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		paths = []string{
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/STHeiti Medium.ttc",
			"/System/Library/Fonts/Supplemental/Songti.ttc",
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/Library/Fonts/Arial Unicode.ttf",
			filepath.Join(home, "Library/Fonts/Arial Unicode.ttf"),
		}
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		fonts := filepath.Join(windir, "Fonts")
		paths = []string{
			filepath.Join(fonts, "msyh.ttc"),
			filepath.Join(fonts, "msyh.ttf"),
			filepath.Join(fonts, "simhei.ttf"),
			filepath.Join(fonts, "simsun.ttc"),
			filepath.Join(fonts, "Deng.ttf"),
			filepath.Join(fonts, "arial.ttf"),
		}
	default:
		paths = []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf",
			"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/TTF/DejaVuSans.ttf",
			filepath.Join(home, ".local/share/fonts/NotoSansCJK-Regular.ttc"),
		}
	}
	return paths
}

func systemFontDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{
			"/System/Library/Fonts",
			"/System/Library/Fonts/Supplemental",
			"/Library/Fonts",
			filepath.Join(home, "Library/Fonts"),
		}
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		dirs = []string{filepath.Join(windir, "Fonts")}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Microsoft/Windows/Fonts"))
		}
	default:
		dirs = []string{
			"/usr/share/fonts",
			"/usr/local/share/fonts",
			filepath.Join(home, ".fonts"),
			filepath.Join(home, ".local/share/fonts"),
		}
	}
	return dirs
}
