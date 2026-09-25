package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brohd11/goutil/executil"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const (
	lspRetryDelay = 15 * time.Second
	lspDialLimit  = 1500 * time.Millisecond
	// Generous because a server may index the whole project during initialize
	// (gdscript-lsp takes ~10s on a large project); a timeout fails the session and the
	// respawn would time out again forever.
	lspInitLimit       = 60 * time.Second
	lspCompletionLimit = 2 * time.Second
	lspChangeDebounce  = 500 * time.Millisecond
)

// How often project-wide diagnostics reach the UI (the panel reflows on each revision);
// the file in the editor is emitted immediately.
const lspDiagnosticsSettle = 300 * time.Millisecond

// lspDocument is the manager's desired view of one editor buffer. version is the LSP
// version, starting at 1 on didOpen and advancing with each editor generation.
type lspDocument struct {
	path, language, server, root, text string
	editSeq                            int
	version                            int32
}

func (d lspDocument) key() string { return d.server + "\x00" + d.root }

type lspEvent struct {
	status     string
	completion *lspCompletionResult
	request    *lspRequestResult
	semantic   *lspSemanticResult
	outline    *lspOutlineResult
}

// lspManager is an actor for server and document lifecycle. The UI writes desired
// snapshots under mu and wakes it; it converges sessions off the Bubble Tea goroutine,
// sending edits after the change debounce.
type lspManager struct {
	cfg            Config
	version        string
	changeDebounce time.Duration

	mu                  sync.Mutex
	desired             map[string]lspDocument
	saves               map[string]bool
	diagnosticsRevision uint64
	diagnostics         map[string][]lspDiagnostic
	completion          *lspCompletionRequest
	nextComplete        uint64
	completionTriggers  map[string]map[string]bool
	// The on-demand lane (lsp_request.go): its own slot and counter, so a queued
	// hover never cancels a completion the same keystroke asked for.
	request           *lspRequest
	nextRequest       uint64
	signatureTriggers map[string]map[string]bool
	capabilities      map[string]*protocol.ServerCapabilities
	roots             []string
	// refreshPulls carries workspace/diagnostic/refresh from the jsonrpc goroutine to
	// the actor, by session root.
	refreshPulls map[string]bool
	// projectRoots are roots whose server reports beyond the open documents (a workspace
	// pull, or files gote never opened). There a closed buffer keeps its diagnostics;
	// elsewhere they go stale on didClose.
	projectRoots map[string]bool
	// The semantic-token lane has its own slot so background refreshes never evict user
	// requests. semanticLegends is per session: token types are indexes into each server's
	// list.
	semantic        *lspSemanticRequest
	nextSemantic    uint64
	semanticLegends map[string][]string
	// The outline lane (lsp_outline.go) is background work like semantic tokens, but
	// independently replaceable so neither refresh can cancel the other.
	outline     *lspOutlineRequest
	nextOutline uint64
	restart     bool
	closed      bool

	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	events chan lspEvent
	dead   chan deadSession
	// diagWake coalesces volunteered and pulled diagnostics into the settle timer instead
	// of waking a converge for each report.
	diagWake chan struct{}
	pulls    chan lspPullResult
	once     sync.Once
	workers  sync.WaitGroup
}

type deadSession struct {
	key  string
	conn jsonrpc2.Conn
	err  error
}

type lspSession struct {
	key, serverID, root string
	server              protocol.Server
	conn                jsonrpc2.Conn
	sent                map[string]int32
	retryAt             time.Time
	failure             string
	// caps is the whole initialize answer, not just the completion options: every
	// on-demand request is gated on what this server said it can do.
	caps *protocol.ServerCapabilities
	// The workspace-diagnostic lane (lsp_diagnostic_pull.go). Actor-owned, like the
	// session itself: only converge and the loop's result handling touch these.
	pullWorkspace  bool
	pullIdentifier *string
	pullResultIDs  map[string]string
	pullActive     bool
	pullCancel     context.CancelFunc
	pullAgainAt    time.Time
}

func newLSPManager(cfg Config, version string) *lspManager {
	return &lspManager{
		cfg: cfg, version: version, changeDebounce: lspChangeDebounce,
		desired: map[string]lspDocument{}, saves: map[string]bool{},
		diagnostics: map[string][]lspDiagnostic{}, completionTriggers: map[string]map[string]bool{},
		signatureTriggers: map[string]map[string]bool{},
		capabilities:      map[string]*protocol.ServerCapabilities{},
		semanticLegends:   map[string][]string{},
		wake:              make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		events: make(chan lspEvent, 64), dead: make(chan deadSession, 8),
		diagWake: make(chan struct{}, 1), pulls: make(chan lspPullResult, 8),
	}
}

