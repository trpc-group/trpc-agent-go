//go:build windows

package octool

import (
	"errors"
	"os"
	"os/exec"
)

func startPTY(cmd *exec.Cmd) (*os.File, func() error, error) {
	return nil, nil, errors.New("pty is not supported on windows")
}
