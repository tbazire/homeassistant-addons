// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: subprocess.go — lifecycle of the eebusd child process.
//
// The bridge owns eebusd: it starts it, pipes its stdout (NDJSON stream) to a
// caller-provided reader, mirrors its stderr to our stdout (HA logs), and
// supervises restarts. If eebusd keeps crashing, the bridge gives up and exits
// so s6-overlay restarts the whole add-on.

package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Subprocess manages a child process with bounded auto-restart.
type Subprocess struct {
	binPath string
	args    []string
	logger  Logger

	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	runCtx context.Context
}

// NewSubprocess returns a manager for one binary + args.
func NewSubprocess(binPath string, args []string, logger Logger) *Subprocess {
	return &Subprocess{binPath: binPath, args: args, logger: logger}
}

// Run starts the subprocess and blocks until ctx is cancelled or the process
// exits permanently (maxRestarts consecutive failures). stdoutPipe receives
// the child's NDJSON stream; the child's stderr is mirrored to our stdout.
//
// onStdout is called with a reader the caller can stream-parse. It is invoked
// once per (re)start, so the caller must tolerate multiple calls (e.g. by
// resetting its parser state).
func (s *Subprocess) Run(parent context.Context, maxRestarts int, onStdout func(io.Reader)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	s.mu.Lock()
	s.runCtx = ctx
	s.cancel = cancel
	s.mu.Unlock()

	consecutiveFailures := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		cmd := exec.CommandContext(ctx, s.binPath, s.args...)
		// Pipe stdout (data) and stderr (logs) separately.
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("stdout pipe: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("stderr pipe: %w", err)
		}

		// Mirror child stderr to our stdout so HA captures it. (Our own logs
		// already go to stdout; this keeps the eebusd logs alongside.)
		go mirror(stderr, os.Stdout)

		s.logger.Info("starting eebusd", "bin", s.binPath, "args", s.args)
		if err := cmd.Start(); err != nil {
			consecutiveFailures++
			if consecutiveFailures > maxRestarts {
				return fmt.Errorf("start eebusd (%d attempts): %w", consecutiveFailures, err)
			}
			s.logger.Warn("eebusd start failed, backing off", "err", err.Error(),
				"attempt", consecutiveFailures, "max", maxRestarts)
			if !sleepCtx(ctx, backoff(consecutiveFailures)) {
				return ctx.Err()
			}
			continue
		}

		s.mu.Lock()
		s.cmd = cmd
		s.mu.Unlock()

		// Hand stdout to the caller for as long as this process lives.
		// We do NOT cancel the pipe on restart; the next loop iteration will
		// create a fresh pipe and call onStdout again.
		parsingDone := make(chan struct{})
		go func() {
			defer close(parsingDone)
			onStdout(stdout)
		}()

		// Wait for the process to exit.
		waitErr := cmd.Wait()
		// Make sure the parser has drained before we restart.
		<-parsingDone

		if ctx.Err() != nil {
			// We were asked to stop. Treat the exit as graceful regardless.
			s.logger.Info("eebusd stopped (shutdown)")
			return nil
		}

		if waitErr != nil {
			consecutiveFailures++
			s.logger.Warn("eebusd exited with error", "err", waitErr.Error(),
				"attempt", consecutiveFailures, "max", maxRestarts)
			if consecutiveFailures > maxRestarts {
				return fmt.Errorf("eebusd crashed %d times in a row: %w", consecutiveFailures, waitErr)
			}
			if !sleepCtx(ctx, backoff(consecutiveFailures)) {
				return ctx.Err()
			}
			continue
		}

		// Clean exit (return code 0) with no shutdown requested is unusual;
		// restart once in case it was a transient condition.
		consecutiveFailures = 0
		s.logger.Info("eebusd exited cleanly, restarting")
	}
}

// Stop signals the subprocess to terminate gracefully. Called by the
// orchestrator on SIGTERM.
func (s *Subprocess) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		// SIGTERM first; eebusd handles it and tears down mDNS.
		_ = cmd.Process.Signal(os.Interrupt)
	}
	_ = cancel
}

// mirror copies src to dst until src returns EOF.
func mirror(src io.Reader, dst io.Writer) {
	_, _ = io.Copy(dst, src)
}

// backoff returns a sleep duration proportional to the attempt number, capped
// at 30s. attempt starts at 1.
func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 2 * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// sleepCtx sleeps for d, returning false if ctx was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
