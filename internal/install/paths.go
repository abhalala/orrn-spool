package install

import (
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	Executable         string
	PreviousExecutable string
	StateDir           string
	DatabaseFile       string
	NodeFile           string
}

func DefaultPaths() Paths {
	if runtime.GOOS == "windows" {
		stateRoot := os.Getenv("ProgramData")
		if stateRoot == "" {
			stateRoot = `C:\ProgramData`
		}
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		stateDir := filepath.Join(stateRoot, "ORRN", "Edge")
		execDir := filepath.Join(programFiles, "ORRN", "Edge")
		return Paths{
			Executable:         filepath.Join(execDir, "orrn-edge.exe"),
			PreviousExecutable: filepath.Join(execDir, "orrn-edge.previous.exe"),
			StateDir:           stateDir,
			DatabaseFile:       filepath.Join(stateDir, "edge.db"),
			NodeFile:           filepath.Join(stateDir, "node.json"),
		}
	}
	stateDir := "/var/lib/orrn-edge"
	return Paths{
		Executable:         "/usr/local/bin/orrn-edge",
		PreviousExecutable: "/usr/local/bin/orrn-edge.previous",
		StateDir:           stateDir,
		DatabaseFile:       filepath.Join(stateDir, "edge.db"),
		NodeFile:           filepath.Join(stateDir, "node.json"),
	}
}
