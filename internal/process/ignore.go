package process

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

var ignoredPatterns = []string{
	".env",
	".agent/config.toml",
	".agent/pragma.db",
	".agent/pragma.log",
}

func init() {
	// attempts to read from .agentignore
	data, err := os.ReadFile(".agentignore")
	if err != nil {
		return
	}
	// loops through each line, if it's a non-empty non-comment add it to the ignoredPatterns
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			ignoredPatterns = append(ignoredPatterns, line)
		}
	}
}

// returns a csv of the patterns to ignore
func GetBlocklist() string {
	return strings.Join(ignoredPatterns, ",")
}

// checks if a path matches an ignoredPattern
func IsIgnored(path string) bool {
	// loops through ignoredPatterns array
	for _, pattern := range ignoredPatterns {
		if strings.Contains(pattern, "/") {
			// the pattern has a path separator, try to match the whole thing
			if matched, _ := filepath.Match(pattern, path); matched {
				return true
			}
		} else {
			// no path, block every file with the name
			if baseMatched, _ := filepath.Match(pattern, filepath.Base(path)); baseMatched {
				return true
			}
		}
	}
	return false
}

// checks if an input string contains an ignored path
func CheckInput(input string) bool {
	for word := range strings.FieldsSeq(input) {
		word = strings.Trim(word, "\"'`,;:()[]{}|")
		if IsIgnored(word) {
			return false
		}
	}
	return true
}

// removes all lines containing ignored patterns from output string and return
func ScrubOutput(output string) string {
	var clean []string
	for line := range strings.SplitSeq(output, "\n") {
		// if the line has no ignored pattern, append it to the cleaned output
		if !lineContainsIgnored(line) {
			clean = append(clean, line)
		}
	}
	return strings.Join(clean, "\n")
}

// strips wildcard characters for substring matching
func literalCore(pattern string) string {
	s := strings.NewReplacer("*", "", "?", "", "[", "", "]", "").Replace(pattern)
	return s
}

// checks if a line contains an ignored pattern
func lineContainsIgnored(line string) bool {
	for _, pattern := range ignoredPatterns {
		// fast path checks if literal core is even contained in the line
		core := literalCore(pattern)
		if core == "" || !strings.Contains(line, core) {
			continue
		}
		// if it is, loop through all the words, remove nonsense characters, same matching logic as above
		for word := range strings.FieldsSeq(line) {
			word = strings.Trim(word, "\"'`,;:()[]{}|")
			if strings.Contains(pattern, "/") {
				// the pattern has a path separator, try to match the whole thing
				if matched, _ := filepath.Match(pattern, word); matched {
					return true
				}
			} else {
				// no path, block every file with the name
				if baseMatched, _ := filepath.Match(pattern, filepath.Base(word)); baseMatched {
					return true
				}
			}
		}
	}
	return false
}