func (m *lspManager) start() { m.once.Do(func() { go m.loop() }) }

func (m *lspManager) signal() {
	m.start()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Reconcile captures all supported open buffers. It performs no IO and never waits for
// the actor: one map replacement and a coalescing wake are the whole UI-thread cost.
func (m *lspManager) Reconcile(c *Ctx) bool {
	if m == nil || c == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	next := make(map[string]lspDocument)
	hadDocuments := len(m.desired) > 0
	c.EachDoc(func(path string, ed *editor.Screen) {
		profile := languageForPath(path)
		if profile == nil || profile.lsp == nil || ed == nil {
			return
		}
		serverCfg, ok := m.cfg.LanguageServers[profile.lsp.server]
		if !ok || serverCfg.Disabled {
			return
		}
		root := lspRootForPath(path, profile)
		if root == "" {
			return
		}
		path = filepath.Clean(path)
		doc := lspDocument{
			path: path, language: profile.id, server: profile.lsp.server, root: root,
			text: ed.Text(), editSeq: ed.EditSeq(), version: 1,
		}
		if prior, ok := m.desired[path]; ok && prior.server == doc.server && prior.root == doc.root {
			doc.version = prior.version
			if prior.editSeq != doc.editSeq || prior.text != doc.text {
				doc.version++
			}
		}
		next[path] = doc
	})
	for path := range m.desired {
		if _, ok := next[path]; !ok {
			// Keep a closed file's diagnostics only under a project-scoped root (see projectRoots).
			if !m.projectScopedLocked(path) {
				key := diagnosticsKey(path)
				if _, ok := m.diagnostics[key]; ok {
					m.diagnosticsRevision++
				}
				delete(m.diagnostics, key)
			}
			if m.completion != nil && m.completion.path == path {
				m.completion = nil
			}
			if m.request != nil && m.request.path == path {
				m.request = nil
			}
			if m.outline != nil && m.outline.path == path {
				m.outline = nil
			}
		}
	}
	m.desired = next
	m.mu.Unlock()
	if len(next) > 0 || hadDocuments {
		m.signal()
	}
	return len(next) > 0
}

func (m *lspManager) DidSave(path string) {
	if m == nil || path == "" {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.saves[filepath.Clean(path)] = true
	}
	m.mu.Unlock()
	m.signal()
}

func (m *lspManager) Restart() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.restart = true
		m.completion = nil
		m.nextComplete++
		m.request = nil
		m.nextRequest++
		// Every session is about to be rebuilt; leaving the old answers up would show a
		// list that no live session stands behind.
		if len(m.diagnostics) > 0 {
			m.diagnostics = map[string][]lspDiagnostic{}
			m.diagnosticsRevision++
		}
	}
	m.mu.Unlock()
	m.signal()
}

func (m *lspManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.start()
	close(m.stop)
	<-m.done
}

// WaitCmd is the single Bubble Tea subscription. Events are broadcasts because an LSP
// notification may arrive while a menu or diagnostics page is above the home screen.
func (m *lspManager) WaitCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	m.start()
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return nil
		}
		return core.PropagateAll(event)
	}
}

