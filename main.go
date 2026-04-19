package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	fileBufferSize = 4 * 1024 * 1024 // 4 MB — much better for large video files
	logBufferSize  = 50
)

var copyWorkers = min(runtime.NumCPU(), 8)

var acceptedFormats = map[string]bool{
	".mkv": true, ".srt": true, ".avi": true, ".mov": true,
	".wmv": true, ".mp4": true, ".m4p": true, ".m4v": true,
	".mpg": true, ".mp2": true, ".mpeg": true, ".mpe": true,
	".mpv": true, ".m2v": true,
}

var (
	seasonRegex  = regexp.MustCompile(`(?i)(s\d+)`)
	episodeRegex = regexp.MustCompile(`(?i)(e\d+)`)
)

type TVFileInfo struct {
	FilePath     string
	FileName     string
	ShowName     string
	SeasonFolder string
	Episode      string
}

type Organizer struct {
	downloadPath string
	tvPath       string
	mappingFile  map[string]string
	logPath      string
	startTime    time.Time

	totalFiles     int64
	processedFiles int64
	copiedFiles    int64
	skippedFiles   int64
	errorFiles     int64

	logBuffer []string
	logMu     sync.Mutex
	logWriter *bufio.Writer
	logFile   *os.File

	emailLines []string
	emailMu    sync.Mutex
}

func newOrganizer(downloadPath, tvPath string) *Organizer {
	return &Organizer{
		downloadPath: downloadPath,
		tvPath:       tvPath,
		mappingFile:  make(map[string]string),
		startTime:    time.Now(),
	}
}

// ── Logging ──────────────────────────────────────────────────────────────────

func (o *Organizer) createLog(logDir string) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create log directory: %v\n", err)
		return
	}
	ts := time.Now().Unix()
	o.logPath = filepath.Join(logDir, fmt.Sprintf("tvfolderorganizer%d.log", ts))
	f, err := os.Create(o.logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create log file: %v\n", err)
		return
	}
	o.logFile = f
	o.logWriter = bufio.NewWriterSize(f, fileBufferSize)
}

func (o *Organizer) writeLog(msg string) {
	if o.logWriter == nil {
		return
	}
	o.logMu.Lock()
	defer o.logMu.Unlock()
	o.logBuffer = append(o.logBuffer, msg)
	if len(o.logBuffer) >= logBufferSize {
		o.flushLogBuffer()
	}
}

func (o *Organizer) flushLogBuffer() {
	if o.logWriter == nil || len(o.logBuffer) == 0 {
		return
	}
	for _, line := range o.logBuffer {
		_, _ = fmt.Fprintln(o.logWriter, line)
	}
	o.logBuffer = o.logBuffer[:0]
	_ = o.logWriter.Flush()
}

func (o *Organizer) closeLog() {
	if o.logWriter == nil {
		return
	}
	o.logMu.Lock()
	defer o.logMu.Unlock()
	o.flushLogBuffer()
	_ = o.logFile.Close()
}

// ── Progress ──────────────────────────────────────────────────────────────────

