package process

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

// buffers hold a pointer to a temp file, the last tailMax lines in a string array, the number of total lines, a partially read string, a mutex, and a callback function that reacts to new lines
type OutputBuffer struct {
	file    *os.File
	tail    []string
	tailMax int
	lines   int
	partial string
	m       sync.Mutex
	OnLine  func(string)
}

// creates a temp file for a new output buffer
func NewOutputBuffer(tailSize int) (*OutputBuffer, error) {
	f, err := os.CreateTemp("", "pragma-output-*")
	if err != nil {
		return nil, err
	}
	return &OutputBuffer{file: f, tail: make([]string, 0, tailSize), tailMax: tailSize}, nil
}

// writes to the buffer, returns number of byte written
func (o *OutputBuffer) Write(p []byte) (int, error) {
	o.m.Lock()
	defer o.m.Unlock()
	// writes data to the file
	if _, err := o.file.Write(p); err != nil {
		return 0, err
	}
	// combines the partial write with the data we got, then splits it into lines and makes the last one the new partial
	text := o.partial + string(p)
	lines := strings.Split(text, "\n")
	o.partial = lines[len(lines)-1]
	lines = lines[:len(lines)-1]
	// for all lines check if the tail is full and if so remove the first line, then append this line
	for _, line := range lines {
		o.lines++
		if len(o.tail) >= o.tailMax {
			copy(o.tail, o.tail[1:])
			o.tail[len(o.tail)-1] = line
		} else {
			o.tail = append(o.tail, line)
		}
		// if we have a defined callback, run it
		if o.OnLine != nil {
			o.OnLine(line)
		}
	}
	return len(p), nil
}

// deep copies the last n lines of the tail and returns it
func (o *OutputBuffer) Tail(n int) []string {
	o.m.Lock()
	defer o.m.Unlock()
	if n >= len(o.tail) {
		result := make([]string, len(o.tail))
		copy(result, o.tail)
		return result
	}
	result := make([]string, n)
	copy(result, o.tail[len(o.tail)-n:])
	return result
}

// number of lines getter
func (o *OutputBuffer) Lines() int {
	o.m.Lock()
	defer o.m.Unlock()
	return o.lines
}

// last line getter
func (o *OutputBuffer) LastLine() string {
	o.m.Lock()
	defer o.m.Unlock()
	if len(o.tail) == 0 {
		return ""
	}
	return o.tail[len(o.tail)-1]
}

// reads the whole file and returns the text
func (o *OutputBuffer) String() string {
	o.m.Lock()
	defer o.m.Unlock()
	o.file.Seek(0, 0)
	data, err := io.ReadAll(o.file)
	if err != nil {
		return ""
	}
	return string(data)
}

// find all lines that match a regex and return the array
func (o *OutputBuffer) Filter(pattern string) []string {
	o.m.Lock()
	defer o.m.Unlock()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	o.file.Seek(0, 0)
	var matches []string
	scanner := bufio.NewScanner(o.file)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			matches = append(matches, scanner.Text())
		}
	}
	if scanner.Err() != nil {
		return nil
	}
	return matches
}

// closes buffer by closing and deleting the file
func (o *OutputBuffer) Close() {
	o.m.Lock()
	defer o.m.Unlock()
	name := o.file.Name()
	o.file.Close()
	os.Remove(name)
}