func (m *lspManager) loop() {
	defer close(m.done)
	sessions := map[string]*lspSession{}
	pending := map[string]lspDocument{}
	debounce := m.changeDebounce
	if debounce <= 0 {
		debounce = lspChangeDebounce
	}
	var changeTimer *time.Timer
	var changeTimerC <-chan time.Time
	var diagTimer *time.Timer
	var diagTimerC <-chan time.Time
	var cancelCompletion context.CancelFunc
	var cancelRequest context.CancelFunc
	var cancelSemantic context.CancelFunc
	var cancelOutline context.CancelFunc
	stopChangeTimer := func() {
		if changeTimer == nil {
			return
		}
		if !changeTimer.Stop() {
			select {
			case <-changeTimer.C:
			default:
			}
		}
		changeTimerC = nil
	}
	resetChangeTimer := func() {
		if changeTimer == nil {
			changeTimer = time.NewTimer(debounce)
		} else {
			stopChangeTimer()
			changeTimer.Reset(debounce)
		}
		changeTimerC = changeTimer.C
	}
	// Fire on a fixed cadence under a stream of publishes; a restarting debounce would never
	// fire.
	armDiagnostics := func() {
		if diagTimerC != nil {
			return
		}
		if diagTimer == nil {
			diagTimer = time.NewTimer(lspDiagnosticsSettle)
		} else {
			diagTimer.Reset(lspDiagnosticsSettle)
		}
		diagTimerC = diagTimer.C
	}
	for {
		select {
		case <-m.wake:
			queued := m.converge(sessions, pending, false)
			if request := m.takeCompletion(); request != nil {
				if cancelCompletion != nil {
					cancelCompletion()
				}
				cancelCompletion = m.startCompletion(sessions, pending, *request)
			}
			if request := m.takeRequest(); request != nil {
				if cancelRequest != nil {
					cancelRequest()
				}
				cancelRequest = m.startRequest(sessions, pending, *request)
			}
			if request := m.takeSemantic(); request != nil {
				if cancelSemantic != nil {
					cancelSemantic()
				}
				cancelSemantic = m.startSemantic(sessions, pending, *request)
			}
			if request := m.takeOutline(); request != nil {
				if cancelOutline != nil {
					cancelOutline()
				}
				cancelOutline = m.startOutline(sessions, pending, *request)
			}
			switch {
			case len(pending) == 0:
				stopChangeTimer()
			case queued || changeTimerC == nil:
				resetChangeTimer()
			}
		case <-changeTimerC:
			changeTimerC = nil
			m.converge(sessions, pending, true)
			if len(pending) > 0 {
				resetChangeTimer()
			}
		case result := <-m.pulls:
			m.applyWorkspacePull(sessions, result)
		case <-m.diagWake:
			armDiagnostics()
		case <-diagTimerC:
			diagTimerC = nil
			m.emit(lspEvent{})
		case dead := <-m.dead:
			if session := sessions[dead.key]; session != nil && session.conn == dead.conn {
				if dead.err == nil {
					dead.err = fmt.Errorf("connection closed")
				}
				m.failSession(session, dead.err)
				clearPendingSession(pending, session.key)
			}
		case <-m.stop:
			stopChangeTimer()
			if cancelCompletion != nil {
				cancelCompletion()
			}
			if cancelRequest != nil {
				cancelRequest()
			}
			if cancelSemantic != nil {
				cancelSemantic()
			}
			if cancelOutline != nil {
				cancelOutline()
			}
			for _, session := range sessions {
				m.closeSession(session)
			}
			m.workers.Wait()
			close(m.events)
			return
		}
	}
}

func (m *lspManager) desiredSnapshot() (map[string]lspDocument, map[string]bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := make(map[string]lspDocument, len(m.desired))
	for path, doc := range m.desired {
		docs[path] = doc
	}
	saves := m.saves
	m.saves = map[string]bool{}
	restart := m.restart
	m.restart = false
	return docs, saves, restart
}

