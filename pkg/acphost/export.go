package acphost

import (
	"context"
	"log/slog"
	"time"
)

// Exporter receives every frame the host journals, for example to ship
// the conversation to a telemetry backend. The host calls Observe with
// its state lock held, so Observe must not block: buffer and drop instead.
type Exporter interface {
	// Observe is called with each frame; from is "agent" or "controller".
	// line must not be retained after Observe returns.
	Observe(from string, line []byte)
	// TakeCounts returns the records dropped and fields truncated since the
	// last call. The host reports them to the controller as MethodExportStats.
	TakeCounts() (dropped, truncated int64)
	// Close flushes and stops the exporter. ctx bounds the flush.
	Close(ctx context.Context) error
}

// ExporterFactory builds the Exporter for a session. It runs once, before
// the agent starts. An error is logged and the session runs without export.
type ExporterFactory func(spec Spec) (Exporter, error)

// Option configures Main.
type Option func(*options)

type options struct {
	exporter   ExporterFactory
	redactor   Redactor
	statsEvery time.Duration      // MethodExportStats period; tests shorten it
	killGrace  time.Duration      // SIGTERM to SIGKILL on stop; tests shorten it
	started    func(agentPid int) // called once the agent runs; set by the detached host child
	listening  func(ListenInfo)   // called once the TCP listener is up
	logger     *slog.Logger       // nil logs text to stderr
}

// WithLogger sets the logger the host writes to. Nil means text on stderr.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithExporter sets the conversation export factory. Nil means no export.
func WithExporter(f ExporterFactory) Option {
	return func(o *options) { o.exporter = f }
}

// Redactor rewrites a frame's JSON-RPC message before it is written to the
// journal. from is "agent" or "controller". msg must not be retained or
// modified; return a new slice or msg itself. It runs with the host's state
// lock held, so it must be quick.
type Redactor func(from string, msg []byte) []byte

// WithRedactor sets a Redactor applied to each frame before it is
// journaled. Only the journal on disk is redacted, and so replays read from
// it after a reconnect. Live frames forwarded to the connected controller or
// agent, and frames passed to an Exporter, are unchanged. If the redactor
// returns invalid JSON the frame's msg is written as null and the host logs
// a warning, so the journal stays parseable. Nil means no redaction.
func WithRedactor(r Redactor) Option {
	return func(o *options) { o.redactor = r }
}
