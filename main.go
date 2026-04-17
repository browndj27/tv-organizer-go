package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Configuration constants (mirrors App.config defaults)
const (
	parallelFileThreshold       = 100
	parallelProcessingThreshold = 20
	fileBufferSize              = 65536
	logBufferSize               = 50
)

// Accepted video file extensions
var acceptedFormats = map[string]bool{
	".mkv": true, ".srt": true, ".avi": true, ".mov": true,
	".wmv": true, ".mp4": true, ".m4p": true, ".m4v": true,
	".mpg": true, ".mp2": true, ".mpeg": true, ".mpe": true,
	".mpv": true, ".m2v": true,
}

// Compiled regex patterns
var (
	seasonRegex  = regexp.MustCompile(`(?i)(s\d+)`)
	episodeRegex = regexp.MustCompile(`(?i)(e\d+)`)
)

// TVFileInfo holds parsed information about a TV show file
type TVFileInfo struct {
	FilePath     string
	FileName     string
	ShowName     string
	SeasonFolder string
	Episode      string
}

// Organizer holds all state for the run
type Organizer struct {
	downloadPath string
	tvPath       string
	mappingFile  map[string]string
	logPath      string
	startTime    time.Time

	// Progress counters (atomic for concurrent access)
	totalFiles    int64
	processedFiles int64
	copiedFiles   int64
	skippedFiles  int64
	errorFiles    int64

	// MD5 cache
	md5Cache   map[string]string
	md5CacheMu sync.Mutex

	// Buffered logging
	logBuffer []string
	logMu     sync.Mutex
	logWriter *bufio.Writer
	logFile   *os.File

	// Email message (kept for parity; could be extended later)
	emailLines []string
	emailMu    sync.Mutex
}

func newOrganizer(downloadPath, tvPath string) *Organizer {
	return &Organizer{
		downloadPath: downloadPath,
		tvPath:       tvPath,
		mappingFile:  make(map[string]string),
		md5Cache:     make(map[string]string),
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

// flushLogBuffer must be called with o.logMu held.
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
		if idx := strings.Index(line, "="); idx != -1 {
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			val := strings.TrimSpace(line[idx+1:])
			o.mappingFile[key] = val
		}
	}
	o.writeLog(fmt.Sprintf("Loaded %d mapping entries", len(o.mappingFile)))
}

// ── File scanning ─────────────────────────────────────────────────────────────

