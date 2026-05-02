package config

import "runtime"

const (
	FileBufferSize = 4 * 1024 * 1024
	LogBufferSize  = 50
)

var CopyWorkers = min(runtime.NumCPU(), 8)

var AcceptedFormats = map[string]bool{
	".mkv": true, ".srt": true, ".avi": true, ".mov": true,
	".wmv": true, ".mp4": true, ".m4p": true, ".m4v": true,
	".mpg": true, ".mp2": true, ".mpeg": true, ".mpe": true,
	".mpv": true, ".m2v": true,
}
