package app

import (
	"context"
	"slices"
	"sort"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The workspace-diagnostic lane: LSP 3.17 pull diagnostics, the standard way to ask a
// server what is wrong with a project rather than inferring it from what it volunteers.
//
// It is a lane in the sense of the other four (lsp.go's completion and on-demand lanes,
// lsp_semantic.go, lsp_outline.go): one replaceable request per session, its own worker,
// gated on an advertised capability, and unable to evict anything a keystroke asked for.
//
// Why this exists at all: a server decides the SCOPE of its own diagnostics, and the three
// answers are all different. gdscript-lsp diagnoses its whole project on `initialized` and
// pushes the result unasked — measured at 197 diagnostics across 36 files with gote having
// opened nothing. rust-analyzer does the same across a crate. gopls advertises no
// diagnosticProvider at all and reports only on the package holding an open file. This
// lane serves the fourth case, a server that answers workspace pulls, and the other three
// need nothing beyond keeping what arrives (see lspClient.PublishDiagnostics).

const (
	// The floor between two workspace pulls. The request is specified as long-running —
	// a server may hold it open until it has news — so the loop is normally paced by the
	// server itself; this only stops a server that answers instantly from spinning the lane.
	lspPullMinInterval = 2 * time.Second
	// After a failed pull. Deliberately not failSession's business: a diagnostics request
	// that errors must not take the whole session down with a dead-server backoff.
	lspPullRetryDelay = 15 * time.Second
)

// lspPullResult is one finished workspace/diagnostic request, delivered to the actor.
type lspPullResult struct {
	key    string
	report *protocol.WorkspaceDiagnosticReport
	err    error
}

// workspaceDiagnosticOptions reads the workspace half of the diagnosticProvider
// capability. It is a union of two shapes in the protocol — the registration variant
// embeds the plain one — and anything else means the server said nothing usable.
func workspaceDiagnosticOptions(provider protocol.DiagnosticProvider) (supported bool, identifier *string) {
	switch options := provider.(type) {
	case *protocol.DiagnosticOptions:
		return options.WorkspaceDiagnostics, options.Identifier
	case *protocol.DiagnosticRegistrationOptions:
		return options.WorkspaceDiagnostics, options.Identifier
	}
	return false, nil
}

// beginWorkspacePull issues the next pull for a session that wants one. Called from
// converge on the actor goroutine, so the session's own lane state needs no lock.
func (m *lspManager) beginWorkspacePull(session *lspSession) {
	if session == nil || session.server == nil || !session.pullWorkspace || session.pullActive {
		return
	}
	if m.takePullRefresh(session.root) {
		// The server said everything it told us may be wrong. The stored result ids are
		// kept even so: they are what lets it answer "unchanged" for the files that are.
		session.pullAgainAt = time.Time{}
	} else if wait := time.Until(session.pullAgainAt); wait > 0 {
		// Nothing else would bring converge back around at the right moment, and a timer
		// in the loop for one lane is more machinery than a single wake needs.
		time.AfterFunc(wait, m.signal)
		return
	}
	previous := make([]protocol.PreviousResultId, 0, len(session.pullResultIDs))
	for path, id := range session.pullResultIDs {
		previous = append(previous, protocol.PreviousResultId{URI: uri.File(path), Value: id})
	}
	sort.Slice(previous, func(i, j int) bool { return previous[i].URI < previous[j].URI })
	params := &protocol.WorkspaceDiagnosticParams{PreviousResultIds: previous}
	if session.pullIdentifier != nil {
		params.Identifier = session.pullIdentifier
	}

	// No timeout, unlike every other lane. workspace/diagnostic is specified as
	// long-running: the server is entitled to hold the request open until it has
	// something to report, and that long poll IS the refresh mechanism. The context is
	// cancelled by session teardown and by nothing else.
	ctx, cancel := context.WithCancel(context.Background())
	session.pullCancel, session.pullActive = cancel, true
	server, key := session.server, session.key
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		report, err := server.DiagnosticWorkspace(ctx, params)
		select {
		case m.pulls <- lspPullResult{key: key, report: report, err: err}:
		case <-m.stop:
		}
	}()
}

// applyWorkspacePull folds a finished pull into the diagnostics map and lines up the next
// one. Runs on the actor goroutine.
func (m *lspManager) applyWorkspacePull(sessions map[string]*lspSession, result lspPullResult) {
	session := sessions[result.key]
	if session == nil || !session.pullActive {
		return // the session was restarted out from under the request
	}
	session.pullActive, session.pullCancel = false, nil
	if result.err != nil {
		// Including a cancelled context, which is what teardown looks like from here.
		session.pullAgainAt = time.Now().Add(lspPullRetryDelay)
		return
	}
	m.foldWorkspacePull(session, result.report)
	// Straight back around: on a server that long-polls this parks until it has news,
	// which is exactly the intended shape. The floor is what saves us from one that
	// answers instantly.
	session.pullAgainAt = time.Now().Add(lspPullMinInterval)
	m.beginWorkspacePull(session)
}

func (m *lspManager) foldWorkspacePull(session *lspSession, report *protocol.WorkspaceDiagnosticReport) {
	if report == nil || len(report.Items) == 0 {
		return
	}
	changed := false
	m.mu.Lock()
	// Answering a workspace pull is the strongest possible statement that this server
	// speaks for the whole project, not just for open tabs.
	m.markProjectRootLocked(session.root)
	for _, item := range report.Items {
		switch entry := item.(type) {
		case *protocol.WorkspaceFullDocumentDiagnosticReport:
			if !entry.URI.IsFile() {
				continue
			}
			path := diagnosticsKey(uriPath(entry.URI))
			diagnostics := make([]lspDiagnostic, 0, len(entry.Items))
			for _, diagnostic := range entry.Items {
				diagnostics = append(diagnostics, projectDiagnostic(diagnostic))
			}
			if !slices.Equal(m.diagnostics[path], diagnostics) {
				m.diagnosticsRevision++
				m.diagnostics[path] = diagnostics
				changed = true
			}
			// A result id is the server's own handle on what it last told us; without
			// storing it the next pull can never be answered "unchanged".
			if entry.ResultID != nil {
				session.setPullResultID(path, *entry.ResultID)
			}
		case *protocol.WorkspaceUnchangedDocumentDiagnosticReport:
			if !entry.URI.IsFile() {
				continue
			}
			// Unchanged means what is stored still stands; only the handle is refreshed.
			session.setPullResultID(diagnosticsKey(uriPath(entry.URI)), entry.ResultID)
		}
	}
	m.mu.Unlock()
	if changed {
		m.wakeDiagnostics()
	}
}

func (s *lspSession) setPullResultID(path, id string) {
	if s.pullResultIDs == nil {
		s.pullResultIDs = map[string]string{}
	}
	s.pullResultIDs[path] = id
}

// requestPullRefresh records a workspace/diagnostic/refresh from a server. It arrives on
// the jsonrpc goroutine, so the want is parked under the lock and consumed by the actor.
func (m *lspManager) requestPullRefresh(root string) {
	m.mu.Lock()
	if m.refreshPulls == nil {
		m.refreshPulls = map[string]bool{}
	}
	m.refreshPulls[root] = true
	m.mu.Unlock()
	m.signal()
}

func (m *lspManager) takePullRefresh(root string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.refreshPulls[root] {
		return false
	}
	delete(m.refreshPulls, root)
	return true
}