func (o *Organizer) showProgress(status string) {
	total := atomic.LoadInt64(&o.totalFiles)
	processed := atomic.LoadInt64(&o.processedFiles)
	copied := atomic.LoadInt64(&o.copiedFiles)
	skipped := atomic.LoadInt64(&o.skippedFiles)
	errors := atomic.LoadInt64(&o.errorFiles)

	elapsed := time.Since(o.startTime).Seconds()
	var pct, fps float64
	if total > 0 {
		pct = float64(processed) / float64(total) * 100
	}
	if elapsed > 0 {
		fps = float64(processed) / elapsed
	}
	var eta string
	if fps > 0 && processed < total {
		remaining := time.Duration(float64(total-processed)/fps) * time.Second
		eta = fmt.Sprintf("%02d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
	} else {
		eta = "00:00"
	}
	fmt.Printf("\r[%3.0f%%] %s: %d/%d files | Copied: %d | Skipped: %d | Errors: %d | Speed: %.2f files/sec | ETA: %s   ",
		pct, status, processed, total, copied, skipped, errors, fps, eta)
}

func (o *Organizer) showFinalStats() {
	elapsed := time.Since(o.startTime)
	processed := atomic.LoadInt64(&o.processedFiles)
	copied := atomic.LoadInt64(&o.copiedFiles)
	skipped := atomic.LoadInt64(&o.skippedFiles)
	errors := atomic.LoadInt64(&o.errorFiles)
	fps := 0.0
	if elapsed.Seconds() > 0 {
		fps = float64(processed) / elapsed.Seconds()
	}

	fmt.Println()
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("                    PROCESSING COMPLETE")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("Total Files Processed: %d\n", processed)
	fmt.Printf("  ✓ Copied:            %d\n", copied)
	fmt.Printf("  → Skipped:           %d\n", skipped)
	fmt.Printf("  ✗ Errors:            %d\n", errors)
	fmt.Printf("Total Time:            %02d:%02d:%02d\n",
		int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
	fmt.Printf("Average Speed:         %.2f files/sec\n", fps)
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()
}

// ── Mapping file ─────────────────────────────────────────────────────────────

func (o *Organizer) loadMappingFile(filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		o.writeLog(fmt.Sprintf("Error loading mapping file: %v", err))
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if k, v, ok := strings.Cut(line, "="); ok {
			o.mappingFile[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	o.writeLog(fmt.Sprintf("Loaded %d mapping entries", len(o.mappingFile)))
}

// ── File scanning ─────────────────────────────────────────────────────────────

func (o *Organizer) getVideoFiles() []string {
	var files []string
	_ = filepath.WalkDir(o.downloadPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if acceptedFormats[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// ── Parsing ───────────────────────────────────────────────────────────────────

func (o *Organizer) parseTVShowInfo(filePath string) *TVFileInfo {
	fileName := filepath.Base(filePath)

	seasonMatch := seasonRegex.FindStringIndex(fileName)
	episodeMatch := episodeRegex.FindStringIndex(fileName)

	if seasonMatch == nil || episodeMatch == nil {
		o.writeLog(fmt.Sprintf("Could not parse TV show info from: %s", fileName))
		return nil
	}

	rawSeason := fileName[seasonMatch[0]:seasonMatch[1]]
	rawEpisode := fileName[episodeMatch[0]:episodeMatch[1]]

	showName := fileName[:seasonMatch[0]]
	showName = strings.ReplaceAll(showName, ".", " ")
	showName = strings.ReplaceAll(showName, "'", " ")
	showName = strings.TrimSpace(strings.ToLower(showName))

	if mapped, ok := o.mappingFile[showName]; ok {
		showName = mapped
	}

	seasonFolder := "season " + strings.ToLower(rawSeason[1:])
	episode := strings.ToLower(rawEpisode[1:])

	info := &TVFileInfo{
		FilePath:     filePath,
		FileName:     fileName,
		ShowName:     showName,
		SeasonFolder: seasonFolder,
		Episode:      episode,
	}

	o.writeLog(fmt.Sprintf("Parsed: %s -> Show: %s, Season: %s, Episode: %s",
		fileName, showName, seasonFolder, episode))

	o.emailMu.Lock()
	o.emailLines = append(o.emailLines,
		fmt.Sprintf("SHOW: %s SEASON: %s EPISODE: %s", showName, seasonFolder, episode))
	o.emailMu.Unlock()

	return info
}

// ── Copy + hash ───────────────────────────────────────────────────────────────

// copyFileWithHash copies src to dst and returns the MD5 of the source data,
// computed for free as part of the single read pass — no second read needed.
func copyFileWithHash(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()

	h := md5.New()
	buf := make([]byte, fileBufferSize)
	if _, err = io.CopyBuffer(io.MultiWriter(out, h), in, buf); err != nil {
		return "", err
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%X", h.Sum(nil)), nil
}

func hashFile(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return "ERROR"
	}
	defer f.Close()
	h := md5.New()
	buf := make([]byte, fileBufferSize)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "ERROR"
	}
	return fmt.Sprintf("%X", h.Sum(nil))
}

func (o *Organizer) copyTVFile(info *TVFileInfo, deleteFiles bool) {
	destPath := filepath.Join(o.tvPath, info.ShowName, info.SeasonFolder, info.FileName)

	atomic.AddInt64(&o.processedFiles, 1)
	o.showProgress("Processing")

	if dstInfo, err := os.Stat(destPath); err == nil {
		if srcInfo, err := os.Stat(info.FilePath); err == nil && srcInfo.Size() == dstInfo.Size() {
			atomic.AddInt64(&o.skippedFiles, 1)
			o.writeLog(fmt.Sprintf("File already exists with same size, skipping: %s", info.FileName))
			if deleteFiles {
				_ = os.Remove(info.FilePath)
				o.writeLog(fmt.Sprintf("Deleted source file: %s", info.FilePath))
			}
			return
		}
	}

	srcHash, err := copyFileWithHash(info.FilePath, destPath)
	if err != nil {
		atomic.AddInt64(&o.errorFiles, 1)
		o.writeLog(fmt.Sprintf("ERROR copying file %s: %v", info.FileName, err))
		return
	}
	o.writeLog(fmt.Sprintf("Copied: %s -> %s", info.FilePath, destPath))

	dstHash := hashFile(destPath)
	if srcHash == dstHash {
		o.writeLog(fmt.Sprintf("MD5 verified: %s", srcHash))
		atomic.AddInt64(&o.copiedFiles, 1)
		if deleteFiles {
			_ = os.Remove(info.FilePath)
			o.writeLog(fmt.Sprintf("Deleted source file: %s", info.FilePath))
		}
	} else {
		atomic.AddInt64(&o.errorFiles, 1)
		o.writeLog(fmt.Sprintf("ERROR: MD5 mismatch for %s — src=%s dst=%s", info.FileName, srcHash, dstHash))
	}
}

// ── Processing ────────────────────────────────────────────────────────────────

func (o *Organizer) processFiles(videoFiles []string, deleteFiles bool) {
	// Parse all files in parallel
	parsedFiles := make([]*TVFileInfo, 0, len(videoFiles))
	var parseMu sync.Mutex
	var parseWg sync.WaitGroup
	for _, f := range videoFiles {
		parseWg.Add(1)
		go func(path string) {
			defer parseWg.Done()
			if info := o.parseTVShowInfo(path); info != nil {
				parseMu.Lock()
				parsedFiles = append(parsedFiles, info)
				parseMu.Unlock()
			}
		}(f)
	}
	parseWg.Wait()

	o.writeLog(fmt.Sprintf("Successfully parsed %d TV show files", len(parsedFiles)))

	// Create all destination directories upfront (fast, serial is fine)
	type groupKey struct{ show, season string }
	seen := make(map[groupKey]bool, len(parsedFiles))
	for _, info := range parsedFiles {
		k := groupKey{info.ShowName, info.SeasonFolder}
		if !seen[k] {
			seen[k] = true
			showSeasonPath := filepath.Join(o.tvPath, k.show, k.season)
			if err := os.MkdirAll(showSeasonPath, 0755); err != nil {
				o.writeLog(fmt.Sprintf("ERROR creating directory %s: %v", showSeasonPath, err))
			}
		}
	}

	// Copy files with a worker pool — I/O bound, so N workers in parallel
	work := make(chan *TVFileInfo, len(parsedFiles))
	for _, info := range parsedFiles {
		work <- info
	}
	close(work)

	workers := min(copyWorkers, len(parsedFiles))
	var copyWg sync.WaitGroup
	for i := 0; i < workers; i++ {
		copyWg.Add(1)
		go func() {
			defer copyWg.Done()
			for info := range work {
				o.copyTVFile(info, deleteFiles)
			}
		}()
	}
	copyWg.Wait()
}

// ── Cleanup ───────────────────────────────────────────────────────────────────

func (o *Organizer) cleanUpSource(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		o.writeLog(fmt.Sprintf("Directory does not exist: %s", dir))
		return
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !acceptedFormats[strings.ToLower(filepath.Ext(path))] {
			if removeErr := os.Remove(path); removeErr == nil {
				o.writeLog(fmt.Sprintf("Clean up deleted file: %s", path))
			}
		}
		return nil
	})
}

func (o *Organizer) deleteEmptyFolders(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(dirs[i])
		if err == nil && len(entries) == 0 {
			if removeErr := os.Remove(dirs[i]); removeErr == nil {
				o.writeLog(fmt.Sprintf("DELETEEMPTYFOLDER: Deleted folder %s", dirs[i]))
			}
		}
	}
}

func (o *Organizer) cleanupSourceDirectory() {
	o.writeLog("****CLEANUP EMPTY FOLDERS IN SOURCE************************")
	o.cleanUpSource(o.downloadPath)
	o.writeLog(fmt.Sprintf("Clean up task ran on %s", o.downloadPath))

	unknownPath := filepath.Join(o.tvPath, "UNKNOWN", "UNKNOWN")
	o.cleanUpSource(unknownPath)
	o.writeLog(fmt.Sprintf("Clean up task ran on %s", unknownPath))

	o.deleteEmptyFolders(o.downloadPath)
	o.writeLog(fmt.Sprintf("All Empty folders deleted from %s", o.downloadPath))
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "ERROR: Invalid arguments")
		fmt.Fprintln(os.Stderr, "Usage: tvorganizer <source_path> <destination_path> <delete_flag> [mapping_file]")
		fmt.Fprintln(os.Stderr, "  source_path      - Directory containing TV show files to organize")
		fmt.Fprintln(os.Stderr, "  destination_path - Directory to organize files into")
		fmt.Fprintln(os.Stderr, "  delete_flag      - true to delete source files after copy, false to keep them")
		fmt.Fprintln(os.Stderr, "  mapping_file     - Optional: Path to show name mapping file")
		os.Exit(1)
	}

	downloadPath := args[0]
	tvPath := args[1]
	deleteFlag := strings.ToLower(args[2])

	if _, err := os.Stat(downloadPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ERROR: Source directory does not exist: %s\n", downloadPath)
		os.Exit(1)
	}

	if deleteFlag != "true" && deleteFlag != "false" {
		fmt.Fprintf(os.Stderr, "ERROR: Invalid delete flag '%s'. Must be 'true' or 'false'\n", args[2])
		os.Exit(1)
	}
	deleteFiles := deleteFlag == "true"

	srcAbs, _ := filepath.Abs(downloadPath)
	dstAbs, _ := filepath.Abs(tvPath)
	if strings.EqualFold(srcAbs, dstAbs) {
		fmt.Fprintln(os.Stderr, "ERROR: Source and destination paths cannot be the same")
		os.Exit(1)
	}

	o := newOrganizer(downloadPath, tvPath)

	if err := os.MkdirAll(filepath.Join(tvPath, "logs"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create TV path: %v\n", err)
	}

	o.createLog(filepath.Join(tvPath, "logs"))
	defer o.closeLog()

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("          TV FOLDER ORGANIZER - Starting Process")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("Source Path:      %s\n", downloadPath)
	fmt.Printf("Destination Path: %s\n", tvPath)
	deleteStr := "NO"
	if deleteFiles {
		deleteStr = "YES"
	}
	fmt.Printf("Delete Originals: %s\n", deleteStr)
	fmt.Printf("Copy Workers:     %d\n", copyWorkers)
	fmt.Printf("Started:          %s\n", o.startTime.Format("2006-01-02 15:04:05"))
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	o.writeLog(time.Now().Format(time.RFC3339))
	o.writeLog("****PATHS/PARAMS BEING USED************************")
	o.writeLog("DOWNLOAD PATH:  " + downloadPath)
	o.writeLog("TV PATH: " + tvPath)
	o.writeLog(fmt.Sprintf("Delete Files: %v", deleteFiles))

	if len(args) >= 4 {
		if _, err := os.Stat(args[3]); err == nil {
			fmt.Printf("Loading mapping file: %s\n", args[3])
			o.writeLog("Mapping file PATH: " + args[3])
			o.loadMappingFile(args[3])
		}
	}

	fmt.Println("Scanning for video files...")
	videoFiles := o.getVideoFiles()
	filesDetected := len(videoFiles)
	atomic.StoreInt64(&o.totalFiles, int64(filesDetected))

	fmt.Printf("Found %d video file(s) to process\n\n", filesDetected)
	o.writeLog(fmt.Sprintf("****%d VIDEO FILES DETECTED************************", filesDetected))
	for _, f := range videoFiles {
		o.writeLog(f)
	}

	if filesDetected > 0 {
		o.writeLog("****PROCESSING AND ORGANIZING FILES************************")
		o.processFiles(videoFiles, deleteFiles)

		if deleteFiles {
			fmt.Println()
			fmt.Println("Cleaning up empty folders...")
			o.cleanupSourceDirectory()
		}

		o.showFinalStats()
	} else {
		fmt.Println("No video files found to process.")
		o.closeLog()
		_ = os.Remove(o.logPath)
	}
}