// converge applies lifecycle work immediately; content changes wait in pending for a
// quiet period, while saves and reconnects send the latest snapshot at once. It reports
// whether a newer pending version needs a fresh debounce.
func (m *lspManager) converge(sessions map[string]*lspSession, pending map[string]lspDocument, flushChanges bool) bool {
	docs, saves, restart := m.desiredSnapshot()
	queuedChange := false
	for path, queued := range pending {
		doc, ok := docs[path]
		if !ok || queued.key() != doc.key() {
			delete(pending, path)
		}
	}
	if restart {
		for key, session := range sessions {
			m.closeSession(session)
			delete(sessions, key)
		}
		clear(pending)
	}

	// Close documents that disappeared or moved to another session before opening their
	// replacements. That ordering makes save-as one clean close/open transition.
	for _, session := range sessions {
		if session.server == nil {
			continue
		}
		for path := range session.sent {
			doc, ok := docs[path]
			if ok && doc.key() == session.key {
				continue
			}
			if err := session.server.DidClose(context.Background(), &protocol.DidCloseTextDocumentParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)},
			}); err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				break
			}
			delete(session.sent, path)
		}
	}

	grouped := make(map[string][]lspDocument)
	for _, doc := range docs {
		grouped[doc.key()] = append(grouped[doc.key()], doc)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := grouped[key]
		sort.Slice(group, func(i, j int) bool { return group[i].path < group[j].path })
		session := sessions[key]
		if session == nil {
			session = &lspSession{key: key, serverID: group[0].server, root: group[0].root, sent: map[string]int32{}}
			sessions[key] = session
		}
		if session.server == nil {
			if time.Now().Before(session.retryAt) {
				clearPendingSession(pending, session.key)
				continue
			}
			if err := m.startSession(session); err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				continue
			}
		}
		// A session's root is only known once it is up, which is where the workspace
		// diagnostic lane hangs off (lsp_diagnostic_pull.go).
		m.beginWorkspacePull(session)
		for _, doc := range group {
			version, opened := session.sent[doc.path]
			var err error
			switch {
			case !opened:
				err = session.server.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
					TextDocument: protocol.TextDocumentItem{
						URI: uri.File(doc.path), LanguageID: protocol.LanguageKind(doc.language),
						Version: doc.version, Text: doc.text,
					},
				})
			case version != doc.version:
				queued, wasQueued := pending[doc.path]
				ready := saves[doc.path] || (flushChanges && wasQueued && queued.version == doc.version && queued.key() == doc.key())
				if !ready {
					if !wasQueued || queued.version != doc.version || queued.key() != doc.key() {
						pending[doc.path] = doc
						queuedChange = true
					}
					continue
				}
				err = didChangeWhole(session.server, doc)
			}
			if err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				break
			}
			session.sent[doc.path] = doc.version
			delete(pending, doc.path)
			if saves[doc.path] {
				text := doc.text
				if err := session.server.DidSave(context.Background(), &protocol.DidSaveTextDocumentParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)}, Text: &text,
				}); err != nil {
					m.failSession(session, err)
					clearPendingSession(pending, session.key)
					break
				}
			}
		}
	}
	m.publishRoots(sessions)
	return queuedChange
}

// publishRoots hands the session roots to the UI (for project-relative paths); the only
// session state the UI sees.
func (m *lspManager) publishRoots(sessions map[string]*lspSession) {
	roots := make([]string, 0, len(sessions))
	for _, session := range sessions {
		roots = append(roots, session.root)
	}
	sort.Strings(roots)
	m.mu.Lock()
	m.roots = roots
	m.mu.Unlock()
}

// Roots is the project root of every live session, sorted.
func (m *lspManager) Roots() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.roots...)
}

func clearPendingSession(pending map[string]lspDocument, key string) {
	for path, doc := range pending {
		if doc.key() == key {
			delete(pending, path)
		}
	}
}

// flushForRequest resolves the live session for path at editSeq and, on the actor
// goroutine, sends it the document's current text if it holds an older version, so a
// request names exactly the text the server has. usable (optional) vetoes the session
// before anything is sent. session is nil when the request cannot proceed; err is set
// when the flush itself failed, which also fails the session.
func (m *lspManager) flushForRequest(sessions map[string]*lspSession, pending map[string]lspDocument,
	path string, editSeq int, usable func(lspDocument, *lspSession) bool) (lspDocument, *lspSession, error) {
	m.mu.Lock()
	doc, ok := m.desired[path]
	m.mu.Unlock()
	if !ok || doc.editSeq != editSeq {
		return doc, nil, nil
	}
	session := sessions[doc.key()]
	if session == nil || session.server == nil || (usable != nil && !usable(doc, session)) {
		return doc, nil, nil
	}
	if session.sent[doc.path] != doc.version {
		if err := didChangeWhole(session.server, doc); err != nil {
			m.failSession(session, err)
			clearPendingSession(pending, session.key)
			return doc, nil, err
		}
		session.sent[doc.path] = doc.version
		delete(pending, doc.path)
	}
	return doc, session, nil
}

// didChangeWhole sends doc's full text as a didChange notification.
func didChangeWhole(server protocol.Server, doc lspDocument) error {
	return server.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
			Version:                doc.version,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{Text: doc.text},
		},
	})
}

// goRequest runs fn on a tracked worker under a timeout and returns its cancel.
func (m *lspManager) goRequest(limit time.Duration, fn func(ctx context.Context)) context.CancelFunc {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		fn(ctx)
	}()
	return cancel
}

