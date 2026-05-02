package organizer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tvorganizer/internal/config"
	"tvorganizer/internal/fileutil"
	"tvorganizer/internal/logger"
	"tvorganizer/internal/parser"
)

type Organizer struct {
	downloadPath string
	tvPath       string
	mapping      map[string]string
	log          *logger.Logger
	startTime    time.Time

	totalFiles     int64
	processedFiles int64
	copiedFiles    int64
	skippedFiles   int64
	errorFiles     int64
}

func New(downloadPath, tvPath string, log *logger.Logger) *Organizer {
	return &Organizer{
		downloadPath: downloadPath,
		tvPath:       tvPath,
		mapping:      make(map[string]string),
		log:          log,
		startTime:    time.Now(),
	}
}

func (o *Organizer) LoadMapping(filePath string) {
	m, err := parser.LoadMappingFile(filePath)
	if err != nil {
		o.log.Write(fmt.Sprintf("Error loading mapping file: %v", err))
		return
	}
	o.mapping = m
	o.log.Write(fmt.Sprintf("Loaded %d mapping entries", len(m)))
}

// Run scans, organizes, and optionally deletes source files.
// Returns the number of video files found.
func (o *Organizer) Run(deleteFiles bool) int {
	videoFiles := fileutil.WalkVideoFiles(o.downloadPath, config.AcceptedFormats)
	filesDetected := len(videoFiles)
	atomic.StoreInt64(&o.totalFiles, int64(filesDetected))

	fmt.Printf("Found %d video file(s) to process\n\n", filesDetected)
	o.log.Write(fmt.Sprintf("****%d VIDEO FILES DETECTED************************", filesDetected))
	for _, f := range videoFiles {
		o.log.Write(f)
	}

	if filesDetected == 0 {
		fmt.Println("No video files found to process.")
		return 0
	}

	o.log.Write("****PROCESSING AND ORGANIZING FILES************************")
	o.processFiles(videoFiles, deleteFiles)

	if deleteFiles {
		fmt.Println()
		fmt.Println("Cleaning up empty folders...")
		o.cleanupSourceDirectory()
	}

	o.showFinalStats()
	return filesDetected
}

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
	eta := "00:00"
	if fps > 0 && processed < total {
		remaining := time.Duration(float64(total-processed)/fps) * time.Second
		eta = fmt.Sprintf("%02d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
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

func (o *Organizer) copyTVFile(info *parser.TVFileInfo, deleteFiles bool) {
	destPath := filepath.Join(o.tvPath, info.ShowName, info.SeasonFolder, info.FileName)
	atomic.AddInt64(&o.processedFiles, 1)

	if dstInfo, err := os.Stat(destPath); err == nil {
		if srcInfo, err := os.Stat(info.FilePath); err == nil && srcInfo.Size() == dstInfo.Size() {
			atomic.AddInt64(&o.skippedFiles, 1)
			o.log.Write(fmt.Sprintf("File already exists with same size, skipping: %s", info.FileName))
			if deleteFiles {
				_ = os.Remove(info.FilePath)
				o.log.Write(fmt.Sprintf("Deleted source file: %s", info.FilePath))
			}
			return
		}
	}

	dataHash, err := fileutil.CopyWithHash(info.FilePath, destPath)
	if err != nil {
		atomic.AddInt64(&o.errorFiles, 1)
		o.log.Write(fmt.Sprintf("ERROR copying file %s: %v", info.FileName, err))
		return
	}
	o.log.Write(fmt.Sprintf("Copied: %s -> %s (MD5: %s)", info.FilePath, destPath, dataHash))
	atomic.AddInt64(&o.copiedFiles, 1)
	if deleteFiles {
		_ = os.Remove(info.FilePath)
		o.log.Write(fmt.Sprintf("Deleted source file: %s", info.FilePath))
	}
}

func (o *Organizer) processFiles(videoFiles []string, deleteFiles bool) {
	// Parse stage — bounded worker pool avoids spawning thousands of goroutines
	parsedFiles := make([]*parser.TVFileInfo, 0, len(videoFiles))
	var parseMu sync.Mutex
	var parseWg sync.WaitGroup
	parseWork := make(chan string, len(videoFiles))
	for _, f := range videoFiles {
		parseWork <- f
	}
	close(parseWork)

	for i := 0; i < min(runtime.NumCPU(), len(videoFiles)); i++ {
		parseWg.Add(1)
		go func() {
			defer parseWg.Done()
			for path := range parseWork {
				info := parser.ParseTVShowInfo(path, o.mapping)
				if info == nil {
					o.log.Write(fmt.Sprintf("Could not parse TV show info from: %s", filepath.Base(path)))
					continue
				}
				o.log.Write(fmt.Sprintf("Parsed: %s -> Show: %s, Season: %s, Episode: %s",
					info.FileName, info.ShowName, info.SeasonFolder, info.Episode))
				parseMu.Lock()
				parsedFiles = append(parsedFiles, info)
				parseMu.Unlock()
			}
		}()
	}
	parseWg.Wait()
	o.log.Write(fmt.Sprintf("Successfully parsed %d TV show files", len(parsedFiles)))

	// Create all destination directories upfront — deduplicated, serial is fine
	type dirKey struct{ show, season string }
	seen := make(map[dirKey]bool, len(parsedFiles))
	for _, info := range parsedFiles {
		k := dirKey{info.ShowName, info.SeasonFolder}
		if !seen[k] {
			seen[k] = true
			dest := filepath.Join(o.tvPath, k.show, k.season)
			if err := os.MkdirAll(dest, 0755); err != nil {
				o.log.Write(fmt.Sprintf("ERROR creating directory %s: %v", dest, err))
			}
		}
	}

	// Copy stage — I/O-bound, run N workers in parallel
	work := make(chan *parser.TVFileInfo, len(parsedFiles))
	for _, info := range parsedFiles {
		work <- info
	}
	close(work)

	// Single goroutine owns progress output every 200ms — avoids fmt.Printf races
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				o.showProgress("Processing")
			case <-stop:
				return
			}
		}
	}()

	workers := min(config.CopyWorkers, len(parsedFiles))
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
	close(stop)
	o.showProgress("Complete")
}

func (o *Organizer) cleanUpSource(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		o.log.Write(fmt.Sprintf("Directory does not exist: %s", dir))
		return
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !config.AcceptedFormats[strings.ToLower(filepath.Ext(path))] {
			if removeErr := os.Remove(path); removeErr == nil {
				o.log.Write(fmt.Sprintf("Clean up deleted file: %s", path))
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
	// Walk in reverse so deepest directories are removed first
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(dirs[i])
		if err == nil && len(entries) == 0 {
			if removeErr := os.Remove(dirs[i]); removeErr == nil {
				o.log.Write(fmt.Sprintf("DELETEEMPTYFOLDER: Deleted folder %s", dirs[i]))
			}
		}
	}
}

func (o *Organizer) cleanupSourceDirectory() {
	o.log.Write("****CLEANUP EMPTY FOLDERS IN SOURCE************************")
	o.cleanUpSource(o.downloadPath)
	o.log.Write(fmt.Sprintf("Clean up task ran on %s", o.downloadPath))

	unknownPath := filepath.Join(o.tvPath, "UNKNOWN", "UNKNOWN")
	o.cleanUpSource(unknownPath)
	o.log.Write(fmt.Sprintf("Clean up task ran on %s", unknownPath))

	o.deleteEmptyFolders(o.downloadPath)
	o.log.Write(fmt.Sprintf("All Empty folders deleted from %s", o.downloadPath))
}
