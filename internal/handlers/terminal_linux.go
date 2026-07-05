//go:build linux

package handlers

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// unixPTY adapta um PTY criado por creack/pty à interface ptySession.
// Usado quando o app roda dentro do WSL: bash é spawnado diretamente,
// sem o wrapper wsl.exe do caminho Windows.
type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) Close() error {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.f.Close()
}

// startPTY sobe um PTY nativo executando o shell diretamente. shellCmd é a
// mesma linha de comando montada no handler; bash -c a interpreta com aspas.
func startPTY(shellCmd string, cols, rows int) (ptySession, error) {
	cmd := exec.Command("bash", "-c", shellCmd)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}