func (m *lspManager) startSession(session *lspSession) error {
	cfg, ok := m.cfg.LanguageServers[session.serverID]
	if !ok || cfg.Disabled {
		return fmt.Errorf("%s language server is disabled", session.serverID)
	}
	if cfg.InitializationOptions == nil {
		cfg.InitializationOptions = defaultLanguageServers()[session.serverID].InitializationOptions
	}
	hasAddress, hasCommand := strings.TrimSpace(cfg.Address) != "", len(cfg.Command) > 0 && strings.TrimSpace(cfg.Command[0]) != ""
	if hasAddress == hasCommand {
		return fmt.Errorf("%s language server needs exactly one of address or command", session.serverID)
	}

	var transport io.ReadWriteCloser
	if hasAddress {
		address := strings.TrimSpace(cfg.Address)
		conn, err := net.DialTimeout("tcp", address, lspDialLimit)
		if err != nil {
			return fmt.Errorf("connect %s at %s: %w", session.serverID, address, err)
		}
		transport = conn
	} else {
		cmd, err := executil.Command(cfg.Command...)
		if err != nil {
			return fmt.Errorf("start %s: %w", strings.Join(cfg.Command, " "), err)
		}
		cmd.Dir = session.root
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return err
		}
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return fmt.Errorf("start %s: %w", strings.Join(cfg.Command, " "), err)
		}
		transport = &stdioTransport{Reader: stdout, Writer: stdin, stdin: stdin, stdout: stdout, cmd: cmd}
	}

	client := &lspClient{manager: m, root: session.root}
	ctx, conn, server := protocol.NewClient(context.Background(), client, jsonrpc2.NewStream(transport))
	rootURI := uri.File(session.root)
	pid := int32(os.Getpid())
	version := protocol.NewOptional(m.version)
	var initializationOptions protocol.LSPAny
	if cfg.InitializationOptions != nil {
		encoded, marshalErr := json.Marshal(cfg.InitializationOptions)
		if marshalErr != nil {
			_ = conn.Close()
			return fmt.Errorf("initialize options for %s: %w", session.serverID, marshalErr)
		}
		initializationOptions = encoded
	}
	initCtx, cancel := context.WithTimeout(ctx, lspInitLimit)
	defer cancel()
	initialized, err := server.Initialize(initCtx, &protocol.InitializeParams{
		ProcessID:             &pid,
		ClientInfo:            protocol.ClientInfo{Name: "gote", Version: version},
		RootURI:               &rootURI,
		InitializationOptions: initializationOptions,
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{
				URI: rootURI, Name: filepath.Base(session.root),
			}}),
		},
		Capabilities: clientCapabilities(),
	})
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize %s: %w", session.serverID, err)
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize %s: %w", session.serverID, err)
	}
	session.server, session.conn = server, conn
	if initialized != nil {
		session.caps = &initialized.Capabilities
		session.pullWorkspace, session.pullIdentifier = workspaceDiagnosticOptions(initialized.Capabilities.DiagnosticProvider)
	}
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	if session.caps != nil {
		m.capabilities[session.key] = session.caps
		if completion := session.caps.CompletionProvider; completion != nil {
			m.completionTriggers[session.key] = triggerSet(completion.TriggerCharacters)
		}
		if signature := session.caps.SignatureHelpProvider; signature != nil {
			m.signatureTriggers[session.key] = triggerSet(signature.TriggerCharacters)
		}
		// Cache the server's token legend now, while the capabilities are in hand: types arrive
		// as indexes into it.
		if legend, ok := semanticLegend(session.caps.SemanticTokensProvider); ok {
			m.semanticLegends[session.key] = legend
		}
	}
	m.mu.Unlock()
	session.failure, session.retryAt = "", time.Time{}
	session.sent = map[string]int32{}
	go func(key string, watch jsonrpc2.Conn) {
		<-watch.Done()
		select {
		case m.dead <- deadSession{key: key, conn: watch, err: watch.Err()}:
		case <-m.stop:
		}
	}(session.key, conn)
	return nil
}

// triggerSet indexes an advertised trigger-character list for lookup by typed text.
func triggerSet(characters []string) map[string]bool {
	triggers := make(map[string]bool, len(characters))
	for _, trigger := range characters {
		triggers[trigger] = true
	}
	return triggers
}

// forgetCapabilitiesLocked drops one session's cached capabilities (held under mu); a
// session without them answers "unsupported".
func (m *lspManager) forgetCapabilitiesLocked(key string) {
	delete(m.completionTriggers, key)
	delete(m.signatureTriggers, key)
	delete(m.capabilities, key)
	delete(m.semanticLegends, key)
}

