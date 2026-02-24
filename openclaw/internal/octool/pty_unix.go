//go:build !windows

package octool

import (
	"errors"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

func startPTY(cmd *exec.Cmd) (*os.File, func() error, error) {
	if cmd == nil {
		return nil, nil, errors.New("nil command")
	}

	master, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	closeIO := func() error {
		return master.Close()
	}
	return master, closeIO, nil
}
