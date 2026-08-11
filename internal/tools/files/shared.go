package files

import (
	"fmt"

	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
)

// checkNotIgnored returns an error if path is excluded by .agentignore.
func checkNotIgnored(path string) error {
	if process.IsIgnored(path) {
		return fmt.Errorf("access denied: %s is in .agentignore", path)
	}
	return nil
}

func RegisterAll() []tools.Tool {
	return []tools.Tool{
		&ReadFileTool{},
		&WriteFileTool{},
		&EditFileTool{},
		&DeleteFileTool{},
		&MoveFileTool{},
	}
}
