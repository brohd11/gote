package app

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// lspClient ignores every server-to-client feature except diagnostics; unsupported
// requests get the protocol package's standard response.
type lspClient struct {
	protocol.UnimplementedClient
	manager *lspManager
	root    string
}

func (*lspClient) RegisterCapability(context.Context, *protocol.RegistrationParams) error {
	return nil
}

func (*lspClient) UnregisterCapability(context.Context, *protocol.UnregistrationParams) error {
	return nil
}

func (*lspClient) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

// DiagnosticRefresh answers a server's "re-pull everything"; the default reply is an error
// a server may read as "cannot refresh".
func (c *lspClient) DiagnosticRefresh(context.Context) error {
	c.manager.requestPullRefresh(c.root)
	return nil
}

func (c *lspClient) Configuration(_ context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	if params == nil {
		return nil, nil
	}
	return make([]protocol.LSPAny, len(params.Items)), nil
}

func (c *lspClient) WorkspaceFolders(context.Context) ([]protocol.WorkspaceFolder, error) {
	return []protocol.WorkspaceFolder{{URI: uri.File(c.root), Name: filepath.Base(c.root)}}, nil
}

func (c *lspClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	if params == nil || !params.URI.IsFile() {
		return nil
	}
	path := uriPath(params.URI)
	c.manager.mu.Lock()
	// Store diagnostics under the open document's spelling: Windows URIs lowercase the drive
	// letter.
	var doc lspDocument
	open := false
	for desiredPath, candidate := range c.manager.desired {
		if sameFilePath(desiredPath, path) {
			path, doc, open = desiredPath, candidate, true
			break
		}
	}
	if !open {
		// Diagnostics for files gote never opened are kept and mark the root project-scoped:
		// gdscript-lsp and rust-analyzer push whole-project results unasked.
		c.manager.markProjectRootLocked(c.root)
	} else if version, ok := params.Version.Get(); ok && version < doc.version {
		c.manager.mu.Unlock()
		return nil
	}
	diagnostics := make([]lspDiagnostic, 0, len(params.Diagnostics))
	for _, item := range params.Diagnostics {
		diagnostics = append(diagnostics, projectDiagnostic(item))
	}
	key := diagnosticsKey(path)
	changed := !slices.Equal(c.manager.diagnostics[key], diagnostics)
	if changed {
		c.manager.diagnosticsRevision++
		c.manager.diagnostics[key] = diagnostics
	}
	c.manager.mu.Unlock()
	if !changed {
		return nil
	}
	if open {
		// The file in the editor answers at once: its gutter and its rows are what the
		// reader is looking at.
		c.manager.emit(lspEvent{})
		return nil
	}
	// A project file goes through the settle timer instead. The panel reflows every row
	// it holds on each revision, and a project publishes hundreds of times.
	c.manager.wakeDiagnostics()
	return nil
}

// stdioTransport joins a child's stdout and stdin into the bidirectional shape the LSP
// framer expects. Closing it tears down only the process Gote created.
type stdioTransport struct {
	io.Reader
	io.Writer
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	once   sync.Once
}

func (t *stdioTransport) Close() error {
	var first error
	t.once.Do(func() {
		if err := t.stdin.Close(); err != nil {
			first = err
		}
		if err := t.stdout.Close(); first == nil && err != nil {
			first = err
		}
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		go func() { _ = t.cmd.Wait() }()
	})
	return first
}
