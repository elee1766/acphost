package acphost_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/elee1766/acphost/pkg/acphost"
)

// A client writes a spec, starts the host, connects to acp.sock, and
// later reconnects asking for the notifications it missed since a seq.
func Example() {
	dir, _ := os.MkdirTemp("", "acphost-example")
	defer func() { _ = os.RemoveAll(dir) }()

	// A stand-in agent: one ACP notification, then it runs until signaled.
	// It ignores stdin EOF so the stop below always ends it by SIGTERM
	// (stop closes stdin first, and an agent that exits on EOF races it).
	agent := filepath.Join(dir, "agent")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"method\":\"session/update\"}'\nexec sleep 600\n"), 0o700)

	// 1. Write the spec.
	_ = acphost.WriteSpec(dir, acphost.Spec{
		Protocol: acphost.Protocol, Session: "demo", Command: agent, Dir: dir,
		LeaseMS: 60_000, GCRootDir: dir,
	})

	// 2. Start the host. A real client runs its own binary detached, with
	// a subcommand that calls acphost.Main(dir); here it runs in-process.
	done := make(chan error, 1)
	go func() { done <- acphost.Main(dir) }()

	// 3. Connect. The welcome carries the last journal seq. acp.sock
	// appears once Main is listening, so retry until it does: there is no
	// event to wait on from outside the host process.
	ctx := context.Background()
	var nc net.Conn
	var err error
	for range 500 {
		if nc, _, _, err = acphost.Connect(ctx, dir); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond) // waiting for Main to listen; see above
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	// Wait until the agent's notification is in the journal. It may have
	// been written before this client connected, so it need not arrive
	// live, and the journal file is the only outside signal.
	for range 500 {
		if seq, _ := acphost.JournalTail(dir); seq > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond) // polling the journal; see above
	}
	_ = nc.Close() // the client goes away

	// 4. Reconnect and replay everything after seq 0. Hello.Since asks
	// the host to send the agent notifications it journaled since then.
	nc, _ = net.Dial("unix", filepath.Join(dir, acphost.ACPSocket))
	since := int64(0)
	hello, _ := json.Marshal(acphost.Hello{Protocol: acphost.Protocol, Since: &since})
	_, _ = nc.Write(append(hello, '\n'))
	rd := bufio.NewReader(nc)
	var w acphost.Welcome
	line, _ := rd.ReadBytes('\n')
	_ = json.Unmarshal(line, &w)
	replayed, _ := rd.ReadBytes('\n')
	fmt.Printf("welcome seq %d, replayed %s", w.Seq, replayed)

	// 5. Stop the host.
	_, _ = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + acphost.MethodStop + `"}` + "\n"))
	if err := <-done; err != nil {
		fmt.Println(err)
	}
	_ = nc.Close()
	ex, _ := acphost.ReadExit(dir)
	fmt.Println("exit:", ex.Reason, ex.Signal)
	// Output:
	// welcome seq 1, replayed {"jsonrpc":"2.0","method":"session/update"}
	// exit: controller requested stop terminated
}
