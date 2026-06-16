package files

import "github.com/AamindMandragora/pragma/internal/tools"

func RegisterAll() []tools.Tool {
	return []tools.Tool{
		&ReadFileTool{},
		&WriteFileTool{},
		&EditFileTool{},
		&DeleteFileTool{},
		&MoveFileTool{},
	}
}
