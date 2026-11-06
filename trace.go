package acphost

import "github.com/elee1766/acphost/internal/trace"

func lookPath(name string) (string, error) { return trace.LookPath(name) }

func traceEvent(what string) { trace.Event(what) }