func (m *lspManager) failSession(session *lspSession, err error) {
	session.cancelPull()
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.server, session.conn, session.caps = nil, nil, nil
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	m.mu.Unlock()
	session.sent = map[string]int32{}
	session.retryAt = time.Now().Add(lspRetryDelay)
	message := err.Error()
	if message == session.failure {
		return
	}
	session.failure = message
	m.emit(lspEvent{status: "LSP: " + message})
}

func (m *lspManager) closeSession(session *lspSession) {
	if session == nil || session.conn == nil {
		return
	}
	session.cancelPull()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = session.server.Shutdown(ctx)
	_ = session.server.Exit(ctx)
	cancel()
	_ = session.conn.Close()
	session.server, session.conn, session.caps = nil, nil, nil
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	m.mu.Unlock()
}

func (m *lspManager) emit(event lspEvent) {
	select {
	case m.events <- event:
	default:
	}
}

// cancelPull ends an in-flight workspace/diagnostic, which has no timeout of its own.
func (s *lspSession) cancelPull() {
	if s.pullCancel != nil {
		s.pullCancel()
		s.pullCancel = nil
	}
	s.pullActive, s.pullWorkspace = false, false
}

// markProjectRootLocked records that root's server reports beyond the documents it was
// given. Callers hold mu.
func (m *lspManager) markProjectRootLocked(root string) {
	if root == "" {
		return
	}
	if m.projectRoots == nil {
		m.projectRoots = map[string]bool{}
	}
	m.projectRoots[root] = true
}

// projectScopedLocked reports whether path belongs to a root whose server answers for the
// whole project. Callers hold mu.
func (m *lspManager) projectScopedLocked(path string) bool {
	for root := range m.projectRoots {
		if underPath(path, root) {
			return true
		}
	}
	return false
}

// clientCapabilities is what gote tells a server it supports. A server may withhold any
// feature the client does not declare, and request dispatch gates on what it advertises.
func clientCapabilities() protocol.ClientCapabilities {
	yes := true
	return protocol.ClientCapabilities{
		Workspace: &protocol.WorkspaceClientCapabilities{
			WorkspaceFolders: &yes,
			// RefreshSupport lets a server ask for a re-pull of project diagnostics.
			Diagnostics: &protocol.DiagnosticWorkspaceClientCapabilities{RefreshSupport: &yes},
		},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{VersionSupport: &yes},
			// Declared so a server can advertise pull diagnostics (lsp_diagnostic_pull.go).
			Diagnostic: &protocol.DiagnosticClientCapabilities{},
			Completion: &protocol.CompletionClientCapabilities{
				ContextSupport: &yes,
				CompletionItem: &protocol.ClientCompletionItemOptions{SnippetSupport: &yes},
			},
			Hover: &protocol.HoverClientCapabilities{
				ContentFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
			},
			Definition: &protocol.DefinitionClientCapabilities{LinkSupport: &yes},
			References: &protocol.ReferenceClientCapabilities{},
			DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
				HierarchicalDocumentSymbolSupport: &yes,
			},
			Formatting: &protocol.DocumentFormattingClientCapabilities{},
			CodeAction: &protocol.CodeActionClientCapabilities{
				CodeActionLiteralSupport: protocol.ClientCodeActionLiteralOptions{
					CodeActionKind: protocol.ClientCodeActionKindOptions{
						ValueSet: []protocol.CodeActionKind{protocol.CodeActionKindSourceOrganizeImports},
					},
				},
			},
			// Semantic tokens overlay chroma's highlighting (AugmentsSyntaxTokens). The full
			// spec name lists say what gote can decode, not only what the palette maps.
			// gopls also needs its own semanticTokens option (defaultLanguageServers).
			SemanticTokens: protocol.SemanticTokensClientCapabilities{
				Requests: protocol.ClientSemanticTokensRequestOptions{
					Full: protocol.Boolean(true),
				},
				TokenTypes:           semanticTokenTypeNames,
				TokenModifiers:       semanticTokenModifierNames,
				Formats:              []protocol.TokenFormat{protocol.TokenFormatRelative},
				AugmentsSyntaxTokens: &yes,
			},
			SignatureHelp: &protocol.SignatureHelpClientCapabilities{
				SignatureInformation: &protocol.ClientSignatureInformationOptions{
					DocumentationFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
					ParameterInformation: &protocol.ClientSignatureParameterInformationOptions{
						LabelOffsetSupport: &yes,
					},
				},
			},
		},
		General: &protocol.GeneralClientCapabilities{
			PositionEncodings: []protocol.PositionEncodingKind{protocol.PositionEncodingKindUTF16},
		},
	}
}
