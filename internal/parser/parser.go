package parser

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

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

// LoadMappingFile reads a key=value file and returns a lowercase-keyed map for
// correcting show names that don't match the desired folder name.
func LoadMappingFile(filePath string) (map[string]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	mapping := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if k, v, ok := strings.Cut(scanner.Text(), "="); ok {
			mapping[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return mapping, scanner.Err()
}

// ParseTVShowInfo extracts show name, season, and episode from a filename.
// Returns nil if the filename does not contain recognisable S##E## markers.
func ParseTVShowInfo(filePath string, mapping map[string]string) *TVFileInfo {
	fileName := filepath.Base(filePath)

	seasonMatch := seasonRegex.FindStringIndex(fileName)
	episodeMatch := episodeRegex.FindStringIndex(fileName)
	if seasonMatch == nil || episodeMatch == nil {
		return nil
	}

	rawSeason := fileName[seasonMatch[0]:seasonMatch[1]]
	rawEpisode := fileName[episodeMatch[0]:episodeMatch[1]]

	showName := fileName[:seasonMatch[0]]
	showName = strings.ReplaceAll(showName, ".", " ")
	showName = strings.ReplaceAll(showName, "'", " ")
	showName = strings.TrimSpace(strings.ToLower(showName))
	showName = strings.TrimRightFunc(showName, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	if mapped, ok := mapping[showName]; ok {
		showName = mapped
	}

	return &TVFileInfo{
		FilePath:     filePath,
		FileName:     fileName,
		ShowName:     showName,
		SeasonFolder: "season " + strings.ToLower(rawSeason[1:]),
		Episode:      strings.ToLower(rawEpisode[1:]),
	}
}
