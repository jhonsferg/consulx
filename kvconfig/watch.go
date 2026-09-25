package kvconfig

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/internal/blocking"
)

// WatchOption configures Watch.
type WatchOption[T any] func(*watchOptions[T])

type watchOptions[T any] struct {
	path     string
	validate func(T) error
	onChange func(old, new T)
}

// WithPath binds only the subtree at path, e.g. "database".
func WithPath[T any](path string) WatchOption[T] {
	return func(o *watchOptions[T]) { o.path = path }
}

// WithValidator adds a validation step run after Validate. A change that
// fails validation is rejected and the previous value stays current.
func WithValidator[T any](fn func(T) error) WatchOption[T] {
	return func(o *watchOptions[T]) { o.validate = fn }
}

// OnChange registers a callback run, in the watcher goroutine, after a
// valid new value is published. It must not block for long.
func OnChange[T any](fn func(old, new T)) WatchOption[T] {
	return func(o *watchOptions[T]) { o.onChange = fn }
}

// Watcher keeps a validated, up-to-date configuration value. Values are
// immutable snapshots: a new value is built for every change, so readers
// never observe a partially updated configuration. Treat returned values as
// read-only; they may share maps and slices with other readers.
type Watcher[T any] struct {
	current atomic.Pointer[T]
	changes chan T
	errs    chan error
	cancel  context.CancelFunc
	done    chan struct{}
}

// Watch loads the configuration, binds and validates it, and keeps it up to
// date. It fails when the initial configuration cannot be loaded or is
// invalid, so the caller decides whether to start without it. Afterwards,
// invalid changes are rejected and reported on Errors while the previous
// value stays current; every folder watcher wakes on a write, so a given
// rejected state is reported once, not once per folder. The watch stops
// when ctx is done or Close is called.
func Watch[T any](ctx context.Context, l *Loader, opts ...WatchOption[T]) (*Watcher[T], error) {
	var o watchOptions[T]
	for _, fn := range opts {
		fn(&o)
	}
	first, _, err := buildValue(ctx, l, o)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx) // #nosec G118 -- cancel is kept in w.cancel and called by Close and by run on exit
	w := &Watcher[T]{
		changes: make(chan T, 1),
		errs:    make(chan error, 1),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	w.current.Store(&first)
	go w.run(ctx, l, o)
	return w, nil
}

// Current returns the latest valid value.
func (w *Watcher[T]) Current() T { return *w.current.Load() }

// Changes delivers each new valid value. Delivery coalesces: a slow reader
// gets the latest value, not a backlog. Closed when the watch stops.
func (w *Watcher[T]) Changes() <-chan T { return w.changes }

// Errors delivers reload failures and rejected changes. Only the latest
// undelivered error is kept. Closed when the watch stops.
func (w *Watcher[T]) Errors() <-chan error { return w.errs }

// Done is closed when the watch has stopped.
func (w *Watcher[T]) Done() <-chan struct{} { return w.done }

// Close stops the watch and waits for its goroutines.
func (w *Watcher[T]) Close() error {
	w.cancel()
	<-w.done
	return nil
}

// buildValue reads, binds and validates the configuration. It returns the
// tree it read: a nil tree means Consul could not be read, so nothing was
// evaluated and nothing was rejected.
func buildValue[T any](ctx context.Context, l *Loader, o watchOptions[T]) (v T, tree map[string]any, err error) {
	tree, err = l.Load(ctx)
	if err != nil {
		return v, nil, err
	}
	if err := l.bindTree(tree, o.path, &v); err != nil {
		return v, tree, err
	}
	if o.validate != nil {
		if err := o.validate(v); err != nil {
			return v, tree, wrapInvalid(err)
		}
	}
	return v, tree, nil
}

func wrapInvalid(err error) error { return &invalidError{err} }

type invalidError struct{ err error }

func (e *invalidError) Error() string   { return ErrInvalid.Error() + ": " + e.err.Error() }
func (e *invalidError) Unwrap() []error { return []error{ErrInvalid, e.err} }

