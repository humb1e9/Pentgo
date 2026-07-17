package terminal

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"pentgo/internal/app"
	"pentgo/internal/report"
	"pentgo/internal/runtime"
)

type recordingRunner struct {
	requests []app.Request
	result   app.Result
}

func (runner *recordingRunner) Run(_ context.Context, request app.Request, _ func(app.Event)) (app.Result, error) {
	runner.requests = append(runner.requests, request)
	return runner.result, nil
}

func TestParseTaskExtractsRuntimeTargetAndRetainsIntent(t *testing.T) {
	task, err := ParseTask("对 https://Example.Test/app 做检查")
	if err != nil || task.Target.Canonical != "https://example.test/app" || task.Intent != "对 https://Example.Test/app 做检查" {
		t.Fatalf("task/err = %+v/%v", task, err)
	}
}

func TestTerminalRunsNaturalLanguageTaskAndShowsArtifacts(t *testing.T) {
	session := runtime.NewSession(runtime.Target{Canonical: "https://example.test"}, "检查", time.Now())
	session.ID = "eng-test"
	session.Status = runtime.SessionDone
	runner := &recordingRunner{result: app.Result{Session: session, Artifacts: report.Artifacts{Markdown: "eng-test/report.md", SessionJSON: "eng-test/session.json"}}}
	var output bytes.Buffer
	instance := New(strings.NewReader("对 example.test 做检查\n/quit\n"), &output, runner, make(chan os.Signal))
	if err := instance.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 1 || runner.requests[0].Target.Canonical != "https://example.test" || !strings.Contains(output.String(), "report.md") {
		t.Fatalf("requests/output = %+v/%q", runner.requests, output.String())
	}
}

func TestTerminalCancelsActiveEngagementWithCommand(t *testing.T) {
	runner := newBlockingRunner()
	var output bytes.Buffer
	instance := New(strings.NewReader("对 example.test 做检查\n/cancel\n/quit\n"), &output, runner, make(chan os.Signal))
	if err := instance.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.cancelled || !strings.Contains(output.String(), "Cancelling") {
		t.Fatalf("cancelled/output = %t/%q", runner.cancelled, output.String())
	}
}

func TestTerminalHelpListsCancelCommand(t *testing.T) {
	var output bytes.Buffer
	instance := New(strings.NewReader("/help\n/quit\n"), &output, &recordingRunner{}, make(chan os.Signal))
	if err := instance.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "/cancel") {
		t.Fatalf("output = %q", output.String())
	}
}

type fakeRunner struct {
	gate    chan struct{}
	started chan struct{}
}

func (runner *fakeRunner) Run(ctx context.Context, _ app.Request, _ func(app.Event)) (app.Result, error) {
	close(runner.started)
	select {
	case <-runner.gate:
	case <-ctx.Done():
	}
	return app.Result{}, nil
}

func TestRunReturnsWhenEOFArrivesDuringEngagement(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{})}
	input := strings.NewReader("对 http://example.test 做检查\n")
	instance := NewWithOutputRoot(input, io.Discard, runner, make(chan os.Signal), t.TempDir())

	done := make(chan error, 1)
	go func() {
		done <- instance.Run(context.Background())
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("engagement never started")
	}
	close(runner.gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after engagement completed with EOF pending (deadlock)")
	}
}

type blockingRunner struct {
	cancelled bool
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{}
}

func (runner *blockingRunner) Run(ctx context.Context, _ app.Request, _ func(app.Event)) (app.Result, error) {
	<-ctx.Done()
	runner.cancelled = true
	session := runtime.NewSession(runtime.Target{Canonical: "https://example.test"}, "检查", time.Now())
	session.Status = runtime.SessionCancelled
	return app.Result{Session: session, Artifacts: report.Artifacts{Markdown: "eng-cancel/report.md"}, RunError: ctx.Err()}, nil
}
