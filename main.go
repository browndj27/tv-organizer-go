package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tvorganizer/internal/config"
	"tvorganizer/internal/logger"
	"tvorganizer/internal/organizer"
)

func main() {
	args := os.Args[1:]

	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "ERROR: Invalid arguments")
		fmt.Fprintln(os.Stderr, "Usage: tvorganizer <source_path> <destination_path> <delete_flag> [mapping_file]")
		fmt.Fprintln(os.Stderr, "  source_path      - Directory containing TV show files to organize")
		fmt.Fprintln(os.Stderr, "  destination_path - Directory to organize files into")
		fmt.Fprintln(os.Stderr, "  delete_flag      - true to delete source files after copy, false to keep them")
		fmt.Fprintln(os.Stderr, "  mapping_file     - Optional: path to show name mapping file (key=value)")
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
		fmt.Fprintf(os.Stderr, "ERROR: Invalid delete flag %q. Must be 'true' or 'false'\n", args[2])
		os.Exit(1)
	}
	deleteFiles := deleteFlag == "true"

	srcAbs, _ := filepath.Abs(downloadPath)
	dstAbs, _ := filepath.Abs(tvPath)
	if strings.EqualFold(srcAbs, dstAbs) {
		fmt.Fprintln(os.Stderr, "ERROR: Source and destination paths cannot be the same")
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Join(tvPath, "logs"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create destination path: %v\n", err)
	}

	log, logErr := logger.New(filepath.Join(tvPath, "logs"))
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: logging unavailable: %v\n", logErr)
	}

	removeLogOnExit := false
	defer func() {
		log.Close()
		if removeLogOnExit {
			log.Remove()
		}
	}()

	startTime := time.Now()
	deleteStr := "NO"
	if deleteFiles {
		deleteStr = "YES"
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("          TV FOLDER ORGANIZER - Starting Process")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("Source Path:      %s\n", downloadPath)
	fmt.Printf("Destination Path: %s\n", tvPath)
	fmt.Printf("Delete Originals: %s\n", deleteStr)
	fmt.Printf("Copy Workers:     %d\n", config.CopyWorkers)
	fmt.Printf("Started:          %s\n", startTime.Format("2006-01-02 15:04:05"))
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	log.Write(startTime.Format(time.RFC3339))
	log.Write("****PATHS/PARAMS BEING USED************************")
	log.Write("DOWNLOAD PATH:  " + downloadPath)
	log.Write("TV PATH: " + tvPath)
	log.Write(fmt.Sprintf("Delete Files: %v", deleteFiles))

	o := organizer.New(downloadPath, tvPath, log)

	if len(args) >= 4 {
		if _, err := os.Stat(args[3]); err == nil {
			fmt.Printf("Loading mapping file: %s\n", args[3])
			log.Write("Mapping file PATH: " + args[3])
			o.LoadMapping(args[3])
		}
	}

	fmt.Println("Scanning for video files...")
	found := o.Run(deleteFiles)
	if found == 0 {
		removeLogOnExit = true
	}
}
