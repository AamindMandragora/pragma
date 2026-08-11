package process

import (
	"io"
	"os"
	"strings"
	"sync"
)

// buffers hold a pointer to a temp file, a partially read string, a mutex,
// and a callback function that reacts to new lines.
type OutputBuffer struct {
	file    *os.File
	partial string
	m       sync.Mutex
	OnLine  func(string)
}

// creates a temp file for a new output buffer
func NewOutputBuffer() (*OutputBuffer, error) {
	f, err := os.CreateTemp("", "pragma-output-*")
	if err != nil {
		return nil, err
	}
	return &OutputBuffer{file: f}, nil
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
	// notify the callback for each complete line
	for _, line := range lines {
		if o.OnLine != nil {
			o.OnLine(line)
		}
	}
	return len(p), nil
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

// closes buffer by closing and deleting the file
func (o *OutputBuffer) Close() {
	o.m.Lock()
	defer o.m.Unlock()
	name := o.file.Name()
	o.file.Close()
	os.Remove(name)
}
