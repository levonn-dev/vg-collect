// White-box tests for the unexported fanout slog.Handler.
// Must be in package otel (not otel_test) to access the unexported type.
package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// bufHandler is a minimal slog.Handler that writes each record's message and
// attrs into a bytes.Buffer. Always Enabled unless level is below levelFilter.
type bufHandler struct {
	buf         *bytes.Buffer
	levelFilter slog.Level
	attrs       []slog.Attr
	group       string
}

func (b *bufHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= b.levelFilter
}

func (b *bufHandler) Handle(_ context.Context, r slog.Record) error {
	b.buf.WriteString(r.Message)
	for _, a := range b.attrs {
		b.buf.WriteString(" " + a.Key + "=" + a.Value.String())
	}
	return nil
}

func (b *bufHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *b
	n.attrs = append(append([]slog.Attr{}, b.attrs...), attrs...)
	return &n
}

func (b *bufHandler) WithGroup(name string) slog.Handler {
	n := *b
	n.group = name
	return &n
}

// testErrHandler always returns a fixed error from Handle.
type testErrHandler struct{ err error }

func (e *testErrHandler) Enabled(context.Context, slog.Level) bool      { return true }
func (e *testErrHandler) Handle(_ context.Context, _ slog.Record) error { return e.err }
func (e *testErrHandler) WithAttrs([]slog.Attr) slog.Handler            { return e }
func (e *testErrHandler) WithGroup(string) slog.Handler                 { return e }

// testDisabledHandler always returns Enabled=false.
type testDisabledHandler struct{}

func (d *testDisabledHandler) Enabled(context.Context, slog.Level) bool      { return false }
func (d *testDisabledHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (d *testDisabledHandler) WithAttrs([]slog.Attr) slog.Handler            { return d }
func (d *testDisabledHandler) WithGroup(string) slog.Handler                 { return d }

func makeFanout(handlers ...slog.Handler) fanout {
	return fanout{handlers: handlers}
}

func newRecord(msg string) slog.Record {
	return slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
}

// TestFanout_Enabled_AnyEnabled verifies Enabled=true when at least one child
// handler is enabled.
func TestFanout_Enabled_AnyEnabled(t *testing.T) {
	f := makeFanout(&testDisabledHandler{}, &bufHandler{buf: &bytes.Buffer{}})
	if !f.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled must return true if any child is enabled")
	}
}

// TestFanout_Enabled_NoneEnabled verifies Enabled=false when all children are
// disabled.
func TestFanout_Enabled_NoneEnabled(t *testing.T) {
	f := makeFanout(&testDisabledHandler{}, &testDisabledHandler{})
	if f.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled must return false when all children are disabled")
	}
}

// TestFanout_Handle_DispatchesToAll verifies Handle routes to every enabled
// child.
func TestFanout_Handle_DispatchesToAll(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	f := makeFanout(&bufHandler{buf: &buf1}, &bufHandler{buf: &buf2})
	if err := f.Handle(context.Background(), newRecord("dispatch")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buf1.String(), "dispatch") || !strings.Contains(buf2.String(), "dispatch") {
		t.Fatalf("not dispatched to both handlers: buf1=%q buf2=%q", buf1.String(), buf2.String())
	}
}

// TestFanout_Handle_SkipsDisabled verifies that a disabled child is not called.
func TestFanout_Handle_SkipsDisabled(t *testing.T) {
	var buf bytes.Buffer
	f := makeFanout(&testDisabledHandler{}, &bufHandler{buf: &buf})
	if err := f.Handle(context.Background(), newRecord("skip-test")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buf.String(), "skip-test") {
		t.Fatalf("enabled handler not called: %q", buf.String())
	}
}

// TestFanout_Handle_ReturnsFirstError verifies the first error is returned
// and subsequent handlers still run.
func TestFanout_Handle_ReturnsFirstError(t *testing.T) {
	want := errors.New("first-fail")
	var buf bytes.Buffer
	f := makeFanout(&testErrHandler{err: want}, &bufHandler{buf: &buf})
	err := f.Handle(context.Background(), newRecord("err-test"))
	if !errors.Is(err, want) {
		t.Fatalf("expected first error %v, got %v", want, err)
	}
	// Second handler still called even after first error.
	if !strings.Contains(buf.String(), "err-test") {
		t.Fatalf("second handler not called after first error: %q", buf.String())
	}
}

// TestFanout_WithAttrs verifies attribute propagation to all children.
func TestFanout_WithAttrs(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	f := makeFanout(&bufHandler{buf: &buf1}, &bufHandler{buf: &buf2})
	derived := f.WithAttrs([]slog.Attr{slog.String("key", "val")})
	if err := derived.Handle(context.Background(), newRecord("with-attrs")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for i, s := range []string{buf1.String(), buf2.String()} {
		if !strings.Contains(s, "key=val") {
			t.Fatalf("handler %d: attr key=val missing: %q", i, s)
		}
	}
}

// TestFanout_WithGroup verifies group name propagation to all children.
func TestFanout_WithGroup(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	f := makeFanout(&bufHandler{buf: &buf1}, &bufHandler{buf: &buf2})
	derived := f.WithGroup("grp")
	fa, ok := derived.(fanout)
	if !ok {
		t.Fatal("WithGroup must return a fanout")
	}
	for i, h := range fa.handlers {
		bh, ok := h.(*bufHandler)
		if !ok {
			t.Fatalf("handler %d is not a *bufHandler", i)
		}
		if bh.group != "grp" {
			t.Fatalf("handler %d: group = %q, want grp", i, bh.group)
		}
	}
}