func (o *Organizer) getVideoFiles() []string {
	var files []string
	_ = filepath.Walk(o.downloadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if acceptedFormats[ext] {
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

	rawSeason := fileName[seasonMatch[0]:seasonMatch[1]]  // e.g. "s02"
	rawEpisode := fileName[episodeMatch[0]:episodeMatch[1]] // e.g. "e05"

	// Show name = everything before the season token
	showName := fileName[:seasonMatch[0]]
	showName = strings.ReplaceAll(showName, ".", " ")
	showName = strings.ReplaceAll(showName, "'", " ")
	showName = strings.TrimSpace(strings.ToLower(showName))

	// Apply mapping
	if mapped, ok := o.mappingFile[showName]; ok {
		showName = mapped
	}

	seasonFolder := "season " + strings.ToLower(rawSeason[1:]) // strip leading 's'
	episode := strings.ToLower(rawEpisode[1:])                 // strip leading 'e'

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

// ── MD5 ───────────────────────────────────────────────────────────────────────

func (o *Organizer) getCachedMD5(filePath string) string {
	o.md5CacheMu.Lock()
	if cached, ok := o.md5Cache[filePath]; ok {
		o.md5CacheMu.Unlock()
		return cached
	}
	o.md5CacheMu.Unlock()

	hash := calculateMD5(filePath)

	o.md5CacheMu.Lock()
	o.md5Cache[filePath] = hash
	o.md5CacheMu.Unlock()

	return hash
}

func calculateMD5(filePath string) string {
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

func (o *Organizer) verifyFileCopy(src, dst string) bool {
	srcInfo, err := os.Stat(src)
	if err != nil {
		o.writeLog(fmt.Sprintf("Error stating source file: %v", err))
		return false
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		o.writeLog(fmt.Sprintf("Error stating dest file: %v", err))
		return false
	}
	if srcInfo.Size() != dstInfo.Size() {
		o.writeLog(fmt.Sprintf("Size mismatch: Source=%d, Dest=%d", srcInfo.Size(), dstInfo.Size()))
		return false
	}
	srcMD5 := o.getCachedMD5(src)
	dstMD5 := o.getCachedMD5(dst)
	if srcMD5 == dstMD5 {
		o.writeLog(fmt.Sprintf("MD5 verified: %s", srcMD5))
		return true
	}
	o.writeLog(fmt.Sprintf("MD5 mismatch: Source=%s, Dest=%s", srcMD5, dstMD5))
	return false
}

// ── Copy ──────────────────────────────────────────────────────────────────────

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, fileBufferSize)
	if _, err = io.CopyBuffer(out, in, buf); err != nil {
		return err
	}
	return out.Sync()
}

func (o *Organizer) copyTVFile(info *TVFileInfo, deleteFiles bool) {
	destPath := filepath.Join(o.tvPath, info.ShowName, info.SeasonFolder, info.FileName)

	atomic.AddInt64(&o.processedFiles, 1)
	o.showProgress("Processing")

	// Skip if destination exists with same size
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

	if err := copyFile(info.FilePath, destPath); err != nil {
		atomic.AddInt64(&o.errorFiles, 1)
		o.writeLog(fmt.Sprintf("ERROR copying file %s: %v", info.FileName, err))
		return
	}
	o.writeLog(fmt.Sprintf("Copied: %s -> %s", info.FilePath, destPath))

	if o.verifyFileCopy(info.FilePath, destPath) {
		atomic.AddInt64(&o.copiedFiles, 1)
		o.writeLog(fmt.Sprintf("File integrity verified for %s", info.FileName))
		if deleteFiles {
			_ = os.Remove(info.FilePath)
			o.writeLog(fmt.Sprintf("Deleted source file: %s", info.FilePath))
		}
	} else {
		atomic.AddInt64(&o.errorFiles, 1)
		o.writeLog(fmt.Sprintf("ERROR: File copy verification failed for %s", info.FileName))
	}
}

// ── Processing ────────────────────────────────────────────────────────────────

func (o *Organizer) processFiles(videoFiles []string, deleteFiles bool) {
	// Parse all files (parallel if above threshold)
	parsedFiles := make([]*TVFileInfo, 0, len(videoFiles))

	if len(videoFiles) > parallelProcessingThreshold {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, f := range videoFiles {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				if info := o.parseTVShowInfo(path); info != nil {
					mu.Lock()
					parsedFiles = append(parsedFiles, info)
					mu.Unlock()
				}
			}(f)
		}
		wg.Wait()
	} else {
		for _, f := range videoFiles {
			if info := o.parseTVShowInfo(f); info != nil {
				parsedFiles = append(parsedFiles, info)
			}
		}
	}

	o.writeLog(fmt.Sprintf("Successfully parsed %d TV show files", len(parsedFiles)))

	// Group by show/season and create directories
	type groupKey struct{ show, season string }
	groups := make(map[groupKey][]*TVFileInfo)
	for _, info := range parsedFiles {
		k := groupKey{info.ShowName, info.SeasonFolder}
		groups[k] = append(groups[k], info)
	}

	for k, infos := range groups {
		showSeasonPath := filepath.Join(o.tvPath, k.show, k.season)
		if err := os.MkdirAll(showSeasonPath, 0755); err != nil {
			o.writeLog(fmt.Sprintf("ERROR creating directory %s: %v", showSeasonPath, err))
			continue
		}
		for _, info := range infos {
			o.copyTVFile(info, deleteFiles)
		}
	}
}

// ── Cleanup ───────────────────────────────────────────────────────────────────

// cleanUpSource deletes non-video files under a directory tree.
func (o *Organizer) cleanUpSource(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		o.writeLog(fmt.Sprintf("Directory does not exist: %s", dir))
		return
	}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !acceptedFormats[ext] {
			if removeErr := os.Remove(path); removeErr == nil {
				o.writeLog(fmt.Sprintf("Clean up deleted file: %s", path))
			}
		}
		return nil
	})
}

// deleteEmptyFolders removes all empty directories under root (bottom-up).
func (o *Organizer) deleteEmptyFolders(root string) {
	// Collect all subdirectory paths
	var dirs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})

	// Walk in reverse order so deepest dirs are handled first
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

	// Ensure destination and logs directories exist
	if err := os.MkdirAll(filepath.Join(tvPath, "logs"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create TV path: %v\n", err)
	}

	o.createLog(filepath.Join(tvPath, "logs"))
	defer o.closeLog()

	// Print startup banner
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
	fmt.Printf("Started:          %s\n", o.startTime.Format("2006-01-02 15:04:05"))
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	o.writeLog(time.Now().Format(time.RFC3339))
	o.writeLog("****PATHS/PARAMS BEING USED************************")
	o.writeLog("DOWNLOAD PATH:  " + downloadPath)
	o.writeLog("TV PATH: " + tvPath)
	o.writeLog(fmt.Sprintf("Delete Files: %v", deleteFiles))

	// Load optional mapping file
	if len(args) >= 4 {
		if _, err := os.Stat(args[3]); err == nil {
			fmt.Printf("Loading mapping file: %s\n", args[3])
			o.writeLog("Mapping file PATH: " + args[3])
			o.loadMappingFile(args[3])
		}
	}

	// Scan for video files
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
		// Delete empty log file
		o.closeLog()
		_ = os.Remove(o.logPath)
	}
}
