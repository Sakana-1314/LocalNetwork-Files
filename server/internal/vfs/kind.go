package vfs

import (
	"mime"
	"path"
	"strings"
)

const (
	KindDir     = "dir"
	KindImage   = "image"
	KindVideo   = "video"
	KindAudio   = "audio"
	KindText    = "text"
	KindPDF     = "pdf"
	KindArchive = "archive"
	KindOther   = "other"
)

var extKinds = map[string]string{}

func init() {
	reg := func(kind string, exts ...string) {
		for _, e := range exts {
			extKinds[e] = kind
		}
	}
	reg(KindImage, "jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff", "svg", "ico", "heic", "heif", "avif",
		"jfif", "cr2", "cr3", "nef", "arw", "dng", "raf", "orf", "rw2")
	reg(KindVideo, "mp4", "m4v", "mkv", "webm", "mov", "avi", "wmv", "flv", "ts", "m2ts", "mts", "mpg", "mpeg",
		"3gp", "rmvb", "rm", "vob", "ogv", "asf", "f4v")
	reg(KindAudio, "mp3", "flac", "wav", "aac", "m4a", "ogg", "oga", "opus", "wma", "ape", "alac", "aiff", "aif",
		"dsf", "dff", "mka", "wv", "tta", "mid", "midi", "amr")
	reg(KindText, "txt", "md", "markdown", "log", "json", "yaml", "yml", "toml", "ini", "conf", "cfg", "xml", "csv",
		"tsv", "html", "htm", "css", "js", "mjs", "cjs", "ts", "tsx", "jsx", "vue", "go", "py", "rb", "rs", "java",
		"kt", "c", "h", "cc", "cpp", "hpp", "cs", "php", "sh", "bash", "zsh", "fish", "ps1", "bat", "cmd", "sql",
		"srt", "ass", "ssa", "vtt", "lrc", "nfo", "cue", "env", "gitignore", "dockerfile", "makefile", "properties",
		"swift", "dart", "lua", "pl", "r", "scala", "gradle")
	reg(KindPDF, "pdf")
	reg(KindArchive, "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "iso", "dmg", "cab", "lz", "lzma")
	// ".ts" is far more often an MPEG transport stream on a media server than TypeScript.
	extKinds["ts"] = KindVideo
}

// Ext returns the lower-case extension without dot.
func Ext(name string) string {
	e := path.Ext(name)
	if e == "" {
		return strings.ToLower(name)
	}
	return strings.ToLower(e[1:])
}

// KindOf classifies a file by its name.
func KindOf(name string, isDir bool) string {
	if isDir {
		return KindDir
	}
	if k, ok := extKinds[Ext(name)]; ok {
		return k
	}
	return KindOther
}

var extraMime = map[string]string{
	"mkv": "video/x-matroska", "flac": "audio/flac", "m4a": "audio/mp4", "opus": "audio/ogg", "ape": "audio/ape",
	"md": "text/markdown", "ts": "video/mp2t", "m2ts": "video/mp2t", "heic": "image/heic", "avif": "image/avif",
	"webp": "image/webp", "srt": "text/plain", "ass": "text/plain", "vtt": "text/vtt", "yaml": "text/yaml",
	"yml": "text/yaml", "log": "text/plain", "wma": "audio/x-ms-wma", "wmv": "video/x-ms-wmv",
}

// MimeOf guesses a MIME type from the file name.
func MimeOf(name string, isDir bool) string {
	if isDir {
		return "inode/directory"
	}
	ext := Ext(name)
	if m, ok := extraMime[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension("." + ext); m != "" {
		return m
	}
	if KindOf(name, false) == KindText {
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// Thumbable reports whether a preview image can be generated for the file.
func Thumbable(name string, isDir bool, ffmpeg bool) bool {
	switch KindOf(name, isDir) {
	case KindImage:
		switch Ext(name) {
		case "jpg", "jpeg", "jfif", "png", "gif", "webp", "bmp", "tif", "tiff":
			return true
		case "svg", "ico":
			return false
		}
		return ffmpeg
	case KindVideo, KindAudio:
		return ffmpeg
	}
	return false
}