// run starts one blocking watcher per context folder and reloads the whole
// configuration whenever any of them reports a change.
//
// Purpose: keep Current up to date. Exit condition: ctx is cancelled; it
// waits for its folder watchers before closing the channels.
func (w *Watcher[T]) run(ctx context.Context, l *Loader, o watchOptions[T]) {
	changed := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for _, prefix := range l.Contexts() {
		wg.Go(func() { l.watchPrefix(ctx, prefix, changed, w.publishErr) })
	}
	defer func() {
		w.cancel() // release the context even if the parent never ends it
		wg.Wait()
		close(w.changes)
		close(w.errs)
		close(w.done)
	}()
	// rejectedTree is the configuration last rejected: the same state is
	// reported once, while any change (even one failing with the same
	// message) is reported again.
	var rejectedTree map[string]any
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		// Every folder watcher wakes on a write, so several signals can
		// describe the same change: one reload answers all of them.
		coalesce(changed)

		next, tree, err := buildValue(ctx, l, o)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if tree == nil {
				// Consul did not answer: the configuration was never
				// evaluated, so nothing was rejected.
				l.cfg.Observe("reload", err)
				l.cfg.Logger.Warn("configuration reload failed, keeping the previous value",
					slog.Any("error", err))
				w.publishErr(err)
				continue
			}
			// The same write reaches every watcher, so one rejected state
			// can be rebuilt more than once: report it once, until the
			// configuration changes.
			if rejectedTree != nil && reflect.DeepEqual(tree, rejectedTree) {
				continue
			}
			rejectedTree = tree
			l.cfg.Observe("reject", err)
			l.cfg.Logger.Warn("configuration rejected, keeping the previous value", slog.Any("error", err))
			w.publishErr(err)
			continue
		}
		rejectedTree = nil
		old := w.Current()
		if reflect.DeepEqual(old, next) {
			continue
		}
		w.current.Store(&next)
		l.cfg.Observe("reload", nil)
		l.cfg.Logger.Info("configuration changed")
		select {
		case <-w.changes:
		default:
		}
		w.changes <- next
		if o.onChange != nil {
			o.onChange(old, next)
		}
	}
}

// coalesce drains the change signals queued while the caller was busy: they
// all describe configuration states that one reload reads in full.
func coalesce(changed <-chan struct{}) {
	for {
		select {
		case <-changed:
		default:
			return
		}
	}
}

func (w *Watcher[T]) publishErr(err error) {
	select {
	case <-w.errs:
	default:
	}
	select {
	case w.errs <- err:
	default:
	}
}

// watchPrefix blocks on one folder and signals changed after each change of
// its index. Failures are retried with backoff and reported.
func (l *Loader) watchPrefix(ctx context.Context, prefix string, changed chan<- struct{}, report func(error)) {
	limiter := blocking.NewLimiter(l.cfg.MinInterval, 2)
	var index uint64
	var hadKeys bool
	failures := 0
	for {
		if limiter.Wait(ctx) != nil {
			return
		}
		// Bound each blocking request (see blocking.RequestTimeout).
		rctx, cancel := context.WithTimeout(ctx, blocking.RequestTimeout(l.cfg.WaitTime, l.cfg.RequestTimeout))
		q := (&api.QueryOptions{WaitIndex: index, WaitTime: l.cfg.WaitTime}).WithContext(rctx)
		keys, meta, err := l.kv.Keys(prefix, "", q)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			l.cfg.Observe("reload", err)
			report(err)
			delay, ok := l.cfg.Retry.NextDelay(failures)
			if !ok {
				delay = l.cfg.WaitTime / 10
			}
			l.cfg.Logger.Warn("configuration watch failed, retry scheduled",
				slog.String("prefix", prefix), slog.Duration("delay", delay), slog.Any("error", err))
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			index = 0
			continue
		}
		prev := index
		index = blocking.NextIndex(index, meta.LastIndex)
		// Signal on the first response too: a change made between the
		// initial load and this response would otherwise be lost. The
		// reload is cheap and publishes nothing if the value is unchanged.
		//
		// A folder that holds no keys only signals the first time and after
		// a failure: it contributes nothing to the merged configuration, and
		// Consul answers its blocking query on every write of the store,
		// because a prefix without keys has no index of its own. Reloading
		// on those writes only rebuilt an unchanged configuration, which
		// re-reported the same rejected value once per folder.
		if prev == 0 || failures > 0 || (index != prev && (len(keys) > 0 || hadKeys)) {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
		hadKeys = len(keys) > 0
		failures = 0
	}
}
