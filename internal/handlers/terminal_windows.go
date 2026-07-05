//go:build windows

package handlers

import (
	"fmt"

	"github.com/UserExistsError/conpty"
)

// winPTY adapta o ConPTY à interface ptySession. O ConPty já implementa
// Read/Write/Close e Resize(cols, rows int), então basta embutir.
type winPTY struct{ *conpty.ConPty }

// startPTY sobe um ConPTY executando o shell dentro do WSL (wsl.exe).
func startPTY(shellCmd string, cols, rows int) (ptySession, error) {
	var full string
	if user := getWslUser(); user != "" {
		full = fmt.Sprintf("wsl.exe -u %s -- %s", user, shellCmd)
	} else {
		full = "wsl.exe -- " + shellCmd
	}
	cpty, err := conpty.Start(full, conpty.ConPtyDimensions(cols, rows))
	if err != nil {
		return nil, err
	}
	return &winPTY{cpty}, nil
}
